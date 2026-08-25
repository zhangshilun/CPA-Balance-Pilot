# CPA Balance Pilot

CPA Balance Pilot 是 CLIProxyAPI 原生插件，用于读取多个上游供应商的余额、额度、用量和计费信息。项目的实现标准以 Sub2API 的字段语义和现有管理页面为准，同时支持 new-api 以及后续供应商扩展。

## 总体架构

所有供应商必须遵循同一条数据流：

```text
供应商配置
  → 多态实现
  → 供应商专属请求
  → 供应商响应结构体
  → 供应商 collected 数据
  → 供应商映射函数
  → BalanceSnapshot 公共模型
  → store 公共存储层
  → state/history 接口
  → Web 页面
```

禁止让 Web 页面直接读取或解析供应商原始响应。页面只读取 `/state` 和 `/history` 返回的已落库公共模型；刷新完成后页面重新读取状态和历史，确保当前值、详情和统计来自同一份持久化数据。

## 供应商多态

供应商扩展边界是 `providerSource`：

- `SourceType() string` 返回稳定的供应商类型标识。
- `Validate(ProviderInput) error` 只负责该供应商的凭据和配置校验。
- `Probe` / `ProbeWithProgress` 只负责该供应商的采集、解析和映射。
- 供应商不得直接访问 SQLite，不得直接拼装管理页面响应。

内置供应商：

- `sub2api`
- `new-api`

供应商注册由 `polymorphicProber`/provider registry 统一分发。新增供应商只应增加自己的实现和测试，不修改调度、存储或页面的供应商分支。

## 请求地址和响应结构

每个供应商的请求地址必须集中声明，禁止在探测流程中散落字符串字面量。

当前标准地址：

### Sub2API

```text
日使用情况，逐日请求
GET /api/v1/usage/stats?start_date=2026-08-24&end_date=2026-08-24&timezone=Asia%2FShanghai  
{
    "code": 0,
    "message": "success",
    "data": {
        "total_requests": 12867,
        "total_input_tokens": 128073053,
        "total_output_tokens": 13876003,
        "total_cache_creation_tokens": 13593399,
        "total_cache_read_tokens": 1514287938,
        "total_tokens": 1669830393,
        "total_cost": 1336.901395515,
        "total_actual_cost": 82.3783033322,
        "today_requests": 424,
        "today_input_tokens": 3822397,
        "today_output_tokens": 262791,
        "today_cache_creation_tokens": 0,
        "today_cache_read_tokens": 38000128,
        "today_tokens": 42085316,
        "today_cost": 21.9957292,
        "today_actual_cost": 3.48908208,
        "by_platform": [
            {
                "platform": "openai",
                "total_requests": 12587,
                "total_tokens": 1669145831,
                "total_actual_cost": 81.8895091322,
                "today_requests": 423,
                "today_tokens": 42085316,
                "today_actual_cost": 3.48908208
            },
            {
                "platform": "anthropic",
                "total_requests": 29,
                "total_tokens": 684562,
                "total_actual_cost": 0.4887942,
                "today_requests": 0,
                "today_tokens": 0,
                "today_actual_cost": 0
            }
        ]
    }
}

余额接口
/api/v1/auth/me?timezone=Asia%2FShanghai
{
    "code": 0,
    "message": "success",
    "data": {
        "id": 224,
        "balance": 3.098805, 余额
        "concurrency": 10,
        "status": "active", 状态
        "total_recharged": 84.5, 累计充值
    }
}

key和对应倍率，使用CPA返回key进行匹配
GET /api/v1/keys?page=1&page_size=20&sort_by=created_at&sort_order=desc&timezone=Asia%2FShanghai
{
    "code": 0,
    "message": "success",
    "data": {
        "items": [
            {
                "id": 274,
                "key": "sk-1ac32780395b771b025ceb2fdaf27c98087c4555d3d42c97c46d2398950ea357", apikey
                "name": "codex", 
                "status": "active", 启用状态
                "group": {
                    "name": "GPT-PLUS-TEAM", 分组
                    "platform": "openai", 平台
                    "rate_multiplier": 0.1, 倍率
                    "status": "active", 状态
                }
            }
        ],
        "total": 1,
        "page": 1,
        "page_size": 20,
        "pages": 1
    }
}
```


### new-api

