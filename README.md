# CPA Balance Pilot

CPA Balance Pilot 是一个原生 [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) 插件，用于读取 CPA 管理 API 中配置的静态 AI 供应商，并跟踪被选供应商的余额。插件不会修改 CPA 本身。

## 核心功能

- 支持多种 AI 供应商的余额、配额、用量及模型统计。
- 支持单个或批量刷新，可配置并发数、超时时间和自动刷新间隔。
- 支持全局 HTTP、HTTPS、SOCKS5 和 SOCKS5H 代理。
- 使用 SQLite 保存配置、凭据、查询结果和历史记录，API Key 与认证信息采用 AES-256-GCM 加密。
- 支持简体中文、繁体中文、英文和俄文，并适配 CPA 管理中心的明暗主题。

## 安装与使用

下载适用于 CPA 主机平台的插件，并将动态库放入 CPA 的插件目录，保持文件名不变：

```text
plugins/windows/amd64/cpa-balance-pilot.dll
plugins/linux/amd64/cpa-balance-pilot.so
plugins/darwin/arm64/cpa-balance-pilot.dylib
```

在 CPA 中启用插件后，从管理中心的插件菜单打开 `CPA Balance Pilot`，插件会自动复用 CPA 管理中心当前会话（无需再次手动输入 Management Key）。若页面被单独打开或 CPA 版本未提供宿主会话，再使用页面中的 Management Key 登录并配置供应商。余额信息显示在插件自己的管理页面中。

## License

[MIT](./LICENSE)
