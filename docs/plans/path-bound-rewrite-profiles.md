# 路径绑定 Rewrite Profile 实施计划

## 背景与目标

当前 `http-relay` 通过单个 `--script` 加载一份 JavaScript，并对所有 Relay 请求执行同一套 `onRequest` / `onResponse` 改写逻辑。

本方案引入具名 Rewrite Profile，使调用方可以直接通过 Relay 请求路径选择不同脚本，同时满足以下目标：

- 保持现有无 namespace 和带 namespace 请求路径完全兼容。
- namespace 继续只负责流量分组和 Web 权限隔离，Rewrite Profile 只负责选择改写逻辑。
- 未选择 Profile 的请求继续使用现有 `--script` 行为。
- URL 中只能引用配置好的 Profile，不能引用任意本地文件。
- 没有脚本的请求继续走现有流式转发路径。
- 第一版只在 `regular` 模式启用 Profile 路径，`reverse` 模式保持原样。

## 已确认的路径格式

使用以 `@` 开头的单段 Profile 标记。`@` 不符合现有 namespace 命名规则，因此不会与 namespace 产生歧义。

```text
/{absolute-url}
/{namespace}/{absolute-url}
/@{profile}/{absolute-url}
/{namespace}/@{profile}/{absolute-url}
```

示例：

```text
/https://example.com
/team-a/https://example.com
/@openai/https://example.com
/team-a/@openai/https://example.com
```

对应关系：

| Relay 请求路径 | namespace | Rewrite Profile | 上游目标 |
|---|---|---|---|
| `/https://example.com` | 空 | 默认脚本 | `https://example.com` |
| `/team-a/https://example.com` | `team-a` | 默认脚本 | `https://example.com` |
| `/@openai/https://example.com` | 空 | `openai` | `https://example.com` |
| `/team-a/@openai/https://example.com` | `team-a` | `openai` | `https://example.com` |

`@{profile}` 只在 Relay 端口参与路由，不会发送给上游，也不会改变 Web 查看路径。带 namespace 的记录仍从以下地址查看：

```text
/namespace/team-a/
```

## 路径解析规则

### Regular 模式

解析顺序固定为：

1. 检查第一段是否为 `@{profile}`。
2. 如果第一段是合法 namespace，则检查第二段是否为 `@{profile}`。
3. 去掉 namespace 和 Profile 控制段后，剩余内容必须是 `http://` 或 `https://` 绝对 URL。
4. query string 继续属于上游目标 URL，行为与当前实现一致。

Profile 名称使用与 namespace 相同的字符和长度约束：

```text
^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$
```

路径安全规则：

- Profile 段必须使用字面量 `@`，不接受 `%40` 等非规范形式。
- 同时检查 `URL.Path`、`RawPath` 和 `RequestURI`，避免编码结果不一致。
- 拒绝空 Profile、编码斜杠、双重编码、控制字符、`.`、`..` 和多段 Profile。
- Profile 之后缺少绝对 URL 时返回 `400`。
- Profile 名称合法但未配置时返回 `404 rewrite profile not found`，不能回退到默认脚本。
- 不带 `@` 的全部现有路径保持原有解析结果和错误行为。

### Reverse 模式

第一版不解析 `@{profile}`，所有路径继续原样拼接到 reverse upstream，避免改变已有上游路径：

```text
--mode reverse:https://api.example.com
/@openai/v1/models -> https://api.example.com/@openai/v1/models
```

后续如果确实需要为 reverse 模式选择 Profile，再单独设计不与上游路径冲突的入口。

## TOML 配置设计

沿用现有严格 TOML 配置文件，在 `rewrite.profiles` 下配置具名脚本：

```toml
[rewrite.profiles.openai]
script = "./scripts/openai.js"

[rewrite.profiles.mock]
script = "./scripts/mock.js"
timeout = "500ms"

[rewrite.profiles.sanitize]
script = "./scripts/sanitize.js"
reload = "off"
```

配置规则：

