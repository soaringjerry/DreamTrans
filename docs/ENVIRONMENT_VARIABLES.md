# 环境变量配置

建议从 [`backend/.env.example`](../backend/.env.example) 复制配置。生产环境
的 `.env` 应设为 `0600`，不要提交到 Git。

## Docker Compose

根目录 Compose 会读取项目根目录的 `.env`：

```bash
cp backend/.env.example .env
chmod 600 .env
docker compose up -d
```

主要部署变量：

```dotenv
# 主机监听；默认仅本机可访问
BIND_ADDRESS=127.0.0.1
PORT=16002
IMAGE_TAG=latest

# 必填
SM_API_KEY=...
POSTGRES_DB=dreamtrans
POSTGRES_USER=dreamtrans
POSTGRES_PASSWORD=...
JWT_SECRET=...
JWT_REFRESH_SECRET=...

# 新数据库建议同时设置；已有安全管理员时可留空
ADMIN_EMAIL=admin@example.com
ADMIN_PASSWORD=...
```

`POSTGRES_PASSWORD`、`JWT_SECRET`、`JWT_REFRESH_SECRET` 必须是三个不同的
随机值。JWT 两把密钥至少 32 字符；管理员密码至少 16 字符。项目没有
默认数据库密码、JWT 密钥或管理员账户。

一键安装器会自动生成这些密钥，并把与所选应用镜像完全一致的迁移包
从镜像中提取出来。`--update --tag <tag>` 会持久化新镜像标签。

## 后端运行时

### Speechmatics

```dotenv
SM_API_KEY=...
BATCH_BILLING_RESERVATION_MINUTES=10080
ALLOW_UNMETERED_CLASSIC_TOKEN_WITH_BILLING=false
CLASSIC_TOKEN_BILLING_MINUTES=10
```

实时和批量转录都需要它。密钥只保存在服务端。数据库计费模式下：

- 实时客户端必须使用可测量音频字节并增量结算的 `/ws/speechmatics`。
  `/api/token/rt` 默认拒绝发放后端无法继续测量或限制复用的临时直连
  token。只有明确设置
  `ALLOW_UNMETERED_CLASSIC_TOKEN_WITH_BILLING=true` 才会恢复旧行为；
  `CLASSIC_TOKEN_BILLING_MINUTES` 的固定扣费并不代表真实用量，运营方需
  自行承担余额和上游成本风险。
- 压缩批量音频在提交前无法可靠推导时长。默认
  `BATCH_BILLING_RESERVATION_MINUTES=10080`，即先预留接口允许的最坏
  7 天，再在完成时用同一笔 reservation 按 Speechmatics 返回的真实时长
  原子结算并退回差额。free/pro 租户若无法覆盖该最坏配额会收到 402；
  这是安全拒绝。显式调低该变量可改善可用性，但意味着恶意伪装长音频
  可能让上游成本超过预留。

### OpenAI 兼容接口

```dotenv
OPENAI_API_KEY=
OPENAI_API_BASE=https://api.openai.com/v1
OPENAI_MODEL=gpt-5.6-sol
OPENAI_EMBEDDING_MODEL=text-embedding-3-small
OPENAI_FALLBACK_MODELS=gpt-5-mini,gpt-5-nano
OPENAI_DEBUG=0
```

未设置 `OPENAI_API_KEY` 时，RAG 会明确关闭，转录本身仍可使用。自定义
API Base 只允许 HTTP(S)，服务端请求有超时、响应体上限和安全错误处理。

### 数据库和本地数据

```dotenv
# 仅在不使用 Compose、直接运行 Go 服务时设置
DATABASE_URL=postgres://user:password@127.0.0.1:5432/dreamtrans?sslmode=disable

RAG_DB_PATH=./data/rag.db
# SQLite 总磁盘预算（MiB，含主库和 sidecar）；仅在明确需要时用 -1 关闭
RAG_MAX_DB_MB=102400
DREAMTRANS_CONFIG_PATH=./data/dreamtrans.config.json
PORT=8080
```

Compose 会根据 `POSTGRES_*` 自动构造容器内的 `DATABASE_URL`。不要把
数据库端口暴露到公网。租户的 `storage_quota_gb` 在每次云端转录写入的
数据库事务内强制执行，按说话人、原文和译文的 UTF-8 字节数计量；
`RAG_MAX_DB_MB` 是共享 SQLite 向量库的近似硬总预算：有限预算会扣除
1 MiB SHM 余量及总量 1/16（最少 4、最多 64 MiB）的 WAL 份额，余额才
用于每个连接的 `max_page_count`。WAL 会自动 checkpoint，并在启动、关闭
或超预算时截断；无法清理时拒绝后续写入，单次序列化写入上限为 16 MiB。
SQLite 无法对进行中事务的 WAL 提供字节硬限制，因此瞬时最坏情况是预算再
加一个事务生成的 WAL；病态全库页改写或外部 SQLite 写入可接近约 2 倍配置
值（另加少量 frame 元数据）。部署时仍需保留应急磁盘余量。`-1` 关闭总量
限制，但不关闭 WAL 清理和单次写入保护。

### API 鉴权和注册

