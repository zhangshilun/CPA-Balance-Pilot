package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	key := make([]byte, 32)
	store, err := openStore(t.TempDir(), key)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = store.db.Close() })
	return store
}

func TestStoreEncryptsProviderCredentialsAndPersistsSnapshot(t *testing.T) {
	store := testStore(t)
	input := ProviderInput{SourceType: SourceSub2API, SourceRef: "source-a", Name: "Source A", BaseURL: "https://example.test", APIKey: "sk-secret-key"}
	provider, err := store.SaveProvider(context.Background(), input)
	if err != nil {
		t.Fatalf("save provider: %v", err)
	}
	var cipher []byte
	if err := store.db.QueryRow(`SELECT api_key_cipher FROM providers WHERE id=?`, provider.ID).Scan(&cipher); err != nil {
		t.Fatal(err)
	}
	if string(cipher) == input.APIKey {
		t.Fatal("API key was stored in plaintext")
	}
	batch, err := store.BeginBatch(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	snapshot := BalanceSnapshot{BatchID: batch, ProviderID: provider.ID, ObservedAt: time.Now(), Success: true, Unit: "USD", Status: "active", Usage: UsageSummary{Balance: float64Ptr(3.1)}}
	if err := store.SaveSnapshot(context.Background(), snapshot); err != nil {
		t.Fatalf("save snapshot: %v", err)
	}
	latest, err := store.latest(context.Background(), provider.ID)
	if err != nil || latest == nil {
		t.Fatalf("latest: %v %#v", err, latest)
	}
	if latest.Usage.Balance == nil || *latest.Usage.Balance != 3.1 {
		t.Fatalf("wrong latest snapshot: %#v", latest)
	}
}

func TestSaveProviderKeepsNewAPICredentialsWhenEditOmitsHeaders(t *testing.T) {
	store := testStore(t)
	ctx := context.Background()
	provider, err := store.SaveProvider(ctx, ProviderInput{SourceType: SourceNewAPI, SourceRef: "new-api-1", Name: "new-api", BaseURL: "https://example.test", APIKey: "sk-key", NewAPIUser: "user-1", NewAPIAuthorization: "Bearer secret"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.SaveProvider(ctx, ProviderInput{ID: provider.ID, SourceType: SourceNewAPI, SourceRef: "new-api-1", Name: "renamed", BaseURL: "https://example.test"}); err != nil {
		t.Fatal(err)
	}
	var userCipher, authCipher []byte
	if err := store.db.QueryRow(`SELECT new_api_user_cipher,new_api_authorization_cipher FROM providers WHERE id=?`, provider.ID).Scan(&userCipher, &authCipher); err != nil {
		t.Fatal(err)
	}
	user, _ := decryptSecret(store.key, userCipher)
	auth, _ := decryptSecret(store.key, authCipher)
	if user != "user-1" || auth != "Bearer secret" {
		t.Fatalf("new-api credentials changed on empty edit: user=%q auth=%q", user, auth)
	}
}

func TestSub2APIMapsBilling(t *testing.T) {
	data := sub2APICollected{
		Billing: sub2APIBillingResponse{BillingScope: "token", EffectiveRateMultiplier: .1, ObservedAt: "2026-08-24T14:57:13.221837001Z"},
		Usage:   sub2APIUsageResponse{Balance: 3.05411868, IsValid: true, Mode: "unrestricted", PlanName: "钱包余额", Unit: "USD", Usage: sub2APIUsageTotals{Today: sub2APIUsageAggregate{Requests: 446, TotalTokens: 43263849, ActualCost: 3.55665984}, Total: sub2APIUsageAggregate{Requests: 12889, TotalTokens: 1671008926, ActualCost: 82.4458810922}}},
	}
	snapshot := mapSub2APISnapshot("provider", data)
	if snapshot.Billing.GroupName != "钱包余额" || snapshot.Billing.RateMultiplier == nil || *snapshot.Billing.RateMultiplier != .1 {
		t.Fatalf("billing = %#v", snapshot.Billing)
	}
	if snapshot.Usage.Balance == nil || *snapshot.Usage.Balance != 3.05411868 || snapshot.Usage.TodayRequests == nil || *snapshot.Usage.TodayRequests != 446 {
		t.Fatalf("usage = %#v", snapshot.Usage)
	}
}

func TestSub2APIUsagePath(t *testing.T) {
	if got, want := sub2APIUsagePath(30, "Asia/Shanghai"), "/usage?days=30&timezone=Asia%2FShanghai"; got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
}

func TestNewAPIMapsMaskedKeyAndConvertsQuota(t *testing.T) {
	data := newAPICollected{APIKey: "sk-Abcd1234Wxyz", Status: newAPIStatus{QuotaDisplayType: "USD", QuotaPerUnit: 100}, User: newAPIUser{Quota: 250, UsedQuota: 50}, Tokens: newAPITokens{Items: []newAPIToken{{Key: "Abcd********Wxyz", Status: 1, Group: "Pro"}}}, Groups: map[string]newAPIGroup{"Pro": {Ratio: .15}}, Usage: []newAPIUsage{{ModelName: "gpt-5.6-sol", TokenUsed: 8, Count: 2, Quota: 75}}}
	snapshot := mapNewAPISnapshot("provider", data)
	if snapshot.Usage.Balance == nil || *snapshot.Usage.Balance != 2.5 {
		t.Fatalf("balance = %#v", snapshot.Usage.Balance)
	}
	if snapshot.Usage.UsedUsage == nil || *snapshot.Usage.UsedUsage != .5 {
		t.Fatalf("used usage = %#v", snapshot.Usage.UsedUsage)
	}
	if snapshot.Usage.TodayRequests == nil || *snapshot.Usage.TodayRequests != 2 || snapshot.Usage.TodayTokens == nil || *snapshot.Usage.TodayTokens != 8 || snapshot.Usage.TodayActualCost == nil || *snapshot.Usage.TodayActualCost != .75 {
		t.Fatalf("today usage = %#v", snapshot.Usage)
	}
	// new-api only exposes cumulative request count through /api/user/self.
	// Token totals are not available there and must remain unset.
	if snapshot.Usage.TotalRequests == nil || *snapshot.Usage.TotalRequests != 0 || snapshot.Usage.TotalTokens != nil {
		t.Fatalf("total usage = %#v", snapshot.Usage)
	}
	if snapshot.Billing.GroupName != "Pro" || snapshot.Billing.RateMultiplier == nil || *snapshot.Billing.RateMultiplier != .15 {
		t.Fatalf("billing = %#v", snapshot.Billing)
	}
}

func TestNewAPIProbeConvertsQuotaFromStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body string
		switch r.URL.Path {
		case newAPIStatusEndpoint:
			body = `{"success":true,"data":{"quota_display_type":"USD","quota_per_unit":500000},"message":""}`
		case newAPIUserSelfEndpoint:
			body = `{"success":true,"data":{"quota":1205283,"used_quota":1294717,"request_count":642},"message":""}`
		default:
			body = `{"success":true,"data":{},"message":""}`
		}
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()

	input := ProviderInput{ID: "provider", BaseURL: server.URL, APIKey: "sk-Abcd1234Wxyz", NewAPIUser: "user", NewAPIAuthorization: "Bearer token"}
	snapshot, err := (newAPISource{}).Probe(context.Background(), input, defaultSettings(), newProviderClient(defaultSettings()))
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Usage.Balance == nil || *snapshot.Usage.Balance != 1205283.0/500000.0 {
		t.Fatalf("balance = %#v", snapshot.Usage.Balance)
	}
	if snapshot.Usage.UsedUsage == nil || *snapshot.Usage.UsedUsage != 1294717.0/500000.0 {
		t.Fatalf("used usage = %#v", snapshot.Usage.UsedUsage)
	}
}

func TestNewAPIProbeRequestsSequentiallyBeforeAssembly(t *testing.T) {
	var order []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		order = append(order, r.URL.Path)
		var body string
		switch r.URL.Path {
		case newAPIStatusEndpoint:
			body = `{"success":true,"data":{"quota_display_type":"USD","quota_per_unit":100},"message":""}`
		case newAPIUserSelfEndpoint:
			body = `{"success":true,"data":{"quota":100,"used_quota":50,"request_count":1},"message":""}`
		case newAPITokensEndpoint:
			body = `{"success":true,"data":{"items":[]},"message":""}`
		case newAPIGroupsEndpoint:
			body = `{"success":true,"data":{},"message":""}`
		default:
			body = `{"success":true,"data":[],"message":""}`
		}
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()
	input := ProviderInput{ID: "provider", BaseURL: server.URL, APIKey: "sk-Abcd1234Wxyz", NewAPIUser: "user", NewAPIAuthorization: "Bearer token"}
	if _, err := (newAPISource{}).Probe(context.Background(), input, defaultSettings(), newProviderClient(defaultSettings())); err != nil {
		t.Fatal(err)
	}
	want := []string{newAPIStatusEndpoint, newAPIUserSelfEndpoint, newAPITokensEndpoint, newAPIGroupsEndpoint, newAPIDataSelfEndpoint}
	if len(order) != len(want) {
		t.Fatalf("request order = %#v, want %#v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("request order = %#v, want %#v", order, want)
		}
	}
}

func TestNewAPIProbeFiltersUsageRowsOutsideRequestedDay(t *testing.T) {
	today := time.Now().In(shanghai())
	start := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, shanghai())
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body string
		switch r.URL.Path {
		case newAPIStatusEndpoint:
			body = `{"success":true,"data":{"quota_per_unit":500000},"message":""}`
		case newAPIUserSelfEndpoint:
			body = `{"success":true,"data":{"quota":100,"used_quota":50,"request_count":1},"message":""}`
		case newAPIDataSelfEndpoint:
			body = fmt.Sprintf(`{"success":true,"data":[{"created_at":%d,"token_used":7,"count":1,"quota":1},{"created_at":%d,"token_used":99,"count":9,"quota":1}],"message":""}`, start.Unix(), start.Add(-time.Second).Unix())
		default:
			body = `{"success":true,"data":{},"message":""}`
		}
		_, _ = w.Write([]byte(body))
	}))
	defer server.Close()
	settings := defaultSettings()
	input := ProviderInput{ID: "provider", BaseURL: server.URL, APIKey: "sk-Abcd1234Wxyz", NewAPIUser: "user", NewAPIAuthorization: "Bearer token"}
	snapshot, err := (newAPISource{}).Probe(context.Background(), input, settings, newProviderClient(settings))
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Usage.TodayTokens == nil || *snapshot.Usage.TodayTokens != 7 {
		t.Fatalf("today tokens = %#v", snapshot.Usage.TodayTokens)
	}
}