- `script` 必填且不能为空。
- 相对脚本路径以 TOML 配置文件所在目录为基准，而不是进程当前工作目录。
- `timeout` 可选；未配置时继承现有 `--script-timeout`，并且必须大于零。
- `reload` 可选；未配置时继承现有 `--script-reload`，只允许 `watch`、`poll` 或 `off`。
- Profile 名称必须符合单段命名规则。
- TOML 未知字段继续严格拒绝。
- 配置文件中不能重复声明 Profile。
- Profile 数量第一版不设置人为上限，但启动日志必须显示实际加载数量。
- 没有配置 `[rewrite.profiles]` 时，程序行为与当前版本完全一致。

示例配置与现有 Web 配置可以共存：

```toml
[web]
max_transactions_per_namespace = 100

[web.auth]
mode = "jwt"
# ...

[rewrite.profiles.openai]
script = "./scripts/openai.js"
```

## 与现有 `--script` 的兼容规则

第一版每个请求最多选择一个 Engine，不组合执行多个脚本：

1. 请求路径包含 `@{profile}` 时，只执行该 Profile 对应脚本。
2. 请求路径不包含 Profile 时，执行现有 `--script` 指定的默认脚本。
3. 两者都不存在时，不执行脚本。

现有参数继续保留：

```text
--script
--script-timeout
--script-reload
```

`--script-timeout` 和 `--script-reload` 同时作为具名 Profile 的默认值；Profile 自身配置可以覆盖。路径选择的 Profile 不与 `--script` 串联，避免以下未定义行为：

- 多个 `onRequest` 的执行顺序。
- 某个脚本短路后是否继续执行后续脚本。
- 多个 `onResponse` 应正序还是逆序执行。
- 前一个脚本改写 URL 后是否重新选择 Profile。

脚本即使改写了 `req.url`，本次请求仍使用最初由路径选中的 Profile，不能在执行过程中切换 Engine。

## JavaScript 上下文扩展

在现有 request 对象上增加只读元数据：

```js
function onRequest(req) {
  console.log(req.namespace);      // "team-a" 或 ""
  console.log(req.rewriteProfile); // "openai" 或 ""
  console.log(req.originalPath);   // 原始 Relay 请求路径
}
```

字段规则：

- `req.namespace`：解析后的 namespace，空字符串表示默认视图。
- `req.rewriteProfile`：本次选择的 Profile，使用默认脚本时为空字符串。
- `req.originalPath`：包含 namespace/Profile 控制段的原始路径，不包含 URL fragment。
- 三个字段通过不可写属性暴露；脚本修改它们不会影响路由和记录归属。
- `req.url` 仍表示已经移除控制段的上游绝对 URL，并继续允许脚本改写。

`onResponse(resp, req)` 接收到相同的 request 元数据。

## 内部结构设计

### Profile Registry

新增一个 Registry 管理默认 Engine 和具名 Engine：

```text
script.Registry
├── defaultEngine      现有 --script
├── openai Engine
├── mock Engine
└── sanitize Engine
```

Registry 需要提供：

```go
Default() *Engine
Lookup(profile string) (*Engine, bool)
Profiles() []ProfileInfo
WatchAll(...) (stop func(), err error)
```

每个 Profile 使用独立的：

- 编译结果和 generation。
- goja runtime pool。
- 执行超时。
- 热更新状态。

初次启动时任意 Profile 文件缺失、编译失败或 Hook 类型非法都应阻止启动。运行期间某个 Profile 热更新失败时，只保留该 Profile 上一次成功版本，不影响其他 Profile。

### Relay 路由结果

扩展现有 `ResolvedTarget`：

```go
type ResolvedTarget struct {
    URL            *url.URL
    Namespace      string
    RewriteProfile string
    OriginalPath   string
}
```

路径解析只负责提取字符串 Profile，不直接访问 Registry。Handler 在解析成功后查询 Registry，从而让解析单元测试不依赖脚本文件。

### Handler 执行流程

将当前先判断单个 Engine、再解析目标的流程调整为：

```text
解析 ResolvedTarget
→ 根据 RewriteProfile 选择 Engine
→ 未指定 Profile 时选择默认 Engine
→ 未找到具名 Profile时返回 404
→ Engine 无 Hook 或不存在时走 servePlain
→ Engine 有 Hook 时走 serveScripted
→ onRequest、上游请求和 onResponse 始终使用同一个 Engine
→ 使用原 namespace 和 Profile 记录结果
```

