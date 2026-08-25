package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// HostCall 是 CLIProxyAPI 提供给原生插件的回调函数。
type HostCall func(method string, payload any) (json.RawMessage, error)

// Envelope 是 CPA RPC 响应信封。
type Envelope struct {
	OK     bool            `json:"ok"`
	Result json.RawMessage `json:"result,omitempty"`
	Error  *RPCError       `json:"error,omitempty"`
}

type RPCError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func OK(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return json.Marshal(Envelope{OK: true, Result: raw})
}

func Error(code, message string) []byte {
	raw, _ := json.Marshal(Envelope{OK: false, Error: &RPCError{Code: code, Message: message}})
	return raw
}

func DecodeResult(raw []byte) (json.RawMessage, error) {
	var envelope Envelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return nil, fmt.Errorf("decode host response: %w", err)
	}
	if envelope.OK {
		return append(json.RawMessage(nil), envelope.Result...), nil
	}
	if envelope.Error != nil {
		return nil, fmt.Errorf("%s: %s", envelope.Error.Code, envelope.Error.Message)
	}
	return nil, fmt.Errorf("host callback failed")
}

type LifecycleRequest struct {
	SchemaVersion uint32 `json:"schema_version"`
	ConfigYAML    []byte `json:"config_yaml"`
}

type Metadata struct {
	Name             string        `json:"Name"`
	Version          string        `json:"Version"`
	Author           string        `json:"Author"`
	GitHubRepository string        `json:"GitHubRepository"`
	Logo             string        `json:"Logo"`
	ConfigFields     []ConfigField `json:"ConfigFields"`
}

type ConfigField struct {
	Name        string   `json:"Name"`
	Type        string   `json:"Type"`
	EnumValues  []string `json:"EnumValues"`
	Description string   `json:"Description"`
}

type Registration struct {
	SchemaVersion uint32       `json:"schema_version"`
	Metadata      Metadata     `json:"metadata"`
	Capabilities  Capabilities `json:"capabilities"`
}

type Capabilities struct {
	ManagementAPI bool `json:"management_api"`
}

type ManagementRegistration struct {
	Routes    []ManagementRoute `json:"routes,omitempty"`
	Resources []ResourceRoute   `json:"resources,omitempty"`
}

type ManagementRoute struct {
	Method, Path, Description string
}

type ResourceRoute struct {
	Path, Menu, Description string
}

type ManagementResponse struct {
	StatusCode int         `json:"StatusCode"`
	Headers    http.Header `json:"Headers,omitempty"`
	Body       []byte      `json:"Body,omitempty"`
}

type managementRequest struct {
	Method  string      `json:"Method"`
	Path    string      `json:"Path"`
	Headers http.Header `json:"Headers"`
	Body    []byte      `json:"Body"`
}

// HostAuthListResponse 和 HostAuthGetResponse 描述 CPA 原生插件回调，
// 用于读取已配置凭据，同时避免向页面暴露敏感信息。
type HostAuthListResponse struct {
	Files []HostAuthFile `json:"files"`
}
type HostAuthFile struct {
	ID            string `json:"id,omitempty"`
	AuthIndex     string `json:"auth_index,omitempty"`
	Name          string `json:"name,omitempty"`
	Email         string `json:"email,omitempty"`
	Provider      string `json:"provider,omitempty"`
	Status        string `json:"status,omitempty"`
	StatusMessage string `json:"status_message,omitempty"`
	Disabled      bool   `json:"disabled,omitempty"`
	Unavailable   bool   `json:"unavailable,omitempty"`
}
type HostAuthGetRequest struct {
	AuthIndex string `json:"auth_index"`
}
type HostAuthGetResponse struct {
	AuthIndex string          `json:"auth_index,omitempty"`
	Name      string          `json:"name,omitempty"`
	Email     string          `json:"email,omitempty"`
	PlanType  string          `json:"plan_type,omitempty"`
	Disabled  bool            `json:"disabled,omitempty"`
	JSON      json.RawMessage `json:"json,omitempty"`
}

func JSON(status int, body any) ManagementResponse {
	raw, err := json.Marshal(body)
	if err != nil {
		status, raw = http.StatusInternalServerError, []byte(`{"error":"response_encoding_failed"}`)
	}
	return ManagementResponse{StatusCode: status, Headers: http.Header{"Content-Type": {"application/json; charset=utf-8"}}, Body: raw}
}

func HTML(status int, body []byte) ManagementResponse {
	return ManagementResponse{StatusCode: status, Headers: http.Header{"Content-Type": {"text/html; charset=utf-8"}}, Body: body}
}

func rfc3339(t time.Time) string { return t.UTC().Format(time.RFC3339Nano) }
