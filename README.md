# CPA Balance Pilot

CPA Balance Pilot 是一个原生 [CLIProxyAPI](https://github.com/router-for-me/CLIProxyAPI) 插件，用于读取 CPA 管理 API 中配置的静态 AI 供应商，并跟踪被选供应商的余额。插件不会修改 CPA 本身。

![alt text](image.png)

## 核心功能

- 支持 sub2api/new-api 中转余额、当日用量同步及展示。
- 使用 SQLite 保存配置、凭据、查询结果，API Key 与认证信息采用 AES-256-GCM 加密。

## 安装与使用

下载适用于 CPA 主机平台的插件，并将动态库放入 CPA 的插件目录，保持文件名不变：

```text
plugins/windows/amd64/cpa-balance-pilot.dll
plugins/linux/amd64/cpa-balance-pilot.so
plugins/darwin/arm64/cpa-balance-pilot.dylib
```

### 配置

`CPA_BALANCE_PILOT_KEY` 必填；提供的 32 字节主密钥加密保存。该值必须是标准 Base64 编码。

`CPA_BALANCE_PILOT_DIR` 可选，每个账号单独保存为 JSON 文件，默认值为 `/CLIProxyAPI/data/cpa-balance-pilot/`。

生成密钥示例：

将命令输出的密钥填写到 CLIProxyAPI 的插件配置页面。配置完成后请一直使用同一个密钥，否则已有账号密码无法解密。


```bash
openssl rand -base64 32
```
## License

[MIT](./LICENSE)
