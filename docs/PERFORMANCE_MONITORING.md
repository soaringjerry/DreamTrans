# 性能监控（Tokens/Latency/Model）

## 后端
- `/api/rag/ask` 响应包含：
  - `usage.prompt_tokens` / `usage.completion_tokens` / `usage.total_tokens`
  - `usage.model`
  - `latency_ms`（从发起到返回的端到端时延）

## 前端
- 学习助手（RAG）每条回答下会显示：`model XXX · tokens P/C(T) · latency XXXms`
- 可扩展到翻译 WS：可在服务端按段落返回 Info 包含 tokens/latency，前端在翻译项下显示。

## 注意
- tokens 信息为 OpenAI 兼容接口返回的 usage 字段，若后端不返回则该行隐藏。
- latency 仅针对本次问答（RAG）；翻译延迟通常受 WS/网络/ASR 流程影响。
