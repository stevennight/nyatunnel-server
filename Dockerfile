# syntax=docker/dockerfile:1.7

FROM --platform=$BUILDPLATFORM node:24-alpine AS web
WORKDIR /src/web/app
COPY web/app/package.json web/app/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/app ./
# vite writes to ../../.tmp-webdist, i.e. /src/.tmp-webdist
RUN npm run build

FROM --platform=$BUILDPLATFORM golang:1.26-alpine AS go-build
ARG TARGETOS=linux
ARG TARGETARCH=amd64
ARG NYATUNNEL_VERSION=0.1.0-dev
ARG NYATUNNEL_COMMIT=
ARG NYATUNNEL_BUILD_DATE=
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS="$TARGETOS" GOARCH="$TARGETARCH" go build -trimpath -buildvcs=false \
    -ldflags "-s -w -X nyatunnel-server/internal/shared/version.Version=${NYATUNNEL_VERSION} -X nyatunnel-server/internal/shared/version.Commit=${NYATUNNEL_COMMIT} -X nyatunnel-server/internal/shared/version.BuildDate=${NYATUNNEL_BUILD_DATE}" \
    -o /out/nyatunnel-server ./cmd/server

FROM alpine:3.22
RUN apk add --no-cache su-exec tzdata \
    && addgroup -S nyatunnel \
    && adduser -S -G nyatunnel nyatunnel
WORKDIR /app
COPY --from=go-build /out/nyatunnel-server /usr/local/bin/nyatunnel-server
COPY --from=web /src/.tmp-webdist /app/.tmp-webdist
COPY deploy/docker/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh
# Console and API on 8080, HTTP tunnel ingress on 8081; meant for host networking behind Caddy.
EXPOSE 8080 8081
ENV NYATUNNEL_DATA=/data \
    NYATUNNEL_LISTEN=127.0.0.1:8080 \
    NYATUNNEL_WEB_DIR=/app/.tmp-webdist
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD ["/usr/local/bin/nyatunnel-server", "--healthcheck"]
ENTRYPOINT ["/entrypoint.sh"]
