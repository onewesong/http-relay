# Docker 部署

## 直接运行

```bash
docker run --rm \
  -p 127.0.0.1:7080:7080 \
  -p 127.0.0.1:7090:7090 \
  ghcr.io/onewesong/http-relay:latest \
  --listen 0.0.0.0:7080 \
  --web --web-listen 0.0.0.0:7090
```

镜像标签规则：

- `latest`：最新正式 Release
- `v1.2.3`、`1.2`、`1`：对应语义版本
- `edge`：`main` 分支最新构建
- `sha-*`：按提交定位的构建

## Docker Compose

```yaml
services:
  http-relay:
    image: ghcr.io/onewesong/http-relay:latest
    restart: unless-stopped
    command:
      - --listen
      - 0.0.0.0:7080
      - --web
      - --web-listen
      - 0.0.0.0:7090
    environment:
      WEB_AUTH_KEY: ${WEB_AUTH_KEY}
    ports:
      - 127.0.0.1:7080:7080
      - 127.0.0.1:7090:7090
```

## 挂载 TOML 配置

```yaml
services:
  http-relay:
    image: ghcr.io/onewesong/http-relay:latest
    command: ["--config", "/etc/http-relay/config.toml", "--listen", "0.0.0.0:7080"]
    environment:
      WEB_AUTH_JWT_SECRET: ${WEB_AUTH_JWT_SECRET}
    volumes:
      - ./http-relay.toml:/etc/http-relay/config.toml:ro
```

不要将包含 JWT Secret 的配置提交到 Git。生产环境推荐通过 Secret 管理系统注入 `WEB_AUTH_JWT_SECRET`。
