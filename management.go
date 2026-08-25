package main

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/google/uuid"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	managementPrefix = "/v0/management/plugins/" + pluginID
	statePath        = managementPrefix + apiPathPrefix + "/state"
	providersPath    = managementPrefix + apiPathPrefix + "/providers"
	refreshPath      = managementPrefix + apiPathPrefix + "/refresh"
	credentialsPath  = managementPrefix + apiPathPrefix + "/credentials"
)

var managementRoutes = []ManagementRoute{{Method: http.MethodGet, Path: statePath, Description: "读取供应商当前状态。"}, {Method: http.MethodGet, Path: credentialsPath, Description: "读取 CPA 宿主凭证。"}, {Method: http.MethodPost, Path: providersPath, Description: "保存供应商监控配置。"}, {Method: http.MethodDelete, Path: providersPath, Description: "删除供应商。"}, {Method: http.MethodPost, Path: refreshPath, Description: "同步供应商当前数据。"}}
var managementResources = []ResourceRoute{{Path: resourcePath, Menu: pluginMenu, Description: "查看供应商余额、用量和历史记录。"}}

func handleMethod(method string, request []byte, host HostCall) ([]byte, error) {
	switch method {
	case methodPluginRegister, methodPluginReconfigure:
		configureForLifecycle(request)
		return OK(pluginRegistration())
	case methodManagementRegister:
		return OK(ManagementRegistration{Routes: managementRoutes, Resources: managementResources})
	case methodManagementHandle:
		return handleManagement(request, host)
	default:
		return Error("unknown_method", "未知 CPA 插件方法: "+method), nil
	}
}
func pluginRegistration() Registration {
	return Registration{SchemaVersion: rpcSchemaVersion, Metadata: Metadata{Name: pluginName, Version: pluginVersion, Author: pluginAuthor, GitHubRepository: pluginRepository, ConfigFields: []ConfigField{{Name: balanceKeyConfigKey, Type: "string", EnumValues: []string{}, Description: "必填：Base64 编码的 32 字节 AES 密钥。"}, {Name: dataDirectoryConfigKey, Type: "string", EnumValues: []string{}, Description: "可选：SQLite 数据目录，默认 /CLIProxyAPI/data/cpa-balance-pilot。"}}}, Capabilities: Capabilities{ManagementAPI: true}}
}

func handleManagement(raw []byte, host HostCall) ([]byte, error) {
	var request managementRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		return nil, fmt.Errorf("decode management request: %w", err)
	}
	method, path := strings.ToUpper(strings.TrimSpace(request.Method)), normalizePath(request.Path)
	if method == http.MethodGet && path == resourceFullPath {
		return OK(HTML(http.StatusOK, indexHTML()))
	}
	switch {
	case method == http.MethodGet && path == statePath:
		return stateResponse(host)
	case method == http.MethodGet && path == credentialsPath:
		return credentialsResponse(host)
	case method == http.MethodPost && path == providersPath:
		return saveProviderResponse(request.Body, host)
	case method == http.MethodDelete && path == providersPath:
		return deleteProviderResponse(request.Path)
	case method == http.MethodPost && path == refreshPath:
		return refreshResponse(request.Body)
	default:
		return OK(JSON(http.StatusNotFound, map[string]string{"error": "route_not_found"}))
	}
}
func normalizePath(value string) string {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err == nil && parsed.Path != "" {
		return parsed.Path
	}
	return strings.SplitN(value, "?", 2)[0]
}
func stateResponse(host HostCall) ([]byte, error) {
	store, err := currentStore()
	if err != nil {
		return OK(JSON(http.StatusServiceUnavailable, map[string]any{"status": configurationStatus()}))
	}
	providers, err := store.Providers(context.Background())
	if err != nil {
		return nil, err
	}
	settings, err := store.Settings(context.Background())
	if err != nil {
		return nil, err
	}
	credentials, credentialErr := listHostCredentials(host, providers)
	payload := map[string]any{"providers": providers, "credentials": credentials, "settings": settings, "status": configurationStatus()}
	if credentialErr != nil {
		payload["credentials_error"] = safeError(credentialErr)
	}
	return OK(JSON(http.StatusOK, payload))
}

type providerSaveRequest struct {
	ProviderInput
	AuthIndex string `json:"auth_index"`
	Enabled   bool   `json:"enabled"`
}

