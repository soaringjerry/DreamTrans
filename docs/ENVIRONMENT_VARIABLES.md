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
      JWT_SIGNING_KEY: ${JWT_SIGNING_KEY}
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

# JWT 签名密钥（用于生成认证令牌）
JWT_SIGNING_KEY=your_secure_secret_key
```

### 可选的环境变量

```bash
# 服务端口（默认 8080）
PORT=8080
```

### 运行时设置

#### Docker 运行
```bash
docker run -d \
  --name dreamtrans \
  -p 8080:8080 \
  -e SM_API_KEY=your_api_key \
  -e JWT_SIGNING_KEY=your_secret \
  ghcr.io/soaringjerry/dreamtrans:latest
```

#### 使用 .env 文件
```bash
# 创建 backend/.env
SM_API_KEY=your_api_key
JWT_SIGNING_KEY=your_secret

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
4. JWT_SIGNING_KEY 应该是一个强随机字符串
5. 不要在代码中硬编码敏感信息