func TestNewAPIManagementBaseURLStripsV1Suffix(t *testing.T) {
	for _, test := range []struct{ raw, want string }{
		{"https://www.hejuapi.com/v1", "https://www.hejuapi.com"},
		{"https://www.hejuapi.com/v1/", "https://www.hejuapi.com"},
		{"https://www.hejuapi.com", "https://www.hejuapi.com"},
	} {
		if got := newAPIManagementBaseURL(test.raw); got != test.want {
			t.Fatalf("newAPIManagementBaseURL(%q) = %q, want %q", test.raw, got, test.want)
		}
	}
}

func TestNewAPIStatusParsesQuotedQuotaPerUnit(t *testing.T) {
	var status newAPIStatus
	if err := json.Unmarshal([]byte(`{"quota_display_type":"USD","quota_per_unit":"500000"}`), &status); err != nil {
		t.Fatal(err)
	}
	if status.QuotaPerUnit != 500000 {
		t.Fatalf("quota_per_unit = %v", status.QuotaPerUnit)
	}
}

func TestNewAPIMissingQuotaPerUnitUsesStandardDefault(t *testing.T) {
	snapshot := mapNewAPISnapshot("provider", newAPICollected{User: newAPIUser{Quota: 1205283, UsedQuota: 1294717}})
	if snapshot.Usage.Balance == nil || *snapshot.Usage.Balance != 1205283.0/newAPIDefaultQuotaPerUnit {
		t.Fatalf("balance = %#v", snapshot.Usage.Balance)
	}
}