```text
GET /api/status
{
    "data": {
        "quota_display_type": "USD", 金额单位
        "quota_per_unit": 500000, 金额换算比例
       
    },
    "message": "",
    "success": true
}

当前余额和累积使用金额、累计请求数
GET /api/user/self
{
    "data": {
        "quota": 1205283, 余额（要/quota_per_unit）
        "request_count": 642, 请求总数
        "used_quota": 1294717, 使用金额（要/quota_per_unit）
       
    },
    "message": "",
    "success": true
}


使用CPA返回key进行匹配，“-”截取后前4 后4位进行匹配获取分组和倍率
GET /api/token/?p=1&size=10
{
    "data": {
        "page": 1,
        "page_size": 10,
        "total": 1,
        "items": [
            {
                "key": "Ag0z**********IE2p", apikey
                "status": 1, 状态
                "group": "GPT K12", 分组名称
            }
        ]
    },
    "message": "",
    "success": true
}

分组列表
GET /api/user/self/groups
{
    "data": {
        "GPT K12": {
            "desc": "限制并发10，适合个人使用简单任务。复杂任务请用GPT Pro Max分组。", 组名称
            "ratio": 0.04 组倍率
        },
        "GPT Pro Max": {
            "desc": "Claude Max满血-Claude code专用 ，响应快速、稳定性高，适合复杂任务及高并发场景。",
            "ratio": 0.15
        }
    },
    "message": "",
    "success": true
}

日使用情况
GET /api/data/self?start_timestamp=1787500800&end_timestamp=1787587199&default_time=day
{
  "data": [
    {
      "model_name": "gpt-5.6-sol", 模型名称
      "token_used": 26230181, 使用token
      "count": 284, 请求数
      "quota": 869062 使用金额（要/quota_per_unit）
    }
  ],
  "message": "",
  "success": true
}
```


## 公共字段和映射


## 落库字段

SQLite 的新设计固定为五张表：`settings`（运行与查询配置）、`providers`（供应商配置与加密凭据）、`refresh_batches`（刷新批次）、`balance_history`（余额快照历史）和 `daily_usage_history`（每日使用记录）。不为旧版本保留兼容迁移；新数据库按当前结构初始化。供应商无论来自 Sub2API、new-api 或后续扩展，均只能通过公共模型写入公共存储层。

### `settings`：运行与查询配置

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` | integer | 固定为 `1` 的单例设置记录 |
| `global_proxy_cipher` | blob | 全局代理地址的 AES-GCM 密文 |
| `concurrency` | integer | 刷新并发数 |
| `timeout_seconds` | integer | 单次请求超时秒数 |
| `retry_count` | integer | 可重试请求的最大重试次数 |
| `auto_refresh` | integer/bool | 是否启用自动刷新 |
| `refresh_interval_minutes` | integer | 自动刷新周期 |
| `usage_days` | integer | 每日用量查询天数 |
| `usage_start_date` / `usage_end_date` | text | 模型统计查询日期范围 |
| `usage_timezone` | text | 用量查询时区 |
| `cleanup_auto_enabled` / `cleanup_hour` / `cleanup_days` / `cleanup_timezone` | scalar | 自动清理历史记录的规则 |
| `cleanup_provider_ids` | text(JSON array) | 自动清理的供应商 ID 列表 |
| `updated_at` | RFC3339 text | 最后更新时间 |

### `providers`：供应商配置与加密凭据

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` | text(UUID) | 公共供应商 ID；`balance_history.provider_id` 的外键 |
| `source_type` | text | 供应商类型，如 `sub2api`、`new-api` |
| `source_ref` | text | 来自 CPA 配置的稳定来源引用；与 `source_type` 组成唯一键 |
| `name` | text | 用户可见的供应商名称 |
| `base_url` | text | 上游基础地址，不含凭据 |
| `api_key_cipher` | blob | API Key 的 AES-GCM 密文，禁止明文落库 |
| `api_key_fingerprint` | text | 用于变更识别的加密指纹，不能还原 API Key |
| `new_api_user_cipher` | blob/null | new-api 用户标识的 AES-GCM 密文 |
| `new_api_authorization_cipher` | blob/null | new-api Authorization 的 AES-GCM 密文 |
| `created_at` / `updated_at` | RFC3339 text | 配置创建、最后修改时间 |

备注：任何新增供应商凭据都必须使用独立 cipher 字段或受控加密扩展保存，不能写入 `details_json`、日志或页面响应。`providers` 删除时通过外键级联删除其历史快照。


### `balance_history`：累计数据

列表最新值、详情页、历史页和统计均从该表读取，不得由页面重新计算上游响应。