`servePlain` 和 `serveScripted` 应直接接收已经解析好的 `ResolvedTarget`，避免重复解析路径。

只有选中的 Engine 确实包含 Hook 时才缓冲请求/响应正文。仅仅配置其他 Profile 不得导致普通请求失去流式转发能力。

## 记录与 Web 可观测性

为交易记录增加可选字段：

```json
{
  "namespace": "team-a",
  "rewriteProfile": "openai"
}
```

建议同步更新：

- Access 日志显示 `rewrite=openai`，默认脚本不额外显示。
- Web Transactions API 返回 `rewriteProfile`。
- Requests 列表在目标 URL 前显示低饱和 Profile 徽章。
- 详情页显示 namespace 和 Rewrite Profile。
- Clear、SSE 和管理统计仍按 namespace 隔离，不按 Profile 再分组。
- Profile 不参与 Web JWT 授权；权限仍完全由 namespace 决定。

## 热更新与进程生命周期

- Registry 在启动阶段先编译全部 Engine，再启动任何监听端口，保证失败时不进入部分可用状态。
- 所有 Profile 编译成功后再分别启动 watcher。
- `watch` 继续监听父目录，兼容编辑器原子替换文件。
- 多个 Profile 指向同一文件时可以分别持有 Engine；第一版不做编译结果去重。
- 停止服务时统一关闭全部 watcher。
- 单个 watcher 报错或重载失败必须带 Profile 名写入 stderr。
- 启动摘要输出默认脚本状态、Profile 数量、名称、Hook 类型、reload 和 timeout，不输出脚本正文。

## 安全边界

- Profile URL 段只能引用 TOML 中的静态名称，禁止将 URL 内容拼接为文件路径。
- 不允许通过 query、Header 或 JavaScript 动态切换 Profile。
- Profile 路径不是认证机制；Relay 写入端口仍需通过网络或反向代理限制。
- Profile 名和日志字段必须拒绝控制字符，防止日志注入。
- Hook 超时、正文缓冲限制和错误处理沿用现有脚本安全策略。
- Profile 脚本拥有与现有 `--script` 相同的能力和信任级别。
- 未知 Profile 的响应不得泄露本地脚本路径或可用 Profile 完整列表。

## 错误行为

```text
400 invalid rewrite profile       Profile 格式非法
400 missing target URL            Profile 后缺少绝对 URL
404 rewrite profile not found     Profile 合法但未配置
500 script hook failed            选中脚本运行失败或超时
```

启动配置错误继续通过 stderr 输出并以非零状态退出。

## 实施待办

### 配置模型

- [x] 在共享 TOML 配置中新增 `rewrite.profiles` 模型。
- [x] 支持每个 Profile 的 `script`、`timeout` 和 `reload`。
- [x] 以配置文件目录为基准解析相对脚本路径。
- [x] 校验 Profile 名称、必填脚本路径、正数 timeout 和 reload 枚举。
- [x] 保持 TOML 未知字段严格拒绝。
- [x] 保证没有 `rewrite.profiles` 时配置行为完全不变。
- [x] 增加配置默认值、覆盖值和错误用例测试。

### 路径解析

- [x] 扩展 `ResolvedTarget`，增加 Rewrite Profile 和原始路径。
- [x] 支持 `/@{profile}/{absolute-url}`。
- [x] 支持 `/{namespace}/@{profile}/{absolute-url}`。
- [x] 保持 `/{absolute-url}` 和 `/{namespace}/{absolute-url}` 行为不变。
- [x] 第一版确保 reverse 模式完全不解析 Profile。
- [x] 拒绝空 Profile、非法字符、点段、编码 `@`、编码斜杠和双重编码。
- [x] Profile 后缺少合法绝对 URL 时返回明确错误。
- [x] 为路径解析增加表驱动测试和 fuzz 测试。

### Script Registry

