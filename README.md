# NyaTunnel Server

NyaTunnel 的服务端：自托管的内网穿透服务，提供 Web 管理后台、账号体系和自研数据面。**所有对外入口（域名、端口、访问策略）只能在后台定义**，客户端只能领取分配给自己的隧道；服务端只为已登记的隧道转发流量。设计上部署在已有的 Caddy 之后，证书由 Caddy 管理。

## 仓库关系

NyaTunnel 由三个独立仓库组成，通过 [协议](docs/协议.md) 对接，协议实现在公共库里：

- `nyatunnel-server`（本仓库）：管理后台、账号、设备会话、HTTP 隧道入口、TCP/UDP 端口池
- `nyatunnel-common`：协议实现（`tunnelproto`）与深链（`deeplink`）
- `nyatunnel-client`：Go 核心 + CLI，GUI 为 Tauri 2

## 当前状态

**M0 骨架**：仓库、CI、版本管理、Docker 镜像、Caddy 配置示例、设计文档与交互原型。服务端目前只提供 `/healthz` 和管理台占位页。

- [设计方案](docs/设计方案.md) · [开发计划](docs/开发计划.md) · [通信协议](docs/协议.md) · [交互原型 prototype.html](docs/prototype.html)

## 开发

需要 Go（版本见 `go.mod`）和 Node.js 24。

```powershell
go test ./...
go vet ./...
```

构建 Web 管理台（输出到仓库根的 `.tmp-webdist`，Go 服务会自动托管；没有构建产物时显示内置的提示页）：

```powershell
cd web/app
npm ci
npm test
npm run build
```

运行服务：

```powershell
go run ./cmd/server           # 监听 :8080，数据在 ./data
go run ./cmd/server --version
```

前端热更新开发（`/api` 代理到 `127.0.0.1:8080`）：另开一个终端 `npm run dev --prefix web/app`。

### 配置

全部通过环境变量（前缀 `NYATUNNEL_`）：

| 变量 | 默认 | 说明 |
|---|---|---|
| `NYATUNNEL_LISTEN` | `:8080` | 管理台 / API 监听地址 |
| `NYATUNNEL_DATA` | `data` | 数据目录 |
| `NYATUNNEL_WEB_DIR` | `.tmp-webdist` | Web 管理台构建产物目录 |

## 部署

Docker 镜像：`ghcr.io/stevennight/nyatunnel-server`（linux/amd64、linux/arm64）。

```bash
cp deploy/docker/.env.example deploy/docker/.env
docker compose -f deploy/docker/docker-compose.yml --env-file deploy/docker/.env up -d
```

NyaTunnel 部署在已有 Caddy 之后：Caddy 继续占用 80/443 并负责全部证书（Cloudflare DNS-01 通配证书 + 自定义域名 on-demand），NyaTunnel 只监听 127.0.0.1 的 8080（管理台）、8081（隧道入口）、8082（内部，Caddy ask）以及对外的 TCP/UDP 端口池。参考 [`deploy/caddy/Caddyfile`](deploy/caddy/Caddyfile) 与 [设计方案 §12](docs/设计方案.md#12-部署)。从 M1 起 compose 改为 `network_mode: host`。

## 发布

1. 修改 `VERSION` 和 `web/app/package.json` 的 `version`（两者必须一致），提交；
2. 推送同名标签：`git tag v0.1.0 && git push origin v0.1.0`；
3. `release.yml` 会发布 GitHub Release（linux amd64/arm64 二进制、Web 包、`SHA256SUMS`）并推送 GHCR 镜像（`vX.Y.Z`、`X.Y`、`latest`）。带 `-beta.N` 后缀的标签发布为预发布，不更新 `latest`。
