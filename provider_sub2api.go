package main

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	sub2APIBillingEndpoint = "/sub2api/billing"
	sub2APIUsageEndpoint   = "/usage"
	sub2APIUsageDays       = 1
	sub2APITimezone        = "Asia/Shanghai"
)

type sub2APISource struct{}

func (sub2APISource) SourceType() string                 { return SourceSub2API }
func (sub2APISource) Validate(input ProviderInput) error { return validateCommonProvider(input) }

// sub2APIBillingResponse 是 Sub2API 返回的当前 Key 计费信息。
type sub2APIBillingResponse struct {
	BillingScope            string  `json:"billing_scope"`
	ResolvedRateMultiplier  float64 `json:"resolved_rate_multiplier"`
	EffectiveRateMultiplier float64 `json:"effective_rate_multiplier"`
	ObservedAt              string  `json:"observed_at"`
}

// sub2APIUsageResponse 是 /v1/usage 返回的标准化用量报告。
type sub2APIUsageResponse struct {
	Balance  float64            `json:"balance"`
	IsValid  bool               `json:"isValid"`
	Mode     string             `json:"mode"`
	PlanName string             `json:"planName"`
	Unit     string             `json:"unit"`
	Usage    sub2APIUsageTotals `json:"usage"`
}

type sub2APIUsageTotals struct {
	Today sub2APIUsageAggregate `json:"today"`
	Total sub2APIUsageAggregate `json:"total"`
}

type sub2APIUsageAggregate struct {
	ActualCost          float64 `json:"actual_cost"`
	CacheCreationTokens float64 `json:"cache_creation_tokens"`
	CacheReadTokens     float64 `json:"cache_read_tokens"`
	Cost                float64 `json:"cost"`
	InputTokens         float64 `json:"input_tokens"`
	OutputTokens        float64 `json:"output_tokens"`
	Requests            float64 `json:"requests"`
	TotalTokens         float64 `json:"total_tokens"`
}

type sub2APICollected struct {
	Billing sub2APIBillingResponse
	Usage   sub2APIUsageResponse
}

func (sub2APISource) Probe(ctx context.Context, input ProviderInput, settings Settings, client *providerClient) (BalanceSnapshot, error) {
	headers := sub2APIHeaders(input.APIKey)
	var billing sub2APIBillingResponse
	if err := client.getJSON(ctx, input, sub2APIBillingEndpoint, headers, &billing); err != nil {
		return BalanceSnapshot{}, fmt.Errorf("sub2api billing: %w", err)
	}
	var usage sub2APIUsageResponse
	if err := client.getJSON(ctx, input, sub2APIUsagePath(sub2APIUsageDays, sub2APITimezone), headers, &usage); err != nil {
		return BalanceSnapshot{}, fmt.Errorf("sub2api usage: %w", err)
	}
	return mapSub2APISnapshot(input.ID, sub2APICollected{Billing: billing, Usage: usage}), nil
}

func sub2APIUsagePath(days int, timezone string) string {
	if days < 1 {
		days = sub2APIUsageDays
	}
	if strings.TrimSpace(timezone) == "" {
		timezone = sub2APITimezone
	}
	query := url.Values{"days": {strconv.Itoa(days)}, "timezone": {timezone}}
	return sub2APIUsageEndpoint + "?" + query.Encode()
}

func mapSub2APISnapshot(providerID string, data sub2APICollected) BalanceSnapshot {
	observedAt := time.Now()
	if parsed, err := time.Parse(time.RFC3339Nano, data.Billing.ObservedAt); err == nil {
		observedAt = parsed
	}
	status := strings.TrimSpace(data.Usage.Mode)
	if !data.Usage.IsValid {
		status = "invalid"
	}
	if status == "" {
		status = "active"
	}
	unit := data.Usage.Unit
	if unit == "" {
		unit = "USD"
	}
	usage := data.Usage.Usage
	snapshot := BalanceSnapshot{
		ProviderID: providerID, ObservedAt: observedAt, Success: true, Unit: unit, Status: status,
		Usage: UsageSummary{
			Balance: float64Ptr(data.Usage.Balance), UsedUsage: float64Ptr(usage.Total.ActualCost), TotalRequests: float64Ptr(usage.Total.Requests),
			TotalInputTokens: float64Ptr(usage.Total.InputTokens), TotalOutputTokens: float64Ptr(usage.Total.OutputTokens), TotalCacheCreation: float64Ptr(usage.Total.CacheCreationTokens), TotalCacheRead: float64Ptr(usage.Total.CacheReadTokens), TotalTokens: float64Ptr(usage.Total.TotalTokens), TotalCost: float64Ptr(usage.Total.Cost),
			TodayRequests: float64Ptr(usage.Today.Requests), TodayInputTokens: float64Ptr(usage.Today.InputTokens), TodayOutputTokens: float64Ptr(usage.Today.OutputTokens), TodayCacheCreation: float64Ptr(usage.Today.CacheCreationTokens), TodayCacheRead: float64Ptr(usage.Today.CacheReadTokens), TodayTokens: float64Ptr(usage.Today.TotalTokens), TodayCost: float64Ptr(usage.Today.Cost), TodayActualCost: float64Ptr(usage.Today.ActualCost),
		},
		Billing: BillingInfo{KeyStatus: keyStatus(data.Usage.IsValid), GroupName: data.Usage.PlanName, GroupPlatform: data.Billing.BillingScope, RateMultiplier: float64Ptr(data.Billing.EffectiveRateMultiplier)},
	}
	return snapshot
}

func keyStatus(valid bool) string {
	if valid {
		return "active"
	}
	return "invalid"
}
