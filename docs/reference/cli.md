# 命令行参数

以下参数以当前程序实现为准。运行 `http-relay --help` 可以查看已安装版本的准确说明。

| 参数 | 默认值 | 说明 |
| --- | --- | --- |
| `--version` | `false` | 输出版本并退出 |
| `--config` | `HTTP_RELAY_CONFIG` | TOML 配置文件路径 |
| `--mode` | `regular` | `regular` 或 `reverse:<url>` |
| `--allow-private-targets` | `false` | 允许 regular 模式访问私网或本地目标 |
| `--listen` | 空 | 完整监听地址，覆盖 `--host` 和 `--port` |
| `--host` | `HOST` / `127.0.0.1` | Relay 监听主机 |
| `--port` | `PORT` / `7080` | Relay 监听端口 |
| `--timeout` | `600s` | 上游请求超时；`0` 表示不超时 |
| `-w`, `--dump` | `false` | 输出请求/响应流量 |
| `--dump-scope` | `WIRE_SCOPE` | `req`、`resp` 或 `req,resp` |
| `--mask-auth` | `false` | 转储时脱敏认证请求头 |
| `--color` | `auto` | `auto`、`always` 或 `never` |
| `--tui` | `false` | 启用交互式 TUI，并采集请求与响应 |
| `--web` | `false` | 启用实时 Web UI，并采集请求与响应 |
| `--web-listen` | `127.0.0.1:7090` | Web UI 监听地址 |
| `--web-trust-forwarded-headers` | `false` | 信任 Web 反向代理转发头 |
| `--script` | 空 | 默认 JavaScript 改写脚本 |
| `--script-timeout` | `200ms` | 每个 Hook 的执行超时 |
| `--script-reload` | `watch` | `watch`、`poll` 或 `off` |
| `--add-header` | 空 | 追加上游请求头，可重复 |
| `--modify-header` | 空 | 设置或覆盖上游请求头，可重复 |

## 环境变量

| 变量 | 说明 |
| --- | --- |
| `HOST` / `PORT` | Relay 默认监听地址 |
| `HTTP_RELAY_CONFIG` | 配置文件路径，优先级低于 `--config` |
| `WIRE_SCOPE` | 转储范围，优先级低于 `--dump-scope` |
| `WEB_AUTH_KEY` | Web UI 共享登录密钥 |
| `WEB_AUTH_JWT_SECRET` | 覆盖 TOML 中的 JWT Secret，不会单独启用 JWT 模式 |
| `WEB_MAX_TRANSACTIONS_PER_NAMESPACE` | 覆盖每个 namespace 的记录上限 |
| `ALL_PROXY` | 普通上游请求的统一代理 |
| `HTTP_PROXY` / `HTTPS_PROXY` | 按协议配置普通上游代理 |
| `NO_PROXY` | 指定直连目标 |
