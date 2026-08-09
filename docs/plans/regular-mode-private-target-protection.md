# Regular 模式内网目标保护实施计划

## 背景与目标

`regular` 模式将 Relay 请求路径中的绝对 URL 直接作为上游目标。例如：

```text
/https://api.example.com/v1/models
/http://127.0.0.1:8080/admin
```

目前只校验目标使用 `http` 或 `https` 且包含 Host，未限制目标 IP 或其 DNS
解析结果。因此一旦 Relay 端口暴露给不可信调用方，调用方可以借 Relay 访问
loopback、私网、链路本地地址或云元数据服务。

本方案的目标是：

- 在 `regular` 模式默认拒绝路径中指定的内网/非全局单播目标。
- 对域名先执行 DNS 解析；只要任一解析结果受限就拒绝，避免双栈回退到内网地址。
- 对直连请求把已通过校验的 IP 用于实际拨号，避免“校验后重新解析”的 DNS rebinding
  问题。
- 通过显式 CLI 参数让受信任部署能够恢复访问内网目标的原有行为。
- 不改变 `reverse:<url>` 的固定上游模型，也不改变脚本 `relay.http.request()` 已有的
  独立策略。

非目标：

- 本次不引入通用目标域名白名单、CIDR 白名单或按 namespace 的网络授权。
- 本次不限制由受信任运营者提供的 `--script` / Rewrite Profile 主动改写出的 `req.url`；
  该 URL 并非来自请求路径。后续若要将脚本改写也纳入同一策略，应单独设计其兼容性与
  权限模型。
- 本次不改变上游响应重定向的现有语义以外的行为；但重定向后的每个目标也必须经过同一
  校验，不能借由 30x 绕过限制。

## CLI 与行为约定

新增布尔参数：

```text
--allow-private-targets
```

- 默认 `false`：启用本计划的目标限制。
- 设置为 `true`：仅对 `regular` 模式关闭内网目标与 DNS 解析结果限制，恢复当前行为。
- 参数需要出现在 `--help`、启动日志和 README/中文 README 的安全说明中，明确这是高
  风险、仅适用于受信任网络的豁免。
- `reverse` 模式忽略此参数，因为其上游由进程启动参数固定，不能由请求路径选择。

拒绝时统一返回 `403 Forbidden`，错误信息使用稳定且不泄露 DNS 结果的文本，例如：

```text
target URL resolves to a prohibited address
```

解析失败应返回 `502 Bad Gateway`（上游 DNS 不可用），而不是把未验证的请求继续交给
默认 Transport；URL 格式错误仍维持 `400`。

## 目标判定规则

当限制开启时，允许的地址必须是全局单播地址。以下地址一律拒绝：

- IPv4/IPv6 unspecified、loopback、私网、链路本地单播或多播、multicast。
- 非 global-unicast 地址以及 CGNAT 网段 `100.64.0.0/10`。
- IPv4-mapped IPv6 地址在 Unmap 后按对应 IPv4 规则判断。
- URL 中直接写入的 IP，以及 DNS 返回的每一个 A/AAAA 记录；结果为空或解析失败也拒绝
  此次请求。

域名检查采用“全部地址均安全”而非“任一地址安全”：若一个 hostname 同时解析到公网与
内网地址，也必须拒绝，防止客户端、网络栈或 Happy Eyeballs 选择内网地址。

## 内部设计

### 可复用目标策略

在 `internal/relay` 新增一个不依赖 Handler 的目标策略组件（建议
`target_policy.go`），包含：

```go
type TargetPolicy struct {
    AllowPrivate bool
    Resolver     IPResolver // 生产环境默认 net.DefaultResolver；测试可注入
}

func (p TargetPolicy) ResolveAndValidate(ctx context.Context, u *url.URL) ([]net.IPAddr, error)
func forbiddenTargetIP(ip net.IP) bool
```

实现细节：

1. 先保留现有 URL 的 `http`/`https` 与 Host 校验。
2. 对字面 IP 直接形成单元素结果；对 hostname 调用 `LookupIPAddr`。
3. 限制开启时检查全部结果；失败不返回任何候选 IP。
4. 返回已校验的候选 IP 及原始端口，供 Transport 实际拨号；保留原 hostname 作为 HTTP
   Host 和 HTTPS SNI，避免改变虚拟主机与证书验证语义。

`internal/script/httpapi.go` 已实现相近的 IP 分类规则。实施时应将公共的 IP 判定提取到
一个最小共享内部包，或由 relay 策略复用，以避免两处规则（尤其 IPv6、CGNAT）漂移；
不能通过 import `internal/relay` 反向依赖 script。

### Handler 和脚本链路

在 `HandlerOptions` 增加目标策略，并在 `servePlain` 与 `serveScripted` 的上游请求执行前
应用它：

```text
解析路径 URL
→ （若有 Hook）执行 onRequest
→ 选择最终上游 URL
→ 校验/解析 URL，并把候选 IP 绑定到本次请求上下文
→ RoundTrip
```

这样能同时覆盖：

- 无脚本的普通转发；
- 路径选择 Profile 后的转发；
- 上游 30x 跳转的后续请求。

