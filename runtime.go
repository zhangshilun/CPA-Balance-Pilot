package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

type pluginConfig struct {
	Key           string `yaml:"CPA_BALANCE_PILOT_KEY"`
	DataDirectory string `yaml:"CPA_BALANCE_PILOT_DATA_DIR"`
}

var configState struct {
	sync.RWMutex
	configured bool
	message    string
}

var refreshScheduler struct {
	sync.Mutex
	cancel context.CancelFunc
}

func configureForLifecycle(raw []byte) {
	var lifecycle LifecycleRequest
	if len(strings.TrimSpace(string(raw))) > 0 {
		if err := json.Unmarshal(raw, &lifecycle); err != nil {
			setConfigurationError("插件配置格式无效：" + err.Error())
			return
		}
	}
	var config pluginConfig
	if len(lifecycle.ConfigYAML) > 0 {
		if err := yaml.Unmarshal(lifecycle.ConfigYAML, &config); err != nil {
			setConfigurationError("插件配置无效：" + err.Error())
			return
		}
	}
	key, err := decodeKey(config.Key)
	if err != nil {
		setConfigurationError(err.Error())
		return
	}
	directory := strings.TrimSpace(config.DataDirectory)
	if directory == "" {
		directory = defaultDataDirectory
	}
	directory, err = filepath.Abs(filepath.Clean(directory))
	if err != nil {
		setConfigurationError("解析数据目录：" + err.Error())
		return
	}
	if err := os.MkdirAll(directory, 0o700); err != nil {
		setConfigurationError("创建数据目录：" + err.Error())
		return
	}
	store, err := openStore(directory, key)
	if err != nil {
		setConfigurationError("初始化数据库：" + err.Error())
		return
	}
	closeRuntimeStore()
	runtimeStore.Lock()
	runtimeStore.store, runtimeStore.error = store, ""
	runtimeStore.Unlock()
	startRefreshScheduler()
	configState.Lock()
	configState.configured, configState.message = true, ""
	configState.Unlock()
}

func setConfigurationError(message string) {
	stopRefreshScheduler()
	closeRuntimeStore()
	runtimeStore.Lock()
	runtimeStore.error = strings.TrimSpace(message)
	runtimeStore.Unlock()
	configState.Lock()
	configState.configured, configState.message = false, strings.TrimSpace(message)
	configState.Unlock()
}

func startRefreshScheduler() {
	stopRefreshScheduler()
	store, err := currentStore()
	if err != nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	refreshScheduler.Lock()
	refreshScheduler.cancel = cancel
	refreshScheduler.Unlock()
	go runRefreshScheduler(ctx, store)
}

func stopRefreshScheduler() {
	refreshScheduler.Lock()
	if refreshScheduler.cancel != nil {
		refreshScheduler.cancel()
		refreshScheduler.cancel = nil
	}
	refreshScheduler.Unlock()
}

func runRefreshScheduler(ctx context.Context, store *Store) {
	for {
		settings, err := store.Settings(ctx)
		interval := time.Minute
		if err == nil && settings.RefreshIntervalMinutes > 0 {
			interval = time.Duration(settings.RefreshIntervalMinutes) * time.Minute
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if err != nil || !settings.AutoRefresh {
			continue
		}
		providers, err := store.Providers(ctx)
		if err != nil || len(providers) == 0 {
			continue
		}
		ids := make([]string, 0, len(providers))
		for _, provider := range providers {
			ids = append(ids, provider.ID)
		}
		_, _ = refreshProviders(ctx, store, ids)
	}
}
func configurationStatus() map[string]any {
	configState.RLock()
	defer configState.RUnlock()
	message := configState.message
	if !configState.configured && message == "" {
		message = "请在 CPA 插件配置中填写 CPA_BALANCE_PILOT_KEY。"
	}
	return map[string]any{"configured": configState.configured, "message": message}
}
func currentSettings() (Settings, error) {
	store, err := currentStore()
	if err != nil {
		return Settings{}, err
	}
	return store.Settings(context.Background())
}

var _ = fmt.Sprintf
