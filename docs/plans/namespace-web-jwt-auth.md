# Namespace Web JWT 认证实施计划

## 背景

当前 Web UI 仅支持通过 `WEB_AUTH_KEY` 配置一个全局访问密码。目标是在保持旧环境变量兼容的前提下，为默认视图和不同 namespace 分别配置访问权限。

新认证方式使用 HS256 JWT 作为用户输入的访问密钥，并提供独立的 `http-relay-auth` 命令生成 Secret、签发 Token 和检查 Token。JWT 仅保护 Web 页面、SSE、交易 API 和 Clear API，不保护 Relay 端口的请求写入。

## 已确认的设计

### 配置入口

主程序新增一个通用配置参数：

```bash
http-relay --config ./http-relay.toml --web
```

配置文件路径优先级：

1. `--config`
2. `HTTP_RELAY_CONFIG`
3. 未提供配置文件

JWT Secret 优先级：

1. `WEB_AUTH_JWT_SECRET`
2. TOML 中的 `web.auth.secret`

`WEB_AUTH_JWT_SECRET` 本身不会隐式启用 JWT；只有 TOML 明确设置 `web.auth.mode = "jwt"` 才启用 JWT 模式。TOML 存在但没有 `[web.auth]` 时，现有 `WEB_AUTH_KEY` 仍然生效。`http-relay-auth` 同样支持 `--config` 优先于 `HTTP_RELAY_CONFIG`。

认证模式选择：

- TOML 中配置 `web.auth.mode = "jwt"` 时，启用 namespace JWT 认证。
- 未启用 JWT、但存在 `WEB_AUTH_KEY` 时，继续使用现有的全局密码认证。
- 两者都不存在时，不启用 Web 认证。
- JWT 模式与 `WEB_AUTH_KEY` 同时启用时，启动失败，避免产生模糊的权限行为。
- `mode` 缺失表示未配置 JWT；空值或未知值启动失败。

### TOML 示例

```toml
[web.auth]
mode = "jwt"
secret = "replace-with-a-random-base64url-secret"

issuer = "http-relay"
audience = "http-relay-web"
token_ttl = "720h"
max_token_ttl = "2160h"

# 是否允许签发和接受不包含 exp 的永久 Token
allow_permanent_tokens = true

# 是否启用仅管理 Token 可访问的管理页面
admin_enabled = true

# 无 namespace 的根 Web 页面是否需要认证
default_protected = true

# 未在 namespaces 中明确配置的 namespace 是否需要认证
fallback_protected = false

# 仅在 Web 服务位于可信反向代理之后时开启
trust_forwarded_headers = false

[web.auth.namespaces]
team-a = true
team-b = true
public-demo = false
```

配置规则：

- `secret` 必须是 Base64URL 编码的至少 32 字节安全随机数；使用 `WEB_AUTH_JWT_SECRET` 时可以省略。
- `http-relay-auth secret` 生成 32 字节随机值并输出无 padding Base64URL；服务端和 CLI 必须严格解码后使用原始字节作为 HMAC key。
- 拒绝包含 padding、非规范 Base64URL 编码或解码后不足 32 字节的 Secret。
- `issuer` 默认使用 `http-relay`。
- `audience` 默认使用 `http-relay-web`。
- `token_ttl` 是签发工具的默认有效期。
- `max_token_ttl` 是管理页面和命令行允许签发的最大有效期。
- `token_ttl` 和 `max_token_ttl` 必须大于零，默认 TTL 不得超过最大 TTL，到期时间计算必须检查溢出。
- `allow_permanent_tokens` 显式控制是否允许缺少 `exp` 的永久 Token，默认关闭。
- `admin_enabled` 控制 `/admin/` 管理页面；管理 API 要求 namespace 为空的管理 JWT。
- `default_protected` 控制无 namespace 的根视图；为 `true` 时只能使用空 namespace 的管理 Token，为 `false` 时公开。
- `fallback_protected` 控制未明确配置的 namespace。
- `trust_forwarded_headers` 默认关闭；开启后才使用 `X-Forwarded-Proto` 和 `X-Forwarded-Host` 计算 Cookie Secure 属性及同源校验。
- `namespaces.<name>` 可以覆盖 fallback，`true` 表示需要认证，`false` 表示公开。
- namespace 必须符合现有的单段 namespace 命名规则。
- 配置中出现未知字段时启动失败，防止拼写错误导致认证策略失效。
- 配置内嵌 Secret 时，如果配置文件权限宽于 `0600`，启动时输出安全警告。

