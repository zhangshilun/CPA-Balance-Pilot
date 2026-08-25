//go:build cgo

package main

/*
#include <stdint.h>
#include <stdlib.h>
typedef struct { void* ptr; size_t len; } cliproxy_buffer;
typedef int (*cliproxy_host_call_fn)(void*, const char*, const uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_host_free_fn)(void*, size_t);
typedef struct { uint32_t abi_version; void* host_ctx; cliproxy_host_call_fn call; cliproxy_host_free_fn free_buffer; } cliproxy_host_api;
typedef int (*cliproxy_plugin_call_fn)(char*, uint8_t*, size_t, cliproxy_buffer*);
typedef void (*cliproxy_plugin_free_fn)(void*, size_t);
typedef void (*cliproxy_plugin_shutdown_fn)(void);
typedef struct { uint32_t abi_version; cliproxy_plugin_call_fn call; cliproxy_plugin_free_fn free_buffer; cliproxy_plugin_shutdown_fn shutdown; } cliproxy_plugin_api;
extern int cliproxyPluginCall(char*, uint8_t*, size_t, cliproxy_buffer*);
extern void cliproxyPluginFree(void*, size_t);
extern void cliproxyPluginShutdown(void);
static const cliproxy_host_api* stored_host;
static void store_host_api(const cliproxy_host_api* host) { stored_host = host; }
static void clear_host_api(void) { stored_host = NULL; }
static int call_host_api(const char* method, const uint8_t* request, size_t length, cliproxy_buffer* response) { if (stored_host == NULL || stored_host->call == NULL) return 1; return stored_host->call(stored_host->host_ctx, method, request, length, response); }
static void free_host_buffer(void* pointer, size_t length) { if (stored_host != NULL && stored_host->free_buffer != NULL && pointer != NULL) stored_host->free_buffer(pointer, length); }
*/
import "C"

import (
	"encoding/json"
	"fmt"
	"unsafe"
)

//export cliproxy_plugin_init
func cliproxy_plugin_init(host *C.cliproxy_host_api, plugin *C.cliproxy_plugin_api) C.int {
	if plugin == nil {
		return 1
	}
	C.store_host_api(host)
	plugin.abi_version = 1
	plugin.call = C.cliproxy_plugin_call_fn(C.cliproxyPluginCall)
	plugin.free_buffer = C.cliproxy_plugin_free_fn(C.cliproxyPluginFree)
	plugin.shutdown = C.cliproxy_plugin_shutdown_fn(C.cliproxyPluginShutdown)
	return 0
}

//export cliproxyPluginCall
func cliproxyPluginCall(method *C.char, request *C.uint8_t, n C.size_t, response *C.cliproxy_buffer) C.int {
	if response != nil {
		response.ptr, response.len = nil, 0
	}
	if method == nil {
		writeResponse(response, Error("invalid_method", "missing CPA plugin method"))
		return 1
	}
	var raw []byte
	if request != nil && n > 0 {
		raw = C.GoBytes(unsafe.Pointer(request), C.int(n))
	}
	result, err := handleMethod(C.GoString(method), raw, callHost)
	if err != nil {
		writeResponse(response, Error("plugin_error", safeError(err)))
		return 1
	}
	if !writeResponse(response, result) {
		return 1
	}
	return 0
}

//export cliproxyPluginFree
func cliproxyPluginFree(pointer unsafe.Pointer, _ C.size_t) {
	if pointer != nil {
		C.free(pointer)
	}
}

//export cliproxyPluginShutdown
func cliproxyPluginShutdown() { C.clear_host_api(); closeRuntimeStore() }

func callHost(method string, payload any) (json.RawMessage, error) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal host callback: %w", err)
	}
	cMethod := C.CString(method)
	defer C.free(unsafe.Pointer(cMethod))
	var request *C.uint8_t
	if len(raw) > 0 {
		request = (*C.uint8_t)(C.CBytes(raw))
		defer C.free(unsafe.Pointer(request))
	}
	var response C.cliproxy_buffer
	if code := C.call_host_api(cMethod, request, C.size_t(len(raw)), &response); code != 0 {
		return nil, fmt.Errorf("host callback %s returned code %d", method, int(code))
	}
	defer C.free_host_buffer(response.ptr, response.len)
	if response.ptr == nil || response.len == 0 {
		return nil, fmt.Errorf("host callback %s returned no response", method)
	}
	return DecodeResult(C.GoBytes(response.ptr, C.int(response.len)))
}

func writeResponse(response *C.cliproxy_buffer, raw []byte) bool {
	if response == nil || len(raw) == 0 {
		return false
	}
	p := C.CBytes(raw)
	if p == nil {
		return false
	}
	response.ptr, response.len = p, C.size_t(len(raw))
	return true
}
