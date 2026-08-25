package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	_ "modernc.org/sqlite"
)

const schema = `
PRAGMA foreign_keys = ON;

-- 运行参数与自动刷新配置（单例）
CREATE TABLE IF NOT EXISTS settings (
 id INTEGER PRIMARY KEY CHECK (id = 1), -- 固定为 1
 concurrency INTEGER NOT NULL, -- 刷新并发数
 timeout_seconds INTEGER NOT NULL, -- 单次请求超时秒数
 retry_count INTEGER NOT NULL, -- 可重试请求次数
 auto_refresh INTEGER NOT NULL, -- 是否自动刷新
 refresh_interval_minutes INTEGER NOT NULL, -- 自动刷新间隔（分钟）
	updated_at TEXT NOT NULL -- 最后更新时间
);

-- 供应商配置与加密凭据
CREATE TABLE IF NOT EXISTS providers (
 id TEXT PRIMARY KEY, -- 公共供应商 UUID
 source_type TEXT NOT NULL, -- sub2api 或 new-api
 source_ref TEXT NOT NULL, -- CPA 凭证稳定引用
 name TEXT NOT NULL, -- 用户可见名称
 base_url TEXT NOT NULL, -- 上游基础地址
 api_key_cipher BLOB NOT NULL, -- API Key AES-GCM 密文
 api_key_fingerprint TEXT NOT NULL, -- API Key 不可逆指纹
 new_api_user_cipher BLOB, -- new-api 用户标识密文
 new_api_authorization_cipher BLOB, -- new-api Authorization 密文
 created_at TEXT NOT NULL, -- 创建时间
 updated_at TEXT NOT NULL, -- 修改时间
 latest_observed_at TEXT, -- 最近一次采集时间
 latest_success INTEGER, -- 最近一次采集是否成功
 latest_error TEXT, -- 最近一次采集错误
 today_requests REAL, -- 当日请求数
 today_tokens REAL, -- 当日总 Token
 today_usage REAL, -- 当日实际费用
 group_radio REAL, -- 当前 Key 组倍率
 balance REAL, -- 当前余额
 balance_unit TEXT, -- 余额单位
 UNIQUE(source_type, source_ref) -- 供应商来源唯一约束
);

`

type Store struct {
	db  *sql.DB
	key []byte
}

var runtimeStore struct {
	sync.RWMutex
	store *Store
	error string
}

