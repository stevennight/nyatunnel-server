# NyaTunnel Server

NyaTunnel 的服务端：自托管的内网穿透服务，带 Web 管理后台、账号体系和自研数据面。**所有对外入口（域名、端口、访问策略）只在后台定义**，客户端只能领取分配给自己的隧道；服务端只为已登记、已启用、属于该设备的隧道转发流量。设计上部署在已有的 Caddy 之后，证书由 Caddy 管理。

## 仓库关系

NyaTunnel 由三个独立仓库组成，通过 [协议](docs/协议.md) 对接：

- `nyatunnel-server`（本仓库）：管理后台、账号、设备会话、HTTP 隧道入口、TCP/UDP 端口
- [`nyatunnel-common`](https://github.com/stevennight/nyatunnel-common)：协议实现（`tunnelproto`）与深链（`deeplink`）
- [`nyatunnel-client`](https://github.com/stevennight/nyatunnel-client)：Go 核心 + CLI，桌面端为 Tauri 2

文档：[设计方案](docs/设计方案.md) · [开发计划](docs/开发计划.md) · [通信协议](docs/协议.md) · [交互原型](docs/prototype.html)

## 功能

- **账号**：管理员与普通用户；argon2id 密码、TOTP 两步验证与一次性恢复码、可强制所有人开启 TOTP、登录限速、会话列表与远程退出
- **设备**：一次性注册码 / `nyatunnel://` 深链 / 二维码邀请；每台设备独立 Ed25519 密钥；吊销即断；客户端最低版本
- **隧道**：HTTPS（子域名或自定义域名）、TCP、UDP；端口池；子域名保留字、品牌词与“用户名-”前缀；本地目标可限制为回环地址
- **访问控制**：访问密码、HTTP Basic、NyaTunnel 账号登录门禁、IP 白名单、首次访问风险提示页、Host 改写
- **限制**：每隧道带宽、并发连接、月流量（超额暂停或仅告警）、到期时间；每账号月流量
- **流程**：普通用户在自助额度内自建隧道，超出则提交申请（管理台或客户端），管理员可修改后批准；自定义域名申请 → 审批 → DNS 校验 → 生效
- **可见性**：仪表盘、每小时流量统计与曲线、审计日志、Webhook（HMAC 签名）/ Telegram 通知（申请、新设备、配额、流量突增、登录爆破、域名变化）
- **传输**：设备经 443 的 WebSocket（yamux 多路复用）连接；可选直连 TLS 端口（证书指纹钉住，失败回落 443）

## 开发

需要 Go（版本见 `go.mod`）和 Node.js 24。本地联调公共库时使用工作区根目录的 `go.work`（见上级目录的 `CLAUDE.md`）。

```powershell
go test ./...
go vet ./...
```

构建 Web 管理台（输出到仓库根的 `.tmp-webdist`，Go 服务会自动托管）：

```powershell
cd web/app
npm ci
npm test
npm run build
```

运行服务。首次启动时日志里会打印一次性的 `setup_token`，在管理台初始化页面填入它来创建管理员：

```powershell
go run ./cmd/server            # 管理台 127.0.0.1:8080，隧道入口 127.0.0.1:8081，内部 127.0.0.1:8082
go run ./cmd/server --version
```

前端热更新开发（`/api` 代理到 `127.0.0.1:8080`）：另开一个终端运行 `npm run dev --prefix web/app`。

### 配置

全部通过环境变量：

| 变量 | 默认 | 说明 |
|---|---|---|
| `NYATUNNEL_PUBLIC_URL` | `http://127.0.0.1:8080` | 管理台与设备连接的公网地址，如 `https://tunnel.example.com` |
| `NYATUNNEL_LISTEN` | `127.0.0.1:8080` | 管理台 + API + 设备连接（由 Caddy 反代） |
| `NYATUNNEL_INGRESS_LISTEN` | `127.0.0.1:8081` | HTTP 隧道入口（由 Caddy 反代），永远不提供管理接口 |
| `NYATUNNEL_INTERNAL_LISTEN` | `127.0.0.1:8082` | Caddy on-demand TLS 询问，**不要反代** |
| `NYATUNNEL_PORT_BIND` | `0.0.0.0` | TCP/UDP 隧道端口的监听地址 |
| `NYATUNNEL_TRUSTED_PROXY_CIDRS` | `127.0.0.1/32,::1/128` | 信任其 `X-Forwarded-For` / `X-Forwarded-Proto` 的反代 |
| `NYATUNNEL_PUBLIC_IPS` | 空 | 本机公网 IP，自定义域名须解析到其中之一；留空则解析公网域名 |
| `NYATUNNEL_DIRECT_LISTEN` | 空 | 可选的设备直连端口，如 `0.0.0.0:7443` |
| `NYATUNNEL_DIRECT_ADDR` | 公网域名 + 直连端口 | 设备拨号用的直连地址 |
| `NYATUNNEL_DATA` | `data` | 数据目录（SQLite、直连证书） |
| `NYATUNNEL_SECRETS_KEY_FILE` | `$NYATUNNEL_DATA/secrets.key` | 加密 TOTP 密钥的主密钥，首次启动自动生成；请单独备份 |
| `NYATUNNEL_SESSION_TTL` | `720h` | 管理台登录有效期 |
| `NYATUNNEL_WEB_DIR` | `.tmp-webdist` | Web 管理台构建产物目录 |

## 部署

在已有 Caddy 之后运行：Caddy 继续占用 80/443 并负责全部证书（Cloudflare DNS-01 通配证书 + 自定义域名 on-demand）。NyaTunnel 只监听本机端口，以及对外的 TCP/UDP 端口池。完整步骤见 [设计方案 §12](docs/设计方案.md#12-部署)，Caddy 配置见 [`deploy/caddy/Caddyfile`](deploy/caddy/Caddyfile)。

```bash
mkdir -p /opt/nyatunnel-server && cd /opt/nyatunnel-server
curl -fsSLO https://raw.githubusercontent.com/stevennight/nyatunnel-server/main/deploy/docker/docker-compose.yml
curl -fsSL https://raw.githubusercontent.com/stevennight/nyatunnel-server/main/deploy/docker/.env.example -o .env
vi .env                                            # 公网地址、本机空闲端口、版本等
docker compose up -d
docker compose logs | grep setup_token
```

compose 文件本身不写死任何值，全部来自同目录的 `.env`；数据在 `./data`，TOTP 主密钥在 `./secrets`，备份整个目录即可。

镜像为 `ghcr.io/stevennight/nyatunnel-server`（linux/amd64、linux/arm64），compose 使用 `network_mode: host`。防火墙需放行端口池范围（以及可选的直连端口）。

## 发布

1. 修改 `VERSION` 和 `web/app/package.json` 的 `version`（两者必须一致），提交；
2. 推送同名标签：`git tag v0.1.0 && git push origin v0.1.0`；
3. `release.yml` 发布 GitHub Release（linux amd64/arm64 二进制、Web 包、`SHA256SUMS`）并推送 GHCR 镜像（`vX.Y.Z`、`X.Y`、`latest`）。带 `-beta.N` 后缀的标签发布为预发布，不更新 `latest`。