### JWT 格式

JWT Header：

```json
{
  "alg": "HS256",
  "typ": "JWT"
}
```

JWT Payload：

```json
{
  "iss": "http-relay",
  "aud": "http-relay-web",
  "namespace": "team-a",
  "iat": 1785900000,
  "nbf": 1785900000,
  "exp": 1788492000,
  "jti": "random-id"
}
```

验证规则：

- 只接受固定的 `HS256` 算法，不根据 JWT Header 动态选择算法。
- `iss` 和 `aud` 必须与配置一致。
- JWT 不携带用户身份、`sub` 或角色信息，权限只由经过签名的 `namespace` 决定。
- 非空 `namespace` 是受限 Token，只能访问完全匹配的 `/namespace/{namespace}/**`。
- 空字符串 `namespace = ""` 是管理 Token，可以访问 `/admin/**`、默认视图 `/` 和任意 `/namespace/{namespace}/**`。
- 管理 Token 访问具体 namespace 时绕过该 namespace 的 protected/public 策略；该策略只决定未持有管理 Token 的访问者是否需要对应受限 Token。
- 不支持 `namespace = "*"`；管理权限只用空 namespace 表示。
- 必须包含合法的 `iat` 和 `nbf`；有期限 Token 还必须包含合法的 `exp`。
- 缺少 `exp` 的 Token 仅在 `allow_permanent_tokens = true` 时作为永久 Token 接受。
- 时间验证允许最多 30 秒时钟误差。
- `namespace = "*"` 和其他非规范 namespace 均无效。

### Cookie 与统一登录行为

所有 Token 都通过统一的 `/login` 页面登录。用户粘贴 JWT 后，服务端验证签名和 claims，再完全根据 JWT 中的 namespace 设置 Cookie 和决定跳转目标，不接受用户提供的 `next` 决定目标位置。JWT 本身保存到 HttpOnly Cookie，不再创建第二层 Session。

受限 Token 的 Cookie 名称使用 namespace SHA-256 摘要的短前缀，管理 Token 使用固定且独立的 Cookie 名称：

```text
http_relay_web_session_<namespace-hash>
http_relay_web_admin_session
```

Cookie 属性：

```text
HttpOnly
SameSite=Lax
Secure=auto
Expires=<JWT exp>
```

Cookie Path 按认证域最小化：

```text
namespace Cookie     Path=/namespace/{namespace}/
管理 Cookie          Path=/
```

Cookie 名中的 namespace 摘要至少使用 SHA-256 的前 128 bit。删除 Cookie 时必须使用与创建时完全一致的名称和 Path。

Web namespace 统一收敛在 `/namespace/{namespace}/` 下。无 namespace 的默认视图继续使用根路径，不引入 `default` 之类的虚拟 namespace：

```text
/                                      默认视图
/login                                 所有 Token 的统一登录页
/logout                                删除管理 Cookie

/namespace/team-a/                     team-a 视图
/namespace/team-a/logout               仅退出 team-a
/namespace/team-a/events               team-a SSE
/namespace/team-a/api/transactions     team-a 交易 API
```

登录后的固定跳转规则：

```text
namespace = "team-a"    -> /namespace/team-a/
namespace = ""          -> /admin/
```