func TestNewAPIGroupParsesStringRatioAndDoesNotInventZero(t *testing.T) {
	var group newAPIGroup
	if err := json.Unmarshal([]byte(`{"desc":"Pro","ratio":"0.15"}`), &group); err != nil {
		t.Fatal(err)
	}
	if group.Ratio != .15 {
		t.Fatalf("ratio = %v", group.Ratio)
	}
	data := newAPICollected{APIKey: "sk-Abcd1234Wxyz", Status: newAPIStatus{QuotaPerUnit: 100}, User: newAPIUser{Quota: 100}, Tokens: newAPITokens{Items: []newAPIToken{{Key: "Abcd********Wxyz", Status: 1, Group: "Missing"}}}, Groups: map[string]newAPIGroup{}}
	snapshot := mapNewAPISnapshot("provider", data)
	if snapshot.Billing.RateMultiplier != nil {
		t.Fatalf("missing group must not become zero multiplier: %#v", snapshot.Billing)
	}
}

func TestNewAPIGroupsKeepNumericEntriesWhenAnotherRatioIsNonNumeric(t *testing.T) {
	var groups map[string]newAPIGroup
	if err := json.Unmarshal([]byte(`{"GPT K12":{"ratio":0.04},"auto":{"ratio":"自动"}}`), &groups); err != nil {
		t.Fatal(err)
	}
	if groups["GPT K12"].Ratio != .04 {
		t.Fatalf("numeric ratio = %v", groups["GPT K12"].Ratio)
	}
}

