package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	newAPIStatusEndpoint   = "/api/status"
	newAPIUserSelfEndpoint = "/api/user/self"
	newAPITokensEndpoint   = "/api/token/"
	newAPIGroupsEndpoint   = "/api/user/self/groups"
	newAPIDataSelfEndpoint = "/api/data/self"
	// 当 /api/status 未返回换算比例时，new-api 默认使用 500000。
	newAPIDefaultQuotaPerUnit = 500000.0
)

type newAPISource struct{}

func (newAPISource) SourceType() string { return SourceNewAPI }
func (newAPISource) Validate(input ProviderInput) error {
	if err := validateCommonProvider(input); err != nil {
		return err
	}
	if strings.TrimSpace(input.ID) == "" && strings.TrimSpace(input.NewAPIUser) == "" {
		return fmt.Errorf("new_api_user is required")
	}
	if strings.TrimSpace(input.ID) == "" && strings.TrimSpace(input.NewAPIAuthorization) == "" {
		return fmt.Errorf("new_api_authorization is required")
	}
	return nil
}

type newAPIEnvelope[T any] struct {
	Data    T      `json:"data"`
	Message string `json:"message"`
	Success bool   `json:"success"`
}
type newAPIStatus struct {
	QuotaDisplayType string  `json:"quota_display_type"`
	QuotaPerUnit     float64 `json:"quota_per_unit"`
}