一个浏览器可以同时登录多个 namespace，也可以同时持有管理 Cookie。访问 namespace 时，合法的管理 Cookie可以代替对应 namespace Cookie。退出某个 namespace 时只删除该 namespace 对应的 Cookie；`/logout` 或 `/admin/logout` 删除管理 Cookie。

所有认证失败的前端统一跳转到 `/login`。登录接口不接受 `next` 控制跳转，防止跨 namespace 或开放重定向。静态资源、logout、SSE 和 API 地址仍根据当前路由基址生成。

SSE 在握手时验证 JWT 后，还必须记录 Token 的 `exp`，并在 `exp` 加允许的时钟误差到达时主动断开连接。该规则同时适用于 namespace SSE 和管理统计 SSE，避免长连接绕过 Token 到期。

永久 Token 没有 `exp`，对应 SSE 不设置基于 Token 到期时间的主动断开。JWT 可以永久有效，但浏览器 Cookie 仍可能受浏览器寿命上限、用户清理和策略限制；Cookie 丢失后可以使用原 JWT 再次登录。

Web 路由的收敛不改变 Relay 入口。以下请求格式继续保持不变：

```text
/team-a/https://example.com
/https://example.com
```

只有 Web 查看地址使用 `/namespace/team-a/`。`admin`、`login`、`events`、`app.js` 等名称均可作为合法 namespace，因为它们不再与根系统路由竞争。`/namespace/` 本身是 Web 系统保留前缀。

Web 路由迁移不保留旧的 `/{namespace}/` 兼容形式，也不提供自动重定向。旧路径按照普通系统路由处理，不再尝试推断 namespace。这样可以彻底消除根路由与 namespace 的解析歧义，避免兼容层长期保留同一类冲突。由于该约束只作用于 Web 端口，Relay 入口的 `/{namespace}/{absolute-url}` 不受影响。

旧的 `/team-a/`、`/team-a/events` 和 `/team-a/api/transactions` 等路径必须明确返回 404，不能落入 SPA 或 FileServer 返回默认页面。`/namespace/team-a` 使用保留 query 的 307/308 重定向规范化到 `/namespace/team-a/`。空 namespace、多级 namespace、双斜杠、`.`、`..`、编码斜杠和双重编码路径必须拒绝；路由解析同时检查 `URL.Path` 和 `RawPath`，避免编码绕过单段限制。

### 管理页面

新增保留路由：

```text
/admin/                       管理页面
/admin/logout                 退出管理会话
/admin/events                 管理统计 SSE
/admin/api/namespaces         namespace 统计 API
/admin/api/tokens             JWT 签发 API
```

管理路由与 `/namespace/{namespace}/` 结构完全分离，不参与 namespace 解析。管理页面只有在以下条件全部满足时才能访问：

- TOML 使用 JWT 认证模式。
- `web.auth.admin_enabled = true`。
- Cookie 中 JWT 签名、时间、issuer 和 audience 验证通过。
- JWT 的 `namespace` 为空字符串。

`admin_enabled = false` 时，所有 `/admin/**` 页面和 API 均返回 404，不暴露管理能力是否存在。

管理页面显示的 namespace 列表是以下集合的并集：

- TOML 中显式配置的 namespace。
- 当前 Web store 中仍然保留记录的 namespace。
- 当前存在 SSE 订阅的 namespace，即使它尚无记录且未显式配置。
- 无 namespace 的默认视图，以 `(default)` 展示。

每个 namespace 至少显示：

```text
namespace
是否受保护
认证策略来源（default / fallback / explicit）
当前保留记录条数
最后一条记录时间
当前 SSE 订阅数
```

管理统计由服务端在一次锁内生成一致快照，前端不能通过下载所有 namespace 的交易再自行统计。

管理页面提供受限 namespace JWT 创建表单：

```text
namespace
有效期
```

管理 API：

```http
POST /admin/api/tokens
Content-Type: application/json

{
  "namespace": "team-a",
  "ttl": "24h"
}
```