| 字段 | 来源 | 说明 |
| --- | --- | --- |
| `id` | 数据库自增 | 历史记录 ID、分页游标 |
| `provider_id` | `BalanceSnapshot.ProviderID` | 所属供应商 |
| `observed_at` | `BalanceSnapshot.ObservedAt` | 本次观测时间 |
| `success` | `BalanceSnapshot.Success` | 探测是否成功；失败也必须落库 |
| `unit` | `BalanceSnapshot.Unit` | 已换算金额的展示单位 |
| `status` | `BalanceSnapshot.Status` | 状态标识 |
| `usage_json` | `BalanceSnapshot.Usage` | 统一用量摘要 JSON，例如累计请求、Token、余额金额 |
| `group_json` | `BalanceSnapshot.Billing` | 组名称/组倍率信息 JSON |
| `error` | `BalanceSnapshot.Error` | 主探测失败的安全错误信息，禁止包含 API Key、Authorization 或代理密码 |


### `daily_usage_history`：每日使用记录

每日记录由供应商映射后的 `details.daily_usage` 规范化写入，作为统计和趋势查询的唯一数据源；`details_json` 中的数组只保留兼容展示和扩展字段。

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` | integer | 自增主键 |
| `batch_id` / `provider_id` | text | 所属刷新批次和供应商，均为外键 |
| `usage_date` | text | 统一日期 `YYYY-MM-DD` |
| `model_name` / `username` | text | 模型和用户；缺失为空字符串 |
| `requests` | real/null | 请求次数；优先 `requests`，回退 `count` |
| `input_tokens` / `output_tokens` | real/null | 输入、输出 Token |
| `cache_creation_tokens` / `cache_read_tokens` / `cache_write_tokens` | real/null | 缓存 Token 指标 |
| `total_tokens` | real/null | 总 Token；缺失时回退 `token_used` |
| `used_usage` | real/null | 实际金额


## 公共存储层

`store.go` 是公共能力，负责：

- 供应商配置和凭据的加密持久化。
- 刷新批次和 `BalanceSnapshot` 历史记录持久化。
- 最新快照读取、历史分页和清理。
- 五张公共表的初始化、事务写入、查询和级联清理。

供应商实现不得新增供应商专属数据库表或绕过 `saveSnapshot`。每日记录与余额快照必须在同一事务中写入；不能把供应商原始 JSON 当作公共统计字段使用。

## Web 页面

页面只负责展示公共模型和发起管理 API 请求：

- 供应商配置来自 `/state.providers`。
- 当前余额、详情和计费来自 `provider.latest`。
- 历史记录来自 `/history`。
- 刷新后重新读取 `/state`，必要时再读取 `/history`。
- 页面统一使用 Sub2API 字段标签和格式化逻辑。
- 缺失值显示为 `-`；不得显示 `- USD`、`NaN` 或由占位值计算出的金额。

供应商特有字段只能作为公共详情中的扩展信息展示，不能让页面根据 `source_type` 分叉出另一套字段协议。

## 新实现方案

按以下顺序实施重构：

1. **建立请求目录**：在每个供应商文件中声明 endpoint 常量、URL 构造函数、请求用途名称和响应结构体。
2. **拆分采集与映射**：供应商探测只产生专属 `CollectedData`；所有公共字段通过 `map<Provider>Snapshot` 生成。
3. **统一公共模型**：以 Sub2API 字段为基线，补充 new-api 缺失字段的占位策略和单位换算规则。
4. **收紧存储边界**：`store` 只接收公共模型，刷新调度层只负责并发、重试、批次和调用保存。
5. **统一页面数据源**：页面只读取落库后的 state/history，移除任何直接消费上游响应的路径。
6. **扩展供应商验证**：新增一个最小测试供应商，验证注册、独立采集、公共映射和公共存储无需修改。

## 验收标准

- Sub2API 和 new-api 都经过各自的响应结构体和映射函数。
- 所有请求地址集中声明，代码中不再散落 endpoint 字符串。
- `go test ./...` 通过。
- 新增供应商只需实现 `providerSource` 和自身映射测试。
- 页面展示、统计和历史读取均来自已落库 `BalanceSnapshot`。
- 任一供应商缺失字段不会阻塞落库，也不会产生错误的统计数值。

## 修改原则

- 优先复用现有插件协议、SQLite schema、加密和管理 API。
- 不在供应商实现中引入页面逻辑或存储逻辑。
- 新设计不承担旧数据库迁移；变更表结构时同步更新初始化 DDL、公共模型、查询和测试。
- 修改请求格式、公共字段或数据库 schema 时，必须同步增加回归测试。
