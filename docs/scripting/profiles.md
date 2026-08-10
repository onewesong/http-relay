# Rewrite Profile

Profile 允许客户端通过路径选择不同改写逻辑，适合在同一个 Relay 中承载多个 API 兼容层或 Mock。

## 配置 Profile

```toml
[rewrite.profiles.openai]
script = "builtin:rewrite.openai.js"
timeout = "500ms"
reload = "off"

[rewrite.profiles.mock]
script = "./plugins/examples/rewrite.mock.js"
timeout = "300ms"
reload = "watch"
```

磁盘脚本的相对路径以 TOML 配置文件所在目录为基准。

## 选择 Profile

```bash
curl "http://127.0.0.1:7080/@openai/https://api.example.com/v1/responses"
curl "http://127.0.0.1:7080/team-a/@mock/https://example.com/healthz"
```

规则如下：

- 未指定 `@profile` 时使用 `--script` 配置的默认脚本。
- 指定 Profile 后只执行该 Profile，不与默认脚本组合。
- 未知 Profile 返回 `404`，不会回退到默认脚本。
- Profile 仅在 `regular` 模式解析。
- `builtin:<文件名>` 引用编译进二进制的内置脚本，不支持热更新。

Profile 名称必须匹配：

```text
^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$
```