成功响应只返回一次新签发的 JWT 和到期时间：

```json
{
  "token": "eyJ...",
  "expiresAt": "2026-08-06T12:00:00+08:00"
}
```

Token API 和全部 `/admin/**` 响应必须设置 `Cache-Control: no-store`。管理页面同时设置严格 CSP、`Referrer-Policy: no-referrer` 和 `X-Content-Type-Options: nosniff`。JWT 不进入 URL、日志、localStorage 或 sessionStorage；页面只短暂显示，提供复制和主动清除操作，所有动态文本使用 `textContent` 渲染。

所有登录页和登录响应同样设置 `Cache-Control: no-store`，避免提交或认证结果被浏览器及中间代理缓存。

第一版管理页面只签发 namespace 非空的受限 Token，不签发 namespace 为空的管理 Token。首个及后续管理 Token 均通过 `http-relay-auth issue --admin` 离线生成，减少在浏览器中误创建高权限 Token 的风险。

JWT 是无状态凭据，因此第一版管理页面不会展示“已签发 Token 列表”，也不提供单 Token 吊销。Secret 轮换会使所有 JWT 失效。

所有管理写操作必须：

- 要求 namespace 为空的有效管理 Token。
- 只接受 JSON POST，不使用 GET 触发变更。
- 校验 `Origin` 与当前 Web Origin 一致。
- 限制请求正文大小。
- 限制 TTL 不超过 `max_token_ttl`。
- 校验 namespace 和所有可签发声明，禁止客户端提交任意 JWT Payload。
- 在服务端日志中记录管理 Token 的 JTI、目标 namespace、新 Token 的 JTI 和到期时间，但绝不记录完整 JWT。
- 对签发接口实施按客户端的速率限制。
- 对 namespace、TTL 和审计字段做长度、字符集、控制字符及换行校验。
- 允许为公开 namespace 签发 Token，但 UI 必须明确提示该 namespace 当前不要求认证。

登录、Clear、所有 logout 和管理写接口都必须执行同源检查并限制正文大小。浏览器写接口缺少或不匹配 `Origin` 时拒绝请求。沙箱页面发送 `Origin: null` 时，仅在浏览器同时提供不可由页面脚本设置的 `Sec-Fetch-Site: same-origin` 时接受；其他 null Origin 仍拒绝。Origin 计算必须基于可信的服务端配置或经过明确信任的反向代理信息，不能盲目信任任意客户端提交的转发头。

### 独立认证工具

新增独立二进制：

```text
cmd/http-relay-auth/main.go
```

主程序 `http-relay` 不增加认证相关子命令。

生成随机 Secret：

```bash
http-relay-auth secret
```

签发 namespace Token：

```bash
http-relay-auth issue \
  --config ./http-relay.toml \
  --namespace team-a
```

覆盖默认 TTL：

```bash
http-relay-auth issue \
  --config ./http-relay.toml \
  --namespace team-a \
  --ttl 24h
```

签发用于初始化管理页面的管理 Token：

```bash
http-relay-auth issue \
  --config ./http-relay.toml \
  --admin
```

检查并验证 Token：

```bash
http-relay-auth inspect \
  --config ./http-relay.toml \
  -
```

推荐通过 stdin 传入，避免 Token 留在 shell history 或进程参数中：

```bash
printf '%s' "$TOKEN" | http-relay-auth inspect --config ./http-relay.toml -
```

`--namespace` 和 `--admin` 必须二选一。`--admin` 生成 namespace 为空的管理 Token；Token 默认只输出到标准输出，方便复制、重定向和脚本调用。

`--permanent` 与 `--ttl` 互斥，且只有配置启用 `allow_permanent_tokens` 时才能签发。`inspect` 对永久 Token 显示 `expires: never`。永久 Token 第一版无法按 JTI 单独吊销，只能通过轮换 Secret 使全部旧 Token 失效。