func openStore(dataDir string, key []byte) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dataDir, "balance-pilot.sqlite"))
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("initialize schema: %w", err)
	}
	s := &Store{db: db, key: append([]byte(nil), key...)}
	if err := s.ensureSettings(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func closeRuntimeStore() {
	stopRefreshScheduler()
	runtimeStore.Lock()
	if runtimeStore.store != nil {
		_ = runtimeStore.store.db.Close()
	}
	runtimeStore.store = nil
	runtimeStore.Unlock()
}
func currentStore() (*Store, error) {
	runtimeStore.RLock()
	defer runtimeStore.RUnlock()
	if runtimeStore.store == nil {
		if runtimeStore.error != "" {
			return nil, fmt.Errorf("%s", runtimeStore.error)
		}
		return nil, fmt.Errorf("plugin is not configured")
	}
	return runtimeStore.store, nil
}

func (s *Store) ensureSettings(ctx context.Context) error {
	settings := defaultSettings()
	_, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO settings(id, concurrency, timeout_seconds, retry_count, auto_refresh, refresh_interval_minutes, updated_at) VALUES(1,?,?,?,?,?,?)`, settings.Concurrency, settings.TimeoutSeconds, settings.RetryCount, settings.AutoRefresh, settings.RefreshIntervalMinutes, rfc3339(time.Now()))
	if err != nil {
		return err
	}
	// 使用旧版 30 分钟或关闭自动刷新的数据库，首次重新打开时切换为 1 分钟默认值。
	_, err = s.db.ExecContext(ctx, `UPDATE settings SET auto_refresh=1, refresh_interval_minutes=1, updated_at=? WHERE id=1 AND auto_refresh=0 AND refresh_interval_minutes=30`, rfc3339(time.Now()))
	return err
}

func (s *Store) Settings(ctx context.Context) (Settings, error) {
	var v Settings
	var auto int
	err := s.db.QueryRowContext(ctx, `SELECT concurrency,timeout_seconds,retry_count,auto_refresh,refresh_interval_minutes FROM settings WHERE id=1`).Scan(&v.Concurrency, &v.TimeoutSeconds, &v.RetryCount, &auto, &v.RefreshIntervalMinutes)
	v.AutoRefresh = auto != 0
	return v, err
}

func (s *Store) SaveProvider(ctx context.Context, input ProviderInput) (Provider, error) {
	if err := validateProviderInput(input); err != nil {
		return Provider{}, err
	}
	apiCipher, err := encryptSecret(s.key, input.APIKey)
	if err != nil {
		return Provider{}, err
	}
	userCipher, err := encryptSecret(s.key, input.NewAPIUser)
	if err != nil {
		return Provider{}, err
	}
	authCipher, err := encryptSecret(s.key, input.NewAPIAuthorization)
	if err != nil {
		return Provider{}, err
	}
	now, id := rfc3339(time.Now()), strings.TrimSpace(input.ID)
	fingerprint := secretFingerprint(input.APIKey)
	if id == "" {
		id = uuid.NewString()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Provider{}, err
	}
	defer tx.Rollback()
	if input.ID != "" { // 敏感字段为空表示保留数据库中的原凭据。
		var oldAPI, oldUser, oldAuth []byte
		if err := tx.QueryRowContext(ctx, `SELECT api_key_cipher,new_api_user_cipher,new_api_authorization_cipher,api_key_fingerprint FROM providers WHERE id=?`, id).Scan(&oldAPI, &oldUser, &oldAuth, &fingerprint); err != nil {
			return Provider{}, err
		}
		if input.APIKey == "" {
			apiCipher = oldAPI
		}
		if input.NewAPIUser == "" {
			userCipher = oldUser
		}
		if input.NewAPIAuthorization == "" {
			authCipher = oldAuth
		}
	}
	baseURL := normalizeProviderBaseURL(input.BaseURL)
	if _, err = tx.ExecContext(ctx, `INSERT INTO providers(id,source_type,source_ref,name,base_url,api_key_cipher,api_key_fingerprint,new_api_user_cipher,new_api_authorization_cipher,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?) ON CONFLICT(id) DO UPDATE SET source_type=excluded.source_type,source_ref=excluded.source_ref,name=excluded.name,base_url=excluded.base_url,api_key_cipher=excluded.api_key_cipher,api_key_fingerprint=excluded.api_key_fingerprint,new_api_user_cipher=excluded.new_api_user_cipher,new_api_authorization_cipher=excluded.new_api_authorization_cipher,updated_at=excluded.updated_at`, id, input.SourceType, input.SourceRef, input.Name, baseURL, apiCipher, fingerprint, userCipher, authCipher, now, now); err != nil {
		return Provider{}, err
	}
	if err = tx.Commit(); err != nil {
		return Provider{}, err
	}
	return s.Provider(ctx, id)
}

func normalizeProviderBaseURL(raw string) string {
	return strings.TrimRight(strings.TrimSpace(raw), "/")
}

func (s *Store) Provider(ctx context.Context, id string) (Provider, error) {
	var p Provider
	var created, updated string
	err := s.db.QueryRowContext(ctx, `SELECT id,source_type,source_ref,name,base_url,api_key_fingerprint,created_at,updated_at FROM providers WHERE id=?`, id).Scan(&p.ID, &p.SourceType, &p.SourceRef, &p.Name, &p.BaseURL, &p.APIKeyFingerprint, &created, &updated)
	if err != nil {
		return p, err
	}
	p.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	p.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	p.Latest, _ = s.latest(ctx, p.ID)
	return p, nil
}

// ProviderBySource 根据供应商类型和来源引用查找对应的监控记录。
func (s *Store) ProviderBySource(ctx context.Context, sourceType, sourceRef string) (Provider, error) {
	var id string
	err := s.db.QueryRowContext(ctx, `SELECT id FROM providers WHERE source_type=? AND source_ref=?`, sourceType, sourceRef).Scan(&id)
	if err != nil {
		return Provider{}, err
	}
	return s.Provider(ctx, id)
}

func (s *Store) Providers(ctx context.Context) ([]Provider, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM providers ORDER BY name,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := []Provider{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		p, err := s.Provider(ctx, id)
		if err != nil {
			return nil, err
		}
		items = append(items, p)
	}
	return items, rows.Err()
}

func (s *Store) ProbeInput(ctx context.Context, id string) (ProviderInput, error) {
	var input ProviderInput
	var api, user, auth []byte
	err := s.db.QueryRowContext(ctx, `SELECT id,source_type,source_ref,name,base_url,api_key_cipher,new_api_user_cipher,new_api_authorization_cipher FROM providers WHERE id=?`, id).Scan(&input.ID, &input.SourceType, &input.SourceRef, &input.Name, &input.BaseURL, &api, &user, &auth)
	if err != nil {
		return input, err
	}
	if input.APIKey, err = decryptSecret(s.key, api); err != nil {
		return input, err
	}
	if input.NewAPIUser, err = decryptSecret(s.key, user); err != nil {
		return input, err
	}
	input.NewAPIAuthorization, err = decryptSecret(s.key, auth)
	return input, err
}

func (s *Store) DeleteProvider(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM providers WHERE id=?`, id)
	return err
}