```dotenv
DREAMTRANS_API_KEY=
DREAMTRANS_ADMIN_API_KEY=
ALLOW_ANONYMOUS_API=false
ALLOW_WEBSOCKET_QUERY_TOKEN=false
API_RATE_LIMIT_PER_MINUTE=120
WEBSOCKET_MAX_CONNECTIONS=256
WEBSOCKET_MAX_CONNECTIONS_PER_PRINCIPAL=4

REGISTRATION_ENABLED=false
REGISTRATION_INVITE_CODE=
CORS_ALLOWED_ORIGINS=
ALLOW_USER_API_KEY=false
```

- 浏览器统一工作台的 `/pro` 登录入口使用 JWT。
- WebSocket JWT 默认通过 `Sec-WebSocket-Protocol: dreamtrans.jwt.<JWT>` 传输。
  URL 中的 `?token=` 会进入代理日志，因此默认拒绝；仅迁移旧客户端时可临时
  设置 `ALLOW_WEBSOCKET_QUERY_TOKEN=true`。
- 服务调用在 `X-DreamTrans-API-Key` 中发送 `DREAMTRANS_API_KEY`。
- 管理服务密钥必须与普通服务密钥不同。
- 匿名 provider API 和自助注册默认关闭。
- 对公网启用注册时建议同时配置邀请码。
- `CORS_ALLOWED_ORIGINS` 是逗号分隔的完整 Origin；同源访问不需要 CORS。
- `ALLOW_USER_API_KEY=false` 时，普通用户不能绕过服务端托管的 API 密钥。
- `WEBSOCKET_MAX_CONNECTIONS` 是翻译和 Speechmatics 两类长连接共享的
  进程总上限；`WEBSOCKET_MAX_CONNECTIONS_PER_PRINCIPAL` 限制单个用户或
  服务调用方。达到上限时在升级前返回 `429`，防止长期连接耗尽文件描述符、
  SQLite 句柄或上游连接。
- 租户 `api_quota_monthly` 对会触发 Speechmatics/OpenAI 工作的入口按
  UTC 自然月原子计数，`-1` 表示不限量。HTTP 请求在进入 provider 工作前
  计数；长连接则对每次翻译、摘要、embedding 或 Speechmatics 识别会话计数。
  计数通过后发生的 provider 失败仍会占一个请求额。

### 健康检查与关闭

- `GET`/`HEAD /healthz`：进程存活，不访问外部 API。
- `GET`/`HEAD /readyz`：数据库模式下执行有 2 秒上限的 DB ping。
- Compose 等待 `/readyz` 健康后才报告服务可用。
- 服务最多用 20 秒排空请求和 WebSocket，Compose 提供 30 秒停止窗口。

## 前端构建变量

Vite 变量必须以 `VITE_` 开头，并在构建时注入：

```dotenv
VITE_BACKEND_URL=/
VITE_BACKEND_WS_URL=/
VITE_SPEECHMATICS_OPERATING_POINT=enhanced
```

生产镜像默认使用 `/`，即 API、WebSocket 和页面同源。分离域名构建：

```bash
docker build \
  --build-arg VITE_BACKEND_URL=https://api.example.com \
  --build-arg VITE_BACKEND_WS_URL=wss://api.example.com \
  -t dreamtrans .
```

本地开发可在 `frontend/.env` 中使用
`http://127.0.0.1:8080`/`ws://127.0.0.1:8080`。

## PCAS Event Worker

```dotenv
PCAS_ADDR=127.0.0.1:50051
PCAS_INSECURE=false
PCAS_CA_CERT=
PCAS_TLS_SERVER_NAME=
PCAS_API_KEY=
AUDIO_URL_ALLOW_HTTP=false
```

非回环 PCAS 地址默认使用 TLS。Worker 不会通过远程明文连接发送
`PCAS_API_KEY`。远程 `audio_url` 默认只允许 HTTPS，并始终拒绝私网、
回环、link-local、云元数据和其他特殊 IP。

## PCAS Streaming Provider

```dotenv
GRPC_BIND_ADDR=127.0.0.1
GRPC_PORT=50052
PCAS_API_KEY=
PCAS_TLS_CERT=
PCAS_TLS_KEY=
PCAS_ALLOW_INSECURE_REMOTE=false
PCAS_MAX_CONCURRENT_STREAMS=32
PCAS_ENABLE_REFLECTION=false
```

非回环监听必须使用至少 16 字符的独立服务密钥，并默认要求证书/私钥
成对配置。并发范围为 1–1024；reflection 默认关闭。

## 直接运行源码

后端与当前统一前端应分别启动；浏览器访问 Vite 地址（默认
`http://localhost:5173`），不要把 `backend/public` 中的历史发布快照当成
当前前端源码：

终端 1：

```bash
cd backend
go run ./cmd/web
```

终端 2：

```bash
cd frontend
npm ci
npm run dev
```

只在本机匿名兼容开发中使用以下后端启动方式：

```bash
SM_API_KEY=... ALLOW_ANONYMOUS_API=true go run ./cmd/web
```

对网络开放时应使用 PostgreSQL/JWT 或独立服务密钥，不要启用匿名模式。