`secret` 和 `issue` 成功时 stdout 只输出 Secret 或 JWT，提示与错误统一写入 stderr。`inspect` 默认不回显完整 Token，并使用明确退出码区分参数错误、配置错误和 Token 无效。CLI 与服务端共享同一套配置、claims、签发和校验实现。

## 待办项

### 配置解析

- [x] 选择并引入支持严格字段校验的 TOML 解析库。
- [x] 新增共享配置模型和 `--config` 参数。
- [x] 支持 `HTTP_RELAY_CONFIG` 配置文件路径环境变量。
- [x] 实现 `WEB_AUTH_JWT_SECRET` 对 TOML Secret 的覆盖。
- [x] 明确只有 TOML `mode = "jwt"` 才启用 JWT，Secret 环境变量不能隐式启用。
- [x] 校验 Secret 的无 padding Base64URL 规范编码和解码后最小长度。
- [x] 确保 CLI 与服务端使用解码后的原始字节作为 HMAC key。
- [x] 校验 namespace 配置名称。
- [x] 校验 TTL 为正、默认值不超过上限且时间计算不溢出。
- [x] 支持默认关闭的 `allow_permanent_tokens` 配置。
- [x] 拒绝未知 TOML 字段。
- [x] 内嵌 Secret 的配置文件权限宽于 `0600` 时输出警告。
- [x] JWT 模式与 `WEB_AUTH_KEY` 同时启用时返回明确错误。
- [x] 保证未提供 TOML 时现有 CLI 和环境变量行为不变。

### JWT 核心能力

- [x] 新增共享的 HS256 JWT 签发与验证包。
- [x] 使用安全随机数生成 `jti`。
- [x] 固定验证 `alg = HS256` 和 `typ = JWT`。
- [x] 验证 issuer、audience 和 namespace。
- [x] 禁止 JWT 携带或依赖用户身份和角色声明进行授权。
- [x] 将空 namespace 固定为管理 scope，非空 namespace 固定为受限 scope。
- [x] `/admin/**` 必须要求 namespace 为空的管理 Token。
- [x] 管理 Token 可以访问默认视图和任意 namespace。
- [x] 拒绝 `namespace = "*"` 和其他非法 scope。
- [x] 验证 `iat`、`nbf`、`exp` 并支持 30 秒时钟误差。
- [x] 拒绝未获配置允许的缺少有效期 Token，以及使用通配 namespace 的 Token。
- [x] 配置允许时接受无 `exp` 的永久 Token，否则拒绝。
- [x] 为签发和验证增加表驱动单元测试。

### Web 认证集成

- [x] 抽象现有全局密码认证和新的 JWT 认证模式。
- [x] 根据当前 namespace 解析 `default_protected`、`fallback_protected` 和精确覆盖规则。
- [x] 增加 namespace 独立 Cookie 名称。
- [x] 增加固定且独立的管理 Cookie 名称。
- [x] Cookie 名的 namespace 摘要至少使用 SHA-256 前 128 bit。
- [x] namespace Cookie 使用 namespace Path，管理 Cookie 使用根 Path。
- [x] 删除 Cookie 时复用完全相同的名称和 Path。
- [x] JWT 登录成功后将 Token 写入 HttpOnly Cookie。
- [x] Cookie 到期时间不得超过 JWT 的 `exp`。
- [x] 实现统一 `/login`，根据 Token namespace 设置 Cookie 并使用 303 跳转。
- [x] 非空 namespace 登录后跳转到 `/namespace/{namespace}/`。
- [x] 空 namespace 登录后跳转到 `/admin/`。
- [x] 登录接口不接受 `next` 控制跳转目标。
- [x] 将 Web namespace 路由统一迁移到 `/namespace/{namespace}/...`。
- [x] 删除旧的 Web `/{namespace}/...` 路由，不增加兼容处理或重定向。
- [x] 保持 Relay 入口的 `/{namespace}/{absolute-url}` 路径不变。
- [x] 删除依赖根路径白名单判断 namespace 的路由逻辑。
- [x] 支持 `/namespace/{namespace}/logout`，且只删除当前 namespace Cookie。
- [x] 将 Logout 表单调整为 namespace 相对路径。
- [x] 所有前端认证失败统一跳转到 `/login`。
- [x] 根据当前路由基址生成表单、静态资源、SSE 和 API 地址。
- [x] 动态生成当前 namespace 的 `Meta.AuthEnabled`。
- [x] 保护 namespace 页面、静态资源、SSE、交易 API 和 Clear API。
- [x] namespace SSE 在 JWT 到期时主动断开。
- [x] login、Clear 和所有 logout 写操作执行同源检查及正文限制。
- [x] 保持公开 namespace 不要求登录。