// UnmarshalJSON 同时支持数字和带引号的数字格式；不同 new-api 部署可能返回这两种形式。
// 在换算金额前必须保留正确的额度换算比例。
func (s *newAPIStatus) UnmarshalJSON(raw []byte) error {
	var value struct {
		QuotaDisplayType string          `json:"quota_display_type"`
		QuotaPerUnit     json.RawMessage `json:"quota_per_unit"`
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	s.QuotaDisplayType = value.QuotaDisplayType
	if len(value.QuotaPerUnit) == 0 || string(value.QuotaPerUnit) == "null" {
		s.QuotaPerUnit = 0
		return nil
	}
	if err := json.Unmarshal(value.QuotaPerUnit, &s.QuotaPerUnit); err == nil {
		return nil
	}
	var quoted string
	if err := json.Unmarshal(value.QuotaPerUnit, &quoted); err != nil {
		return err
	}
	_, err := fmt.Sscan(strings.TrimSpace(quoted), &s.QuotaPerUnit)
	return err
}

type newAPIUser struct {
	Quota        float64 `json:"quota"`
	RequestCount float64 `json:"request_count"`
	UsedQuota    float64 `json:"used_quota"`
}
type newAPIToken struct {
	Key    string `json:"key"`
	Status int    `json:"status"`
	Group  string `json:"group"`
}
type newAPITokens struct {
	Items []newAPIToken `json:"items"`
}
type newAPIGroup struct {
	Ratio float64 `json:"ratio"`
}

func (g *newAPIGroup) UnmarshalJSON(raw []byte) error {
	var value struct {
		Ratio json.RawMessage `json:"ratio"`
	}
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	if len(value.Ratio) == 0 || string(value.Ratio) == "null" {
		g.Ratio = 0
		return nil
	}
	if err := json.Unmarshal(value.Ratio, &g.Ratio); err == nil {
		return nil
	}
	var quoted string
	if err := json.Unmarshal(value.Ratio, &quoted); err != nil {
		return err
	}
	if _, err := fmt.Sscan(strings.TrimSpace(quoted), &g.Ratio); err != nil {
		// 部分内置分组可能使用“自动”等非数字标记。
		// 保留分组信息，并让映射逻辑忽略不可用的倍率，不丢弃整个分组响应。
		g.Ratio = 0
	}
	return nil
}

type newAPIUsage struct {
	CreatedAt int64   `json:"created_at"`
	ModelName string  `json:"model_name"`
	TokenUsed float64 `json:"token_used"`
	Count     float64 `json:"count"`
	Quota     float64 `json:"quota"`
}
type newAPICollected struct {
	APIKey string
	Status newAPIStatus
	User   newAPIUser
	Tokens newAPITokens
	Groups map[string]newAPIGroup
	Usage  []newAPIUsage
}

// Probe 仅同步一个 new-api 供应商的当前状态。它先读取额度换算比例和余额，
// 任一请求失败都会终止本次同步。Token、分组信息和当日用量属于可选数据：
// 元数据不可用时组倍率保持为空；用量不可用时今日指标保持为空，但不丢弃余额结果。
func (newAPISource) Probe(ctx context.Context, input ProviderInput, settings Settings, client *providerClient) (BalanceSnapshot, error) {
	// CPA 凭据经常配置为带有 OpenAI 兼容 `/v1` 后缀的地址，
	// 而 new-api 管理接口位于主机根路径。
	input.BaseURL = newAPIManagementBaseURL(input.BaseURL)
	headers := newAPIHeaders(input)
	var status newAPIEnvelope[newAPIStatus]
	if err := client.getJSON(ctx, input, newAPIStatusEndpoint, headers, &status); err != nil {
		return BalanceSnapshot{}, fmt.Errorf("new-api status: %w", err)
	}
	if !status.Success {
		return BalanceSnapshot{}, fmt.Errorf("new-api status: %s", safeUpstreamMessage(status.Message))
	}
	var user newAPIEnvelope[newAPIUser]
	if err := client.getJSON(ctx, input, newAPIUserSelfEndpoint, headers, &user); err != nil {
		return BalanceSnapshot{}, fmt.Errorf("new-api user/self: %w", err)
	}
	if !user.Success {
		return BalanceSnapshot{}, fmt.Errorf("new-api user/self: %s", safeUpstreamMessage(user.Message))
	}
	var tokens newAPIEnvelope[newAPITokens]
	tokensErr := client.getJSON(ctx, input, newAPITokensPath(1, 100), headers, &tokens)
	var groups newAPIEnvelope[map[string]newAPIGroup]
	groupsErr := client.getJSON(ctx, input, newAPIGroupsEndpoint, headers, &groups)
	date := time.Now().In(shanghai())
	start := time.Date(date.Year(), date.Month(), date.Day(), 0, 0, 0, 0, shanghai())
	end := start.AddDate(0, 0, 1).Add(-time.Second)
	var usage newAPIEnvelope[[]newAPIUsage]
	path := newAPIDataSelfPath(start, end)
	usageErr := client.getJSON(ctx, input, path, headers, &usage)
	// 等所有请求完成后再组装结果。可选接口失败不会导致同步失败，
	// 但不会暴露未完整组装的对象。
	collected := newAPICollected{APIKey: input.APIKey, Status: status.Data, User: user.Data}
	if tokensErr == nil && tokens.Success {
		collected.Tokens = tokens.Data
	}
	if groupsErr == nil && groups.Success {
		collected.Groups = groups.Data
	}
	if usageErr == nil && usage.Success {
		for _, item := range usage.Data {
			// 部分 new-api 部署即使收到当天时间范围，也可能返回相邻日期数据。
			// 这里只统计属于当天的数据。
			if item.CreatedAt != 0 && (item.CreatedAt < start.Unix() || item.CreatedAt > end.Unix()) {
				continue
			}
			collected.Usage = append(collected.Usage, item)
		}
	}
	return mapNewAPISnapshot(input.ID, collected), nil
}

func newAPIManagementBaseURL(raw string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(raw), "/")
	if len(trimmed) >= 3 && strings.EqualFold(trimmed[len(trimmed)-3:], "/v1") {
		return strings.TrimRight(trimmed[:len(trimmed)-3], "/")
	}
	return trimmed
}