func (s *Store) BeginBatch(ctx context.Context) (string, error) {
	return uuid.NewString(), nil
}
func (s *Store) FinishBatch(ctx context.Context, id string, success, failed int) error {
	return nil
}

func (s *Store) SaveSnapshot(ctx context.Context, snapshot BalanceSnapshot) error {
	if snapshot.ProviderID == "" {
		return fmt.Errorf("snapshot provider ID is required")
	}
	if snapshot.ObservedAt.IsZero() {
		snapshot.ObservedAt = time.Now()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if !snapshot.Success {
		_, err = tx.ExecContext(ctx, `UPDATE providers SET latest_observed_at=?,latest_success=?,latest_error=? WHERE id=?`, rfc3339(snapshot.ObservedAt), snapshot.Success, nullableString(snapshot.Error), snapshot.ProviderID)
	} else {
		_, err = tx.ExecContext(ctx, `UPDATE providers SET latest_observed_at=?,latest_success=?,latest_error=?,today_requests=?,today_tokens=?,today_usage=?,group_radio=?,balance=?,balance_unit=? WHERE id=?`, rfc3339(snapshot.ObservedAt), snapshot.Success, nullableString(snapshot.Error), snapshot.Usage.TodayRequests, snapshot.Usage.TodayTokens, snapshot.Usage.TodayActualCost, snapshot.Billing.RateMultiplier, snapshot.Usage.Balance, nullableString(snapshot.Unit), snapshot.ProviderID)
	}
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) latest(ctx context.Context, providerID string) (*BalanceSnapshot, error) {
	var snapshot BalanceSnapshot
	var observed, errText, unit string
	var success int
	var req, tokens, usage, ratio, balance sql.NullFloat64
	err := s.db.QueryRowContext(ctx, `SELECT id,latest_observed_at,COALESCE(latest_success,0),COALESCE(latest_error,''),today_requests,today_tokens,today_usage,group_radio,balance,COALESCE(balance_unit,'') FROM providers WHERE id=? AND latest_observed_at IS NOT NULL`, providerID).Scan(&snapshot.ProviderID, &observed, &success, &errText, &req, &tokens, &usage, &ratio, &balance, &unit)
	if err != nil {
		return nil, err
	}
	snapshot.ObservedAt, _ = time.Parse(time.RFC3339Nano, observed)
	snapshot.Success = success != 0
	snapshot.Error = errText
	snapshot.Unit = unit
	if req.Valid {
		snapshot.Usage.TodayRequests = &req.Float64
	}
	if tokens.Valid {
		snapshot.Usage.TodayTokens = &tokens.Float64
	}
	if usage.Valid {
		snapshot.Usage.TodayActualCost = &usage.Float64
	}
	if balance.Valid {
		snapshot.Usage.Balance = &balance.Float64
	}
	if ratio.Valid {
		snapshot.Billing.RateMultiplier = &ratio.Float64
	}
	return &snapshot, nil
}
func (s *Store) History(ctx context.Context, providerID string, limit int) ([]BalanceSnapshot, error) {
	latest, err := s.latest(ctx, providerID)
	if err != nil {
		return nil, err
	}
	return []BalanceSnapshot{*latest}, nil
}

type scanner interface{ Scan(...any) error }

func scanSnapshot(row scanner) (*BalanceSnapshot, error) {
	var v BalanceSnapshot
	var observed string
	var success int
	var usage, billing string
	err := row.Scan(&v.ID, &v.BatchID, &v.ProviderID, &observed, &success, &v.Unit, &v.Status, &usage, &billing, &v.Error)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	v.ObservedAt, _ = time.Parse(time.RFC3339Nano, observed)
	v.Success = success != 0
	_ = json.Unmarshal([]byte(usage), &v.Usage)
	_ = json.Unmarshal([]byte(billing), &v.Billing)
	return &v, nil
}
func nullableString(v string) any {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return v
}