### 管理页面

- [x] 将 `/admin/` 注册为独立的 Web 根路由，不进入 `/namespace/{namespace}/` 路由分支。
- [x] 使用统一 `/login` 实现管理 Token 登录，并实现管理 Cookie 验证和退出流程。
- [x] 确保非空 namespace Token 无法访问管理页。
- [x] `admin_enabled = false` 时所有 `/admin/**` 返回 404。
- [x] 聚合配置 namespace、store namespace 和默认视图。
- [x] 将只有活跃 SSE 订阅的 namespace 纳入管理集合。
- [x] 统计每个 namespace 的保留记录数、最后记录时间和 SSE 订阅数。
- [x] 在一次锁内生成管理统计一致快照。
- [x] 实现 `GET /admin/api/namespaces`。
- [x] 实现管理统计的实时 SSE 更新。
- [x] 管理 SSE 在管理 JWT 到期时主动断开。
- [x] 构建 `/admin/` namespace 列表和统计界面。
- [x] 构建受限 namespace JWT 创建表单。
- [x] 实现 `POST /admin/api/tokens`，且只允许签发 namespace 非空的受限 Token。
- [x] 对管理写接口实施同源 Origin 检查和正文大小限制。
- [x] 明确可信反向代理配置，禁止盲目信任客户端转发头计算 Origin。
- [x] 校验签发 TTL 不超过 `max_token_ttl`。
- [x] 对签发接口实施速率限制和最小 TTL 校验。
- [x] 校验 namespace 及审计字段，阻止控制字符和日志换行注入。
- [x] 记录不包含完整 Token 的安全审计日志。
- [x] 确保管理 API 不提供 Secret、完整历史正文或现有 JWT。
- [x] 为 `/admin/**` 和 Token API 设置 no-store、CSP、no-referrer 与 nosniff。
- [x] 管理 UI 不持久化 Token，只短暂显示并支持复制和主动清除。
- [x] 管理 UI 所有动态内容使用 `textContent` 等安全方式渲染。
- [x] 为公开 namespace 签发 Token 时显示明确提示。

### 独立命令

- [x] 新增 `cmd/http-relay-auth` 二进制入口。
- [x] 实现 `http-relay-auth secret`。
- [x] 实现 `http-relay-auth issue`。
- [x] 实现 `http-relay-auth inspect`。
- [x] 支持从 TOML 和 `WEB_AUTH_JWT_SECRET` 读取 Secret。
- [x] `--config` 与 `HTTP_RELAY_CONFIG` 使用和主程序一致的优先级。
- [x] 校验 `--namespace` 与 `--admin` 必须二选一。
- [x] `--namespace` 只签发非空 namespace Token。
- [x] 支持互斥的 `--admin` 参数生成空 namespace 管理 Token。
- [x] 支持通过 `--ttl` 覆盖配置中的默认 TTL。
- [x] 支持与 `--ttl` 互斥的 `--permanent`。
- [x] `issue` 和服务端共享输入、claims、TTL 与签名校验实现。
- [x] 保证 `issue` 默认只向标准输出写入 JWT。
- [x] 保证 `secret` stdout 只输出 Secret，所有提示和错误走 stderr。
- [x] `inspect` 支持从 stdin 的 `-` 读取且默认不回显完整 Token。
- [x] 定义参数、配置和无效 Token 的稳定退出码。
- [x] 为所有子命令补充帮助文本和错误用例测试。