func newAPITokensPath(page, size int) string {
	if page < 1 {
		page = 1
	}
	if size < 1 {
		size = 100
	}
	query := url.Values{"p": {strconv.Itoa(page)}, "size": {strconv.Itoa(size)}}
	return newAPITokensEndpoint + "?" + query.Encode()
}

func newAPIDataSelfPath(start, end time.Time) string {
	query := url.Values{
		"start_timestamp": {strconv.FormatInt(start.Unix(), 10)},
		"end_timestamp":   {strconv.FormatInt(end.Unix(), 10)},
		"default_time":    {"day"},
	}
	return newAPIDataSelfEndpoint + "?" + query.Encode()
}

func newAPIHeaders(input ProviderInput) http.Header {
	h := http.Header{"Accept": {"application/json"}}
	// new-api 凭据独立于 CPA API Key；由于不同部署使用的认证方案不同，
	// 这里保存并原样转发 Authorization。
	h.Set("New-Api-User", strings.TrimSpace(input.NewAPIUser))
	h.Set("Authorization", strings.TrimSpace(input.NewAPIAuthorization))
	return h
}
func mapNewAPISnapshot(providerID string, data newAPICollected) BalanceSnapshot {
	unit := data.Status.QuotaDisplayType
	if unit == "" {
		unit = "USD"
	}
	quotaPerUnit := data.Status.QuotaPerUnit
	if quotaPerUnit <= 0 {
		quotaPerUnit = newAPIDefaultQuotaPerUnit
	}
	snapshot := BalanceSnapshot{ProviderID: providerID, ObservedAt: time.Now(), Success: true, Unit: unit, Status: "active", Usage: UsageSummary{Balance: newAPIMoney(data.User.Quota, quotaPerUnit), UsedUsage: newAPIMoney(data.User.UsedQuota, quotaPerUnit), TotalRequests: float64Ptr(data.User.RequestCount)}}
	for _, token := range data.Tokens.Items {
		if !matchesMaskedKey(data.APIKey, token.Key) {
			continue
		}
		billing := BillingInfo{KeyStatus: newAPIKeyStatus(token.Status), GroupName: token.Group}
		if group, ok := lookupNewAPIGroup(data.Groups, token.Group); ok {
			billing.RateMultiplier = float64Ptr(group.Ratio)
		}
		snapshot.Billing = billing
		break
	}
	var todayRequests, todayTokens, todayActualCost float64
	for _, item := range data.Usage {
		used := newAPIMoney(item.Quota, quotaPerUnit)
		todayRequests += item.Count
		todayTokens += item.TokenUsed
		if used != nil {
			todayActualCost += *used
		}
	}
	if len(data.Usage) > 0 {
		snapshot.Usage.TodayRequests = float64Ptr(todayRequests)
		snapshot.Usage.TodayTokens = float64Ptr(todayTokens)
		snapshot.Usage.TodayActualCost = float64Ptr(todayActualCost)
	}
	return snapshot
}

func lookupNewAPIGroup(groups map[string]newAPIGroup, name string) (newAPIGroup, bool) {
	if group, ok := groups[name]; ok {
		return group, true
	}
	want := strings.TrimSpace(name)
	for key, group := range groups {
		if strings.TrimSpace(key) == want {
			return group, true
		}
	}
	return newAPIGroup{}, false
}

func newAPIMoney(quota, quotaPerUnit float64) *float64 {
	if quotaPerUnit <= 0 {
		return nil
	}
	return float64Ptr(quota / quotaPerUnit)
}

func newAPIKeyStatus(status int) string {
	if status == 1 {
		return "active"
	}
	return "disabled"
}

func matchesMaskedKey(apiKey, masked string) bool {
	parts := strings.SplitN(apiKey, "-", 2)
	key := apiKey
	if len(parts) == 2 {
		key = parts[1]
	}
	if len(key) < 8 {
		return false
	}
	return strings.HasPrefix(masked, key[:4]) && strings.HasSuffix(masked, key[len(key)-4:])
}
