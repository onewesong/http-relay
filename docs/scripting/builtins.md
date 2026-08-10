# 内置兼容脚本

内置脚本通过 `builtin:<文件名>` 引用，随 Release 二进制一起发布，无需额外复制文件。

## OpenAI 请求规范化

```toml
[rewrite.profiles.openai]
script = "builtin:rewrite.openai.js"
timeout = "500ms"
reload = "off"
```

## Chat Completions 转 Responses

```toml
[rewrite.profiles.openai-compat]
script = "builtin:rewrite.chat-completions-to-responses.js"
timeout = "200ms"
reload = "off"
```

客户端继续调用 `/v1/chat/completions`，Relay 将请求转换为上游 Responses API，并将响应转换回 Chat Completions 格式。`stream: true` 时会实时转换 SSE。

## Anthropic Messages 转 Responses

```toml
[rewrite.profiles.anthropic-compat]
script = "builtin:rewrite.anthropic-messages-to-responses.js"
timeout = "200ms"
reload = "off"
```

支持文本、base64 图片、自定义工具与 SSE，并把 `x-api-key` 转换为 Bearer 鉴权。

## Anthropic Messages 转 Chat Completions

```toml
[rewrite.profiles.anthropic-chat-compat]
script = "builtin:rewrite.anthropic-messages-to-chat-completions.js"
timeout = "200ms"
reload = "off"
```

适用于只支持 Chat Completions 的上游，Anthropic 客户端仍保持 `/v1/messages` 请求格式。

完整默认配置见 [`config.example.toml`](https://github.com/onewesong/http-relay/blob/main/config.example.toml)。
