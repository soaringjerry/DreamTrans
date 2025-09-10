# 环境变量配置指南

## 前端环境变量（Vite）

前端使用 Vite，环境变量必须以 `VITE_` 开头，并且在**构建时**注入。

### 开发环境设置

创建 `frontend/.env` 文件：
```bash
# 后端 API 地址
VITE_BACKEND_URL=http://localhost:8080
VITE_BACKEND_WS_URL=ws://localhost:8080

# Speechmatics 配置
VITE_SPEECHMATICS_OPERATING_POINT=enhanced
# VITE_SPEECHMATICS_MAX_DELAY=2.0
```

### 生产环境设置

#### 方式 1：Docker 构建时设置（推荐）

```bash
# 构建时指定后端地址
docker build \
  --build-arg VITE_BACKEND_URL=https://api.example.com \
  --build-arg VITE_BACKEND_WS_URL=wss://api.example.com \
  -t dreamtrans .

# 或者使用默认值（同源部署）
docker build -t dreamtrans .
```

默认情况下，生产环境使用相对路径（`/`），这意味着前端会使用相同的域名访问后端。

#### 方式 2：Docker Compose

```yaml
version: '3.8'
services:
  dreamtrans:
    build:
      context: .
      args:
        VITE_BACKEND_URL: /
        VITE_BACKEND_WS_URL: /
    environment:
      SM_API_KEY: ${SM_API_KEY}
    ports:
      - "8080:8080"
```

#### 方式 3：GitHub Actions

在 `.github/workflows/docker-build.yml` 中已配置默认使用相对路径。

## 后端环境变量

后端环境变量在**运行时**设置：

### 必需的环境变量

```bash
# Speechmatics API Key
SM_API_KEY=your_speechmatics_api_key

# OpenAI-兼容 API Key（RAG 摘要/问答/向量化）
OPENAI_API_KEY=your_openai_api_key
```

### 可选的环境变量

```bash
# 服务端口（默认 8080）
PORT=8080

# OpenAI 兼容设置
OPENAI_API_BASE=https://api.openai.com/v1
OPENAI_MODEL=gpt-5
OPENAI_EMBEDDING_MODEL=text-embedding-3-small

# 回退模型仅限 GPT‑5 家族（默认：gpt-5-mini,gpt-5-nano）
# 不会回退到 gpt-4 系列
OPENAI_FALLBACK_MODELS="gpt-5-mini,gpt-5-nano"

# 调试日志（可选，打印实际命中的模型等，生产建议关闭）
OPENAI_DEBUG=0

# RAG 数据库路径（容器默认 /app/data/rag.db）
RAG_DB_PATH=./rag.db

# 可选：内置词典 SQLite 路径（存在时启用 /api/dict* 接口；默认 /app/data/dict.db）
DICT_DB_PATH=/app/data/dict.db
```

### 运行时设置

#### Docker 运行
```bash
docker run -d \
  --name dreamtrans \
  -p 8080:8080 \
  -e SM_API_KEY=your_api_key \
  ghcr.io/soaringjerry/dreamtrans:latest
```

#### 使用 .env 文件
```bash
# 创建 backend/.env
SM_API_KEY=your_api_key

# 运行
cd backend && go run main.go
```

## 常见部署场景

### 1. 同源部署（前后端同一域名）
这是默认配置，无需修改：
- 前端：`https://app.example.com`
- 后端 API：`https://app.example.com/api/*`
- WebSocket：`wss://app.example.com/ws/*`

### 2. 分离部署（不同域名）
构建时指定后端地址：
```bash
docker build \
  --build-arg VITE_BACKEND_URL=https://api.example.com \
  --build-arg VITE_BACKEND_WS_URL=wss://api.example.com \
  -t dreamtrans .
```

### 3. 本地开发
使用 `.env` 文件配置不同的后端地址。

## 注意事项

1. **Vite 环境变量**必须在构建时设置，不能在运行时更改
2. **后端环境变量**可以在运行时通过 Docker 或系统环境变量设置
3. 生产环境建议使用 HTTPS/WSS 协议
4. 不要在代码中硬编码敏感信息
