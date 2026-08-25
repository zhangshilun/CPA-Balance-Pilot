package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// providerSource 将供应商校验、采集和映射与存储及页面代码隔离。
type providerSource interface {
	SourceType() string
	Validate(ProviderInput) error
	Probe(context.Context, ProviderInput, Settings, *providerClient) (BalanceSnapshot, error)
}

type providerRegistry struct {
	mu     sync.RWMutex
	values map[string]providerSource
}

func newProviderRegistry(sources ...providerSource) *providerRegistry {
	r := &providerRegistry{values: map[string]providerSource{}}
	for _, s := range sources {
		r.values[s.SourceType()] = s
	}
	return r
}
func (r *providerRegistry) source(kind string) (providerSource, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s, ok := r.values[kind]
	return s, ok
}

var sources = newProviderRegistry(sub2APISource{}, newAPISource{})

func validateProviderInput(input ProviderInput) error {
	source, ok := sources.source(strings.TrimSpace(input.SourceType))
	if !ok {
		return fmt.Errorf("unsupported provider source_type %q", input.SourceType)
	}
	return source.Validate(input)
}

func validateCommonProvider(input ProviderInput) error {
	if strings.TrimSpace(input.SourceRef) == "" {
		return fmt.Errorf("source_ref is required")
	}
	if strings.TrimSpace(input.Name) == "" {
		return fmt.Errorf("name is required")
	}
	if strings.TrimSpace(input.APIKey) == "" && strings.TrimSpace(input.ID) == "" {
		return fmt.Errorf("api_key is required")
	}
	u, err := url.Parse(strings.TrimSpace(input.BaseURL))
	if err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("base_url must be an absolute HTTP URL")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("base_url must use HTTP or HTTPS")
	}
	return nil
}

type providerClient struct {
	client  *http.Client
	retries int
}

func newProviderClient(settings Settings) *providerClient {
	return &providerClient{client: &http.Client{Timeout: seconds(settings.TimeoutSeconds)}, retries: settings.RetryCount}
}
func seconds(value int) time.Duration {
	if value < 1 {
		value = 20
	}
	return time.Duration(value) * time.Second
}

func (c *providerClient) getJSON(ctx context.Context, input ProviderInput, path string, headers http.Header, target any) error {
	requestURL := strings.TrimRight(input.BaseURL, "/") + path
	var last error
	for attempt := 0; attempt <= c.retries; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
		if err != nil {
			return err
		}
		for key, values := range headers {
			for _, value := range values {
				req.Header.Add(key, value)
			}
		}
		resp, err := c.client.Do(req)
		if err != nil {
			last = err
			continue
		}
		func() {
			defer resp.Body.Close()
			if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				last = fmt.Errorf("upstream returned HTTP %d", resp.StatusCode)
				return
			}
			last = json.NewDecoder(io.LimitReader(resp.Body, maxRequestBytes)).Decode(target)
		}()
		if last == nil {
			return nil
		}
		if attempt < c.retries {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Duration(attempt+1) * 150 * time.Millisecond):
			}
		}
	}
	return last
}

// sub2APIHeaders 使用 CPA 返回的 API Key 认证 Sub2API 请求。
func sub2APIHeaders(cpaAPIKey string) http.Header {
	h := http.Header{}
	h.Set("Authorization", "Bearer "+cpaAPIKey)
	h.Set("Accept", "application/json")
	return h
}

func shanghai() *time.Location {
	zone, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		return time.FixedZone("CST", 8*3600)
	}
	return zone
}

func safeUpstreamMessage(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "upstream reported an error"
	}
	if len(value) > 200 {
		return value[:200]
	}
	return value
}