### 隔离与安全测试

- [x] 验证 team-a Token 不能访问 team-b 页面。
- [x] 验证 team-a Token 不能访问 team-b SSE。
- [x] 验证 team-a Token 不能查询或清理 team-b 交易。
- [x] 验证同一浏览器可以同时持有多个 namespace Cookie。
- [x] 验证退出 team-a 不影响 team-b Cookie。
- [x] 验证过期、未生效、签名错误和 namespace 不匹配的 Token 被拒绝。
- [x] 验证永久 Token 的签发、接受、禁用和 inspect 输出。
- [x] 验证 namespace 和 admin SSE 在 JWT 到期后主动断开。
- [x] 验证通配和其他非法 namespace scope 被拒绝。
- [x] 验证公开 namespace 不受其他认证配置影响。
- [x] 验证旧 `WEB_AUTH_KEY` 的登录和 Session 测试继续通过。
- [x] 验证非空 namespace Token 无法访问管理页面和管理 API。
- [x] 验证空 namespace 管理 Token 可以查看跨 namespace 统计。
- [x] 验证管理 Token 可以访问默认视图和任意 namespace。
- [x] 验证管理页签发的 Token 只能访问指定 namespace。
- [x] 验证管理页无法签发空 namespace 管理 Token 或注入任意声明。
- [x] 验证管理写接口拒绝跨 Origin 请求。
- [x] 验证 login、Clear 和 logout 拒绝缺失或不匹配的浏览器 Origin。
- [x] 验证 `/namespace/team-a/` 的页面、静态资源、SSE 和 API 使用同一 namespace。
- [x] 验证 `admin`、`login`、`events` 和 `app.js` 可以作为普通 namespace 名称。
- [x] 验证 `/namespace/default/` 表示名为 `default` 的真实 namespace，而根路径 `/` 仍表示无 namespace 视图。
- [x] 验证旧 `/team-a/`、`/team-a/events` 和 `/team-a/api/transactions` 均返回 404 且不重定向、不返回默认页面。
- [x] 验证 `/namespace/team-a` 保留 query 重定向到带尾斜杠路径。
- [x] 验证空、多级、点段、双斜杠、编码斜杠及双重编码 namespace 路径被拒绝。
- [x] 验证配置、记录和仅有订阅的 namespace 都出现在一致的管理快照中。
- [x] 运行 `go test ./...` 和认证相关 race 测试。

### 文档与发布

- [x] 更新中英文 README 的 TOML 和 JWT 配置说明。
- [x] 记录管理 Token 的初始化、保管和轮换流程。
- [x] 增加管理页面和受限 namespace Token 签发使用说明。
- [x] 明确 JWT 只保护 Web 读取和清理权限，不保护 Relay 写入入口。
- [x] 增加 Docker Secret 和环境变量配置示例。
- [x] 增加示例配置到 `.gitignore` 或提供不含 Secret 的模板，避免误提交真实 Secret。
- [x] 更新 Makefile，支持构建两个二进制。
- [x] 更新安装脚本和 Release 流程，发布 `http-relay-auth`。
- [x] 保持运行镜像只包含 `http-relay`，除非后续明确需要在容器内签发 Token。

## 暂不包含

- Token JTI 吊销列表。
- 受限 Token 的 namespace 通配或多 namespace scope。
- JWT 刷新令牌。
- Relay 写入端认证。
- 非对称公私钥签名。
- 配置文件热加载。

这些能力可以在实际需要出现后独立设计，第一版保持认证模型和运维流程简单。
