# syntax=docker/dockerfile:1

# 前端构建：Vite 输出到 ../server/web，供 Go 的 //go:embed 打包。
FROM node:20-alpine AS web-builder
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

# 后端构建：使用纯 Go SQLite，运行镜像不需要 gcc 或 SQLite 动态库。
# 必须与 server/go.mod 的 go 指令保持一致；低版本会在 go mod download 阶段失败。
FROM golang:1.25-alpine AS server-builder
WORKDIR /src/server
COPY server/go.mod server/go.sum ./
RUN go mod download
COPY server/ ./
COPY --from=web-builder /src/server/web ./web
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -tags ui -trimpath -ldflags="-s -w -X github.com/ovh-buy/server/internal/handlers.Version=${VERSION}" -o /out/ovh-server .

# 运行时仅保留 CA 证书、时区数据和单一二进制。
FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata su-exec \
    && addgroup -S -g 10001 ovh \
    && adduser -S -D -H -u 10001 -G ovh ovh
WORKDIR /app
COPY --from=server-builder /out/ovh-server /usr/local/bin/ovh-server
COPY docker/entrypoint.sh /usr/local/bin/entrypoint
RUN chmod 0755 /usr/local/bin/ovh-server /usr/local/bin/entrypoint && mkdir -p /data && chown ovh:ovh /data
ENV DATA_DIR=/data \
    CACHE_DIR=/data/cache \
    LOGS_DIR=/data/logs \
    PORT=19998 \
    GIN_MODE=release
EXPOSE 19998
VOLUME ["/data"]
ENTRYPOINT ["/usr/local/bin/entrypoint"]
