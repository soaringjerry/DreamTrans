# Docker 动态配置指南

## 问题说明

Vite 环境变量（`VITE_BACKEND_URL` 等）是在构建时注入的，无法在 Docker 运行时通过 `-e` 参数修改。

## 解决方案

### 方案 1：使用默认同源配置（推荐）

默认配置已经支持同源部署，无需额外配置：

```bash
# 直接运行，前端会使用相对路径
docker run -d \
  --name dreamtrans \
  -p 8080:8080 \
  -e SM_API_KEY=your_key \
  ghcr.io/soaringjerry/dreamtrans:latest
```

前端会自动连接到：
- API: `http://localhost:8080/api/*`
- WebSocket: `ws://localhost:8080/ws/*`

### 方案 2：构建时指定后端地址

如果前后端分离部署，在构建时指定：

```bash
# 构建自定义镜像
docker build \
  --build-arg VITE_BACKEND_URL=https://api.example.com \
  --build-arg VITE_BACKEND_WS_URL=wss://api.example.com \
  -t my-dreamtrans .

# 运行自定义镜像
docker run -d \
  -p 8080:8080 \
  -e SM_API_KEY=your_key \
  my-dreamtrans
```

### 方案 3：使用反向代理

使用 Nginx 或 Traefik 作为反向代理，将前后端统一到同一个域名下：

```nginx
server {
    listen 80;
    server_name app.example.com;

    # 前端静态文件
    location / {
        proxy_pass http://dreamtrans-frontend:80;
    }

    # 后端 API
    location /api/ {
        proxy_pass http://dreamtrans-backend:8080;
    }

    # WebSocket
    location /ws/ {
        proxy_pass http://dreamtrans-backend:8080;
        proxy_http_version 1.1;
        proxy_set_header Upgrade $http_upgrade;
        proxy_set_header Connection "upgrade";
    }
}
```

### 方案 4：运行时配置脚本（高级）

创建一个启动脚本，在容器启动时动态替换配置：

```bash
#!/bin/sh
# entrypoint.sh

# 如果提供了环境变量，替换前端文件中的占位符
if [ ! -z "$RUNTIME_BACKEND_URL" ]; then
  find /app/public -name "*.js" -exec sed -i "s|BACKEND_URL_PLACEHOLDER|$RUNTIME_BACKEND_URL|g" {} \;
fi

# 启动应用
./server
```

但这需要修改构建过程，不推荐。

## 最佳实践

1. **开发环境**：使用 `.env` 文件
2. **生产环境**：
   - 同源部署：使用默认配置
   - 分离部署：构建时指定
   - 多环境：为每个环境构建不同的镜像

## 常见场景

### 本地测试
```bash
# 前后端都在本地
docker run -d -p 8080:8080 -e SM_API_KEY=xxx ghcr.io/soaringjerry/dreamtrans:latest
```

### 云部署（同域名）
```bash
# 部署到 https://app.example.com
# 前端和后端使用相同域名，无需特殊配置
docker run -d -p 80:8080 -e SM_API_KEY=xxx ghcr.io/soaringjerry/dreamtrans:latest
```

### 云部署（分离）
```bash
# 前端: https://app.example.com
# 后端: https://api.example.com
# 需要重新构建镜像
docker build \
  --build-arg VITE_BACKEND_URL=https://api.example.com \
  --build-arg VITE_BACKEND_WS_URL=wss://api.example.com \
  -t dreamtrans-prod .
```