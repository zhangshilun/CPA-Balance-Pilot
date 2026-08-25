package main

import "time"

const (
	SourceSub2API = "sub2api"
	SourceNewAPI  = "new-api"
)

// ProviderInput 仅在管理接口和探测边界传递凭据。
// 其中的敏感字段不会出现在状态响应中。
type ProviderInput struct {
	ID                  string `json:"id,omitempty"`
	SourceType          string `json:"source_type"`
	SourceRef           string `json:"source_ref"`
	Name                string `json:"name"`
	BaseURL             string `json:"base_url"`
	APIKey              string `json:"api_key,omitempty"`
	NewAPIUser          string `json:"new_api_user,omitempty"`
	NewAPIAuthorization string `json:"new_api_authorization,omitempty"`
}

// Provider 是不包含敏感信息的公共供应商配置。
type Provider struct {
	ID                string           `json:"id"`
	SourceType        string           `json:"source_type"`
	SourceRef         string           `json:"source_ref"`
	Name              string           `json:"name"`
	BaseURL           string           `json:"base_url"`
	APIKeyFingerprint string           `json:"api_key_fingerprint"`
	CreatedAt         time.Time        `json:"created_at"`
	UpdatedAt         time.Time        `json:"updated_at"`
	Latest            *BalanceSnapshot `json:"latest,omitempty"`
}

type UsageSummary struct {
	Balance            *float64 `json:"balance,omitempty"`
	TotalRecharged     *float64 `json:"total_recharged,omitempty"`
	UsedUsage          *float64 `json:"used_usage,omitempty"`
	TotalRequests      *float64 `json:"total_requests,omitempty"`
	TotalInputTokens   *float64 `json:"total_input_tokens,omitempty"`
	TotalOutputTokens  *float64 `json:"total_output_tokens,omitempty"`
	TotalCacheCreation *float64 `json:"total_cache_creation_tokens,omitempty"`
	TotalCacheRead     *float64 `json:"total_cache_read_tokens,omitempty"`
	TotalTokens        *float64 `json:"total_tokens,omitempty"`
	TotalCost          *float64 `json:"total_cost,omitempty"`
	TodayRequests      *float64 `json:"today_requests,omitempty"`
	TodayInputTokens   *float64 `json:"today_input_tokens,omitempty"`
	TodayOutputTokens  *float64 `json:"today_output_tokens,omitempty"`
	TodayCacheCreation *float64 `json:"today_cache_creation_tokens,omitempty"`
	TodayCacheRead     *float64 `json:"today_cache_read_tokens,omitempty"`
	TodayTokens        *float64 `json:"today_tokens,omitempty"`
	TodayCost          *float64 `json:"today_cost,omitempty"`
	TodayActualCost    *float64 `json:"today_actual_cost,omitempty"`
}

type BillingInfo struct {
	KeyStatus      string   `json:"key_status,omitempty"`
	GroupName      string   `json:"group_name,omitempty"`
	GroupPlatform  string   `json:"group_platform,omitempty"`
	RateMultiplier *float64 `json:"rate_multiplier,omitempty"`
}

// BalanceSnapshot 是供应商之间统一使用的数据模型。
type BalanceSnapshot struct {
	ID         int64        `json:"id,omitempty"`
	BatchID    string       `json:"batch_id,omitempty"`
	ProviderID string       `json:"provider_id"`
	ObservedAt time.Time    `json:"observed_at"`
	Success    bool         `json:"success"`
	Unit       string       `json:"unit,omitempty"`
	Status     string       `json:"status,omitempty"`
	Usage      UsageSummary `json:"usage"`
	Billing    BillingInfo  `json:"billing"`
	Error      string       `json:"error,omitempty"`
}

type RefreshBatch struct {
	ID, StartedAt, FinishedAt string
	Success, Failed           int
}

type Settings struct {
	Concurrency            int  `json:"concurrency"`
	TimeoutSeconds         int  `json:"timeout_seconds"`
	RetryCount             int  `json:"retry_count"`
	AutoRefresh            bool `json:"auto_refresh"`
	RefreshIntervalMinutes int  `json:"refresh_interval_minutes"`
}

func defaultSettings() Settings {
	return Settings{Concurrency: 4, TimeoutSeconds: 20, RetryCount: 1, AutoRefresh: true, RefreshIntervalMinutes: 1}
}

func float64Ptr(v float64) *float64 { return &v }
