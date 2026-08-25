package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// CredentialView 仅包含可安全返回给浏览器的 CPA 凭据元数据。
type CredentialView struct {
	AuthIndex, Name, Provider, Status, BaseURL, ProviderID string
	Disabled, Unavailable                                  bool
}

// hostCredential 仅在内存中使用，用于将 CPA API Key 从 host.auth.get
// 直接传递到插件加密存储，绝不会被序列化。
type hostCredential struct {
	CredentialView
	APIKey string
}

func listHostCredentials(host HostCall, providers []Provider) ([]CredentialView, error) {
	if host == nil {
		return nil, fmt.Errorf("CPA 宿主回调不可用")
	}
	raw, err := host("host.auth.list", map[string]any{})
	if err != nil {
		return nil, fmt.Errorf("读取 CPA 凭证列表: %w", err)
	}
	var listed HostAuthListResponse
	if err := json.Unmarshal(raw, &listed); err != nil {
		return nil, fmt.Errorf("解析 CPA 凭证列表: %w", err)
	}
	byRef := map[string]string{}
	for _, provider := range providers {
		byRef[provider.SourceRef] = provider.ID
	}
	views := make([]CredentialView, 0, len(listed.Files))
	for _, file := range listed.Files {
		if strings.TrimSpace(file.AuthIndex) == "" {
			continue
		}
		view := CredentialView{AuthIndex: file.AuthIndex, Name: firstNonEmpty(file.Name, file.Email, file.AuthIndex), Provider: file.Provider, Status: file.Status, Disabled: file.Disabled, Unavailable: file.Unavailable, ProviderID: byRef[file.AuthIndex]}
		if credential, err := getHostCredential(host, file); err == nil {
			view.BaseURL = credential.BaseURL
			if credential.Name != "" {
				view.Name = credential.Name
			}
		}
		views = append(views, view)
	}
	return views, nil
}

func getHostCredential(host HostCall, file HostAuthFile) (hostCredential, error) {
	if host == nil || strings.TrimSpace(file.AuthIndex) == "" {
		return hostCredential{}, fmt.Errorf("CPA 凭证索引不可用")
	}
	raw, err := host("host.auth.get", HostAuthGetRequest{AuthIndex: file.AuthIndex})
	if err != nil {
		return hostCredential{}, fmt.Errorf("读取 CPA 凭证详情: %w", err)
	}
	var response HostAuthGetResponse
	if err := json.Unmarshal(raw, &response); err != nil {
		return hostCredential{}, fmt.Errorf("解析 CPA 凭证详情: %w", err)
	}
	values := map[string]json.RawMessage{}
	if len(response.JSON) > 0 {
		_ = json.Unmarshal(response.JSON, &values)
	}
	credential := hostCredential{CredentialView: CredentialView{AuthIndex: file.AuthIndex, Name: firstNonEmpty(response.Name, file.Name, response.Email, file.Email, file.AuthIndex), Provider: file.Provider, Status: file.Status, Disabled: file.Disabled || response.Disabled, Unavailable: file.Unavailable}}
	credential.APIKey = firstJSONValue(values, "api_key", "apiKey", "key")
	credential.BaseURL = firstJSONValue(values, "base_url", "baseURL", "url")
	if credential.APIKey == "" {
		return hostCredential{}, fmt.Errorf("CPA 凭证不含 API Key")
	}
	return credential, nil
}

func firstJSONValue(values map[string]json.RawMessage, keys ...string) string {
	for _, key := range keys {
		var value string
		if raw := values[key]; len(raw) > 0 && json.Unmarshal(raw, &value) == nil && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value = strings.TrimSpace(value); value != "" {
			return value
		}
	}
	return ""
}