func TestNewAPIQueryPaths(t *testing.T) {
	if got, want := newAPITokensPath(1, 100), "/api/token/?p=1&size=100"; got != want {
		t.Fatalf("token path = %q, want %q", got, want)
	}
	start := time.Unix(1787500800, 0)
	end := time.Unix(1787587199, 0)
	if got, want := newAPIDataSelfPath(start, end), "/api/data/self?default_time=day&end_timestamp=1787587199&start_timestamp=1787500800"; got != want {
		t.Fatalf("usage path = %q, want %q", got, want)
	}
}

func TestHostCredentialUsesHostAuthGetWithoutExposingAPIKey(t *testing.T) {
	host := func(method string, payload any) (json.RawMessage, error) {
		if method != "host.auth.get" {
			t.Fatalf("method = %q", method)
		}
		request, ok := payload.(HostAuthGetRequest)
		if !ok || request.AuthIndex != "credential-1" {
			t.Fatalf("request = %#v", payload)
		}
		return json.RawMessage(`{"auth_index":"credential-1","name":"主线路","json":{"api_key":"sk-private","base_url":"https://upstream.example"}}`), nil
	}
	credential, err := getHostCredential(host, HostAuthFile{AuthIndex: "credential-1", Provider: "openai"})
	if err != nil {
		t.Fatal(err)
	}
	if credential.APIKey != "sk-private" || credential.BaseURL != "https://upstream.example" {
		t.Fatalf("credential = %#v", credential)
	}
	view := credential.CredentialView
	if raw, err := json.Marshal(view); err != nil {
		t.Fatal(err)
	} else if string(raw) == "" || strings.Contains(string(raw), "sk-private") {
		t.Fatalf("credential view leaked secret: %s", raw)
	}
}

func TestProviderRequestHeaders(t *testing.T) {
	t.Run("sub2api", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if got := r.Header.Get("Authorization"); got != "Bearer sk-from-cpa" {
				t.Errorf("Authorization = %q", got)
			}
			if got := r.Header.Get("Accept"); got != "application/json" {
				t.Errorf("Accept = %q", got)
			}
			_, _ = w.Write([]byte(`{"ok":true}`))
		}))
		defer server.Close()
		var target map[string]bool
		err := newProviderClient(defaultSettings()).getJSON(context.Background(), ProviderInput{BaseURL: server.URL}, "/", sub2APIHeaders("sk-from-cpa"), &target)
		if err != nil {
			t.Fatal(err)
		}
	})
	t.Run("new-api", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if got := r.Header.Get("New-Api-User"); got != "user-42" {
				t.Errorf("New-Api-User = %q", got)
			}
			if got := r.Header.Get("Authorization"); got != "Bearer separately-managed" {
				t.Errorf("Authorization = %q", got)
			}
			if got := r.Header.Get("Accept"); got != "application/json" {
				t.Errorf("Accept = %q", got)
			}
			_, _ = w.Write([]byte(`{"ok":true}`))
		}))
		defer server.Close()
		var target map[string]bool
		input := ProviderInput{BaseURL: server.URL, APIKey: "sk-from-cpa", NewAPIUser: "user-42", NewAPIAuthorization: "Bearer separately-managed"}
		err := newProviderClient(defaultSettings()).getJSON(context.Background(), input, "/", newAPIHeaders(input), &target)
		if err != nil {
			t.Fatal(err)
		}
	})
}

func TestLifecycleConfigurationCreatesSQLiteStore(t *testing.T) {
	directory := t.TempDir()
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	raw := []byte("CPA_BALANCE_PILOT_KEY: " + key + "\nCPA_BALANCE_PILOT_DATA_DIR: " + directory + "\n")
	configureForLifecycle(mustLifecycle(t, raw))
	t.Cleanup(closeRuntimeStore)
	if _, err := currentStore(); err != nil {
		t.Fatalf("configured store: %v", err)
	}
	if _, err := os.Stat(directory); err != nil {
		t.Fatal(err)
	}
}

func TestDefaultSettingsEnableOneMinuteRefresh(t *testing.T) {
	settings := defaultSettings()
	if !settings.AutoRefresh || settings.RefreshIntervalMinutes != 1 {
		t.Fatalf("defaults = %#v", settings)
	}
}
func mustLifecycle(t *testing.T, config []byte) []byte {
	t.Helper()
	raw, err := json.Marshal(LifecycleRequest{ConfigYAML: config})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}
