<div align="center">

# http-relay

轻量 HTTP 转发工具。

[![CI](https://github.com/onewesong/http-relay/actions/workflows/ci.yml/badge.svg)](https://github.com/onewesong/http-relay/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/onewesong/http-relay)](https://github.com/onewesong/http-relay/releases)
[![License](https://img.shields.io/github/license/onewesong/http-relay)](https://github.com/onewesong/http-relay/blob/main/LICENSE)
[![Ask DeepWiki](https://deepwiki.com/badge.svg)](https://deepwiki.com/onewesong/http-relay)

[English](./README.md) | 简体中文

</div>

`http-relay` 监听本地 HTTP，请求格式如下：

`http://localhost:{port}/https://example.com/path?...`

它会将路径中的绝对 URL 作为上游目标进行转发，并原样返回上游响应（状态码、响应头、响应体）。

## 安装

```bash
go install github.com/onewesong/http-relay/cmd/http-relay@latest
```

## 快速开始

1. 启动服务（默认 `127.0.0.1:8080`）：

```bash
http-relay
```

2. 发起请求：

```bash
curl -i "http://127.0.0.1:8080/https://example.com"
```

查看版本：

```bash
http-relay version
```

## 配置（环境变量）

- `HOST`：监听地址（默认 `127.0.0.1`）
- `PORT`：监听端口（默认 `8080`）

示例：

```bash
HOST=0.0.0.0 PORT=9000 http-relay
```

## 抓包输出

开启请求/响应转储：

```bash
http-relay -w
```

对请求头认证信息脱敏：

```bash
http-relay -w -mask-auth
```

会脱敏的请求头：
`Authorization`、`Proxy-Authorization`、`Cookie`、`X-Api-Key`、`X-Auth-Token`。

使用 `WIRE_SCOPE` 控制输出范围（仅 `-w` 开启时生效）：

- `req`：只输出请求
- `resp`：只输出响应
- `req,resp`：请求和响应都输出（默认）

示例：

```bash
WIRE_SCOPE=req http-relay -w
WIRE_SCOPE=resp http-relay -w
WIRE_SCOPE=req,resp http-relay -w
```

## 上游代理

支持标准代理环境变量：

- `ALL_PROXY`（优先级最高）
- `HTTP_PROXY` / `HTTPS_PROXY`
- `NO_PROXY`（命中后直连）

示例：

```bash
HTTPS_PROXY=http://127.0.0.1:7890 http-relay
ALL_PROXY=socks5://127.0.0.1:1080 http-relay
HTTPS_PROXY=http://127.0.0.1:7890 NO_PROXY=example.com http-relay
```

## 路由规则

仅支持 `/{absolute-url}`，例如：

- `http://127.0.0.1:8080/https://example.com`
- `http://127.0.0.1:8080/http://httpbin.org/post`

目标 URL 必须包含 `http://` 或 `https://`。

## 错误码

- `400`：目标 URL 缺失或格式错误
- `502`：上游连接失败或超时
- `500`：服务内部错误