但只对“初始目标来自请求路径”的请求启用策略标记。若 Hook 改写 `req.url`，按本计划的
非目标定义不施加该限制；实现需明确携带来源标记，不能因仅比较 URL 字符串而产生误判。

短路响应不产生上游连接，不做 DNS 查询。

### Transport、重定向与代理

当前 `http.Client` 会自动处理重定向，且 Relay Transport 可选择 HTTP/SOCKS 上游代理。
需要把策略接入 Transport，而不是只在 Handler 预检：

1. 为每次受限请求在 `context.Context` 中放入经验证的 hostname、端口和 IP 候选集。
2. 直连 `DialContext` 只能使用该候选集中的 IP，并在轮换候选地址时再次确认它们仍符合
   策略；不得让 `net.Dialer` 以 hostname 重新解析。
3. `CheckRedirect` 在每一跳重新执行 URL/DNS 校验并创建新的拨号上下文；超过现有 Go 默认
   重定向上限的行为不变。
4. HTTP/SOCKS 代理可能由代理端解析目标 hostname，无法由本进程的 `DialContext` 保证
   rebinding 安全。限制开启时，受限请求必须绕过目标代理并使用受控直连 Transport；代理
   仅用于 `--allow-private-targets` 的旧行为。启动日志和文档须说明这一行为。

第 4 点是安全边界的一部分：仅在 Handler 中做一次 DNS 预检、随后仍让远端代理解析域名，
不能满足“解析结果受限即禁止”及防 rebinding 的目标。

## 接线位置

- `cmd/http-relay/main.go`
  - 注册 `--allow-private-targets`，构造 `TargetPolicy` 并传入普通、TUI、Web 三种
    Handler 创建路径。
  - 调整 `runTUI`、`runWeb` 的参数，保证它们不意外使用不同默认值。
  - 启动日志输出目标保护是否启用，以及在启用时上游代理对 Relay 目标不生效。
- `internal/relay/handler.go`
  - 在 `HandlerOptions` / `Handler` 保存策略；为初始路径目标标记受限请求。
  - 将策略错误映射为稳定的 403/502 响应，不记录敏感解析详情。
- `internal/relay/transport.go`
  - 为受限请求使用策略感知的直连 RoundTripper 和 redirect 校验；其他请求保持现有环境
    代理选择行为。
- `internal/relay/errors.go`
  - 如现有错误映射不适合区分策略拒绝与 DNS 故障，新增可识别的哨兵/包装错误，避免依据
    错误字符串判断 HTTP 状态。
- `README.md`、`README.zh-CN.md`、`docs/configuration.zh-CN.md`
  - 更新 regular 模式、上游代理和暴露端口的安全文档；提供内网开发场景使用显式豁免参数
    的示例。

## 测试计划

### 单元测试

在 `internal/relay` 为可注入 Resolver 的策略补充表驱动测试：

- 允许公网 IPv4、IPv6 和合法 hostname。
- 拒绝 `127.0.0.1`、`::1`、RFC1918、链路本地、multicast、unspecified、CGNAT 与
  IPv4-mapped IPv6。
- hostname 解析到单个内网 IP、混合公网/内网 IP、空结果、DNS 错误。
- `AllowPrivate=true` 时保留字面 IP 与 DNS hostname 的原有允许行为。

### Handler / Transport 集成测试

- regular 路径指向 loopback 测试服务器时，默认返回 403，且上游 handler 未被命中。
- 注入 Resolver 使公网样式域名解析到 loopback 时，默认返回 403。
- 注入多个安全候选 IP 时，受控拨号只使用这些 IP，验证不会再次按 hostname 查询。
- `--allow-private-targets` 时，loopback 测试服务器可成功访问。
- 普通路径、namespace、Profile 路径均受保护；短路 Hook 不触发解析。
- HTTP 30x 从公网测试域名跳往内网解析结果时被拒绝；确认响应体不会泄露内网响应。
- reverse 模式保持固定上游拼接语义，参数开关不改变其结果。
- 配置 HTTP/SOCKS 代理时，保护开启的路径目标不走代理；豁免开启时继续遵从现有代理
  选择和 `NO_PROXY` 测试。

### 回归验证

```bash
go test ./internal/relay ./internal/script ./cmd/http-relay
go test -race ./internal/relay ./internal/script
go test ./...
```

并用两个本地测试服务手动验证：默认访问
`http://127.0.0.1:7080/http://127.0.0.1:<port>/` 返回 403；加
`--allow-private-targets` 后能按预期转发。

## 实施顺序

- [x] 实现可注入的 relay 目标策略与单元测试，覆盖字面 IP、DNS 结果和混合解析结果。
- [x] 将策略接入 Handler，并在 `http.Client` 自动重定向时逐跳重新校验。
- [x] 注册 `--allow-private-targets` 并贯通普通、TUI 和 Web 启动路径。
- [x] 更新帮助文本、上游代理说明和中英文安全文档。
- [x] 运行相关包测试和全量测试。
- [x] 将已校验 IP 绑定到实际直连拨号，并在保护启用时绕过 HTTP/SOCKS 上游代理，消除校验后 DNS rebinding 的窗口。
