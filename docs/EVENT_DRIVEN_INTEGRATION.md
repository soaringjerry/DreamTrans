---
title: Event‑Driven Transcription D‑App Integration
version: 0.1.0
---

# Event‑Driven Transcription D‑App Integration

本指南说明如何以“事件驱动”方式让 DreamTrans 作为 D‑App 接入 PCAS 事件总线：D‑App 作为 gRPC 客户端连接 PCAS（默认 `127.0.0.1:50051`），订阅请求事件、执行转写，再发布响应事件。

## 关键要点
- 连接：`pcas.bus.v1.EventBusService`（回环地址默认明文，远程默认 TLS）
- 订阅：`Subscribe(SubscribeRequest)` 获取事件流
- 发布：`Publish(Event)` 回写响应/业务事件
- 可选：`Search(SearchRequest)` 做语义检索
- 无需暴露端口：D‑App 不启动 gRPC 服务器，也不监听 `50051`

## 事件类型（建议）
- 请求：`capability.audio.transcribe.request.v1`
- 响应：`capability.audio.transcribe.response.v1`
- 错误：`capability.audio.transcribe.error.v1`

请求 `data` 约定（其一）：
```json
{ "audio_base64": "<base64-bytes>", "language": "en", "format": "wav", "sample_rate": 16000 }
```
或：
```json
{ "audio_url": "https://.../sample.wav", "language": "en" }
```

## 最小运行

环境变量：
- `PCAS_ADDR`：PCAS 地址，默认 `127.0.0.1:50051`
- `SM_API_KEY`：Speechmatics API Key
- `PCAS_API_KEY`：可选 Bearer 服务密钥
- `PCAS_CA_CERT`：可选的私有 CA 文件路径
- `PCAS_TLS_SERVER_NAME`：可选的服务端证书名覆盖
- `PCAS_INSECURE`：设为 `true` 才允许远程明文；远程明文不会发送密钥
- `AUDIO_URL_ALLOW_HTTP`：设为 `true` 才允许非 HTTPS 音频 URL

本地构建/运行：
```bash
cd backend
SM_API_KEY=xxx PCAS_ADDR=127.0.0.1:50051 go run ./cmd/event-worker
```

Docker（事件模式默认）：
```bash
# 事件 Worker 专用镜像
docker build -f backend/Dockerfile.event -t dreamtrans-event .
docker run --rm \
  -e SM_API_KEY=xxx \
  -e PCAS_ADDR=pcas.example.com:50051 \
  -e PCAS_API_KEY=独立服务密钥 \
  -e PCAS_CA_CERT=/run/secrets/pcas-ca.crt \
  -v "$PWD/pcas-ca.crt:/run/secrets/pcas-ca.crt:ro" \
  dreamtrans-event

# 或通用 Dockerfile.pcas（默认 MODE=event）
docker build -f backend/Dockerfile.pcas --build-arg MODE=event -t dreamtrans .
docker run --rm \
  -e SM_API_KEY=xxx \
  -e PCAS_ADDR=pcas.example.com:50051 \
  -e PCAS_API_KEY=独立服务密钥 \
  dreamtrans
```

## 处理流程
1. 订阅事件流后，过滤 `capability.audio.transcribe.request.v1`
2. 从 `data` 读取 `audio_base64` 或下载 `audio_url`
3. 调用 Speechmatics 批量转写 API（仓库已有 `internal/speechmatics` 客户端）
4. 以 `capability.audio.transcribe.response.v1` 发布结果，设置：
   - `correlation_id = 请求 id`
   - `trace_id = 透传请求 trace_id`
   - `source = dapp.dreamtrans`

## 注意
- 大音频建议走 `audio_url`
- `audio_url` 默认必须使用 HTTPS；即使显式启用 HTTP，Worker 也会拒绝
  私网、回环、link-local、云元数据和其他特殊地址
- 远程 PCAS 默认验证系统 CA 与 `PCAS_ADDR` 主机名；私有 PKI 使用
  `PCAS_CA_CERT`，需要时通过 `PCAS_TLS_SERVER_NAME` 覆盖证书名
- 不要依赖 `PCAS_INSECURE=true` 对外部署；Worker 不会在远程明文中
  发送 `PCAS_API_KEY`
- 交付语义：建议在消费方提炼知识，再以业务事件发布（与能力解耦）