- [x] 新增默认 Engine 与具名 Engine Registry。
- [x] 启动时原子地编译并验证全部 Profile。
- [x] 为每个 Profile 应用独立 timeout 和 reload 设置。
- [x] 实现全部 watcher 的统一启动和关闭。
- [x] 某个 Profile 热更新失败时保留其上一成功版本。
- [x] 日志包含 Profile 名但不暴露脚本正文。
- [x] 增加多 Profile、热更新隔离和并发 race 测试。

### Relay Handler 集成

- [x] Handler 在解析路径后再选择 Engine。
- [x] 未指定 Profile 时继续使用 `--script` 默认 Engine。
- [x] 指定 Profile 时只运行对应 Engine，不与默认 Engine 组合。
- [x] 未知 Profile 返回 404，不能回退到默认 Engine。
- [x] 同一请求的 `onRequest` 和 `onResponse` 固定使用同一 Engine。
- [x] Profile 改写 `req.url` 后不能触发二次 Profile 选择。
- [x] 无 Hook 的请求继续走流式 `servePlain`。
- [x] 避免 `servePlain` 和 `serveScripted` 重复解析目标路径。
- [x] 保持短路响应、静态 Header 规则和正文改写的现有顺序。
- [x] 增加错误、超时、短路和上游 URL 改写测试。

### JavaScript API

- [x] 向 request 对象增加只读 `namespace`。
- [x] 向 request 对象增加只读 `rewriteProfile`。
- [x] 向 request 对象增加只读 `originalPath`。
- [x] 确保 `onResponse` 获取相同的 request 上下文。
- [x] 验证脚本不能通过修改上下文字段改变路由归属。
- [x] 更新 JavaScript API 单元测试。

### 记录与 Web

- [x] 在 AccessRecord 和 Web Transaction 中记录 Rewrite Profile。
- [x] 在文本访问日志中显示非空 Profile。
- [x] 在 Transactions API 和 SSE 中输出 `rewriteProfile`。
- [x] 在 Requests 列表增加低饱和 Profile 徽章。
- [x] 在请求详情中显示 Profile 和 namespace。
- [x] 验证 store、Clear、SSE 和管理统计仍只按 namespace 隔离。
- [x] 验证 Web JWT 权限不受 Profile 影响。

### 兼容与安全测试

- [x] 验证现有无 namespace 路径完全不变。
- [x] 验证现有 namespace 路径完全不变。
- [x] 验证同一 namespace 可以选择不同 Profile 且仍进入同一 Web 视图。
- [x] 验证默认视图可以选择不同 Profile。
- [x] 验证 Profile 只能引用配置项，不能引用任意文件路径。
- [x] 验证未知 Profile 不执行默认脚本。
- [x] 验证 reverse 模式将 `@profile` 当作普通上游路径。
- [x] 验证普通无脚本请求继续流式转发。
- [x] 验证多 Profile 并发请求不会串用 Engine 或运行时状态。
- [x] 运行 `go test ./...`、脚本相关 race 测试和路径解析 fuzz 测试。

### 文档与发布

- [x] 更新中英文 README 的路径格式与 TOML 示例。
- [x] 更新 `config.example.toml`。
- [x] 增加多个 Profile 的 JavaScript 示例文件。
- [x] 说明 `--script` 是未指定 Profile 时的默认脚本。
- [x] 说明第一版 reverse 模式不启用 Profile 路由。
- [x] 说明 Profile 不提供 Relay 写入认证。
- [x] 在启动帮助和示例中加入 `@profile` 请求。

## 第一版暂不包含

- 多 Profile 脚本链和中间件式组合。
- 根据 target host、target path、HTTP method 或 Header 自动匹配 Profile。
- 根据 namespace 自动绑定默认 Profile。
- 通过 query 或 Header 选择 Profile。
- 在 Web 管理页创建、编辑或删除 Profile。
- Profile 级别的认证、限流、记录保留和统计隔离。
- Reverse 模式的 Profile 控制路径。
- 运行时新增或删除 Profile；配置仍需重启加载。

这些能力可以在单 Profile 路径选择稳定后独立设计，避免第一版同时引入路由匹配、脚本组合和动态配置三类复杂度。
