# syntax=docker/dockerfile:1

# 前端产物不区分 CPU 架构，固定在构建机原生平台执行，避免 arm64 的 QEMU 模拟。
# Vite 输出到 ../server/web，供 Go 的 //go:embed 打包。
FROM --platform=$BUILDPLATFORM node:20-alpine AS web-builder
WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci
COPY web/ ./
RUN npm run build

# 后端使用原生构建机交叉编译目标架构。CGO 关闭后 Go 能可靠交叉编译，
# 避免 linux/arm64 的 go build 在 QEMU 下耗时数分钟。
# 必须与 server/go.mod 的 go 指令保持一致；低版本会在 go mod download 阶段失败。
FROM --platform=$BUILDPLATFORM golang:1.25-alpine AS server-builder
WORKDIR /src/server
ARG TARGETOS
ARG TARGETARCH
COPY server/go.mod server/go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod go mod download
COPY server/ ./
COPY --from=web-builder /src/server/web ./web
ARG VERSION=dev
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -tags ui -trimpath -ldflags="-s -w -X github.com/ovh-buy/server/internal/handlers.Version=${VERSION}" -o /out/ovh-server .

# 运行时仅保留 CA 证书、时区数据和单一二进制。
FROM alpine:3.21
RUN apk add --no-cache ca-certificates tzdata su-exec \
    && addgroup -S -g 10001 ovh \
    && adduser -S -D -H -u 10001 -G ovh ovh
WORKDIR /app
COPY --from=server-builder /out/ovh-server /usr/local/bin/ovh-server
COPY docker/entrypoint.sh /usr/local/bin/entrypoint
RUN chmod 0755 /usr/local/bin/ovh-server /usr/local/bin/entrypoint && mkdir -p /data && chown ovh:ovh /data
# 运行时默认时区与用户可见时间一致；Go 代码仍显式使用 Asia/Shanghai，避免被宿主环境覆盖。
ENV TZ=Asia/Shanghai \
    DATA_DIR=/data \
    CACHE_DIR=/data/cache \
    LOGS_DIR=/data/logs \
    PORT=19998 \
    GIN_MODE=release
EXPOSE 19998
VOLUME ["/data"]
ENTRYPOINT ["/usr/local/bin/entrypoint"]