func saveProviderResponse(raw []byte, host HostCall) ([]byte, error) {
	store, err := currentStore()
	if err != nil {
		return OK(JSON(http.StatusServiceUnavailable, map[string]string{"error": safeError(err)}))
	}
	var request providerSaveRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		return OK(JSON(http.StatusBadRequest, map[string]string{"error": "invalid_provider"}))
	}
	if !request.Enabled || strings.TrimSpace(request.AuthIndex) == "" {
		return OK(JSON(http.StatusBadRequest, map[string]string{"error": "credential_enable_required"}))
	}
	credential := hostCredential{CredentialView: CredentialView{AuthIndex: request.AuthIndex, Name: request.Name, BaseURL: request.BaseURL}, APIKey: strings.TrimSpace(request.APIKey)}
	if credential.APIKey == "" {
		var credentialErr error
		credential, credentialErr = getHostCredential(host, HostAuthFile{AuthIndex: request.AuthIndex})
		if credentialErr != nil {
			return OK(JSON(http.StatusBadRequest, map[string]string{"error": safeError(credentialErr)}))
		}
	}
	input := request.ProviderInput
	input.SourceRef, input.APIKey = request.AuthIndex, credential.APIKey
	if input.Name == "" {
		input.Name = credential.Name
	}
	if input.BaseURL == "" {
		input.BaseURL = credential.BaseURL
	}
	if previous, lookupErr := store.ProviderBySource(context.Background(), input.SourceType, input.SourceRef); lookupErr == nil {
		input.ID = previous.ID
	}
	provider, err := store.SaveProvider(context.Background(), input)
	if err != nil {
		return OK(JSON(http.StatusBadRequest, map[string]string{"error": safeError(err)}))
	}
	return OK(JSON(http.StatusOK, map[string]any{"provider": provider}))
}
func credentialsResponse(host HostCall) ([]byte, error) {
	store, err := currentStore()
	if err != nil {
		return nil, err
	}
	providers, err := store.Providers(context.Background())
	if err != nil {
		return nil, err
	}
	credentials, err := listHostCredentials(host, providers)
	if err != nil {
		return OK(JSON(http.StatusBadGateway, map[string]string{"error": safeError(err)}))
	}
	return OK(JSON(http.StatusOK, map[string]any{"credentials": credentials}))
}
func deleteProviderResponse(path string) ([]byte, error) {
	store, err := currentStore()
	if err != nil {
		return nil, err
	}
	id := queryValue(path, "id")
	if id == "" {
		return OK(JSON(http.StatusBadRequest, map[string]string{"error": "provider_id_required"}))
	}
	if err := store.DeleteProvider(context.Background(), id); err != nil {
		return nil, err
	}
	return OK(JSON(http.StatusOK, map[string]string{"status": "deleted"}))
}
func queryValue(path, key string) string {
	parsed, err := url.Parse(path)
	if err != nil {
		return ""
	}
	return parsed.Query().Get(key)
}

type refreshRequest struct {
	ProviderIDs []string `json:"provider_ids"`
}

func refreshResponse(raw []byte) ([]byte, error) {
	store, err := currentStore()
	if err != nil {
		return nil, err
	}
	var request refreshRequest
	if len(raw) > 0 && json.Unmarshal(raw, &request) != nil {
		return OK(JSON(http.StatusBadRequest, map[string]string{"error": "invalid_refresh_request"}))
	}
	providers, err := store.Providers(context.Background())
	if err != nil {
		return nil, err
	}
	selected := map[string]bool{}
	for _, id := range request.ProviderIDs {
		selected[id] = true
	}
	ids := []string{}
	for _, provider := range providers {
		if len(selected) == 0 || selected[provider.ID] {
			ids = append(ids, provider.ID)
		}
	}
	batch, err := refreshProviders(context.Background(), store, ids)
	if err != nil {
		return nil, err
	}
	return OK(JSON(http.StatusOK, map[string]any{"sync_id": batch}))
}
func refreshProviders(ctx context.Context, store *Store, ids []string) (string, error) {
	settings, err := store.Settings(ctx)
	if err != nil {
		return "", err
	}
	batch := uuid.NewString()
	workers := settings.Concurrency
	if workers < 1 {
		workers = 1
	}
	if workers > 16 {
		workers = 16
	}
	jobs := make(chan string)
	var wg sync.WaitGroup
	var counts struct {
		sync.Mutex
		ok, failed int
	}
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range jobs {
				input, err := store.ProbeInput(ctx, id)
				snapshot := BalanceSnapshot{BatchID: batch, ProviderID: id, ObservedAt: time.Now(), Success: false, Error: safeError(err)}
				if err == nil {
					source, _ := sources.source(input.SourceType)
					snapshot, err = source.Probe(ctx, input, settings, newProviderClient(settings))
					snapshot.BatchID = batch
					snapshot.ProviderID = id
					if err != nil {
						snapshot.Success = false
						snapshot.Error = safeError(err)
					}
				}
				_ = store.SaveSnapshot(ctx, snapshot)
				counts.Lock()
				if snapshot.Success {
					counts.ok++
				} else {
					counts.failed++
				}
				counts.Unlock()
			}
		}()
	}
	for _, id := range ids {
		jobs <- id
	}
	close(jobs)
	wg.Wait()
	counts.Lock()
	defer counts.Unlock()
	return batch, store.FinishBatch(ctx, batch, counts.ok, counts.failed)
}
func safeError(err error) string {
	if err == nil {
		return ""
	}
	value := err.Error()
	for _, marker := range []string{"Bearer ", "sk-", "Authorization"} {
		if index := strings.Index(value, marker); index >= 0 {
			value = value[:index] + "[redacted]"
		}
	}
	if len(value) > 300 {
		value = value[:300]
	}
	return value
}
