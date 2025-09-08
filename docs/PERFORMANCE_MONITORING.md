# 性能监控（Tokens/Latency/Model）

## 后端
- `/api/rag/ask` 响应包含：
  - `usage.prompt_tokens` / `usage.completion_tokens` / `usage.total_tokens`
  - `usage.model`
  - `latency_ms`（从发起到返回的端到端时延）

## 前端
- 学习助手（RAG）每条回答下会显示：`model XXX · tokens P/C(T) · latency 12.34 s`（单位自动换算）
- 右下角 Performance 浮窗提供“全链路”可视化：
  - Summary 卡片：Transcript Avg / Translation Avg / Chat Avg · Tokens，顶部条形迷你图（最近 24 个事件）
  - Live Metrics：转写/翻译/聊天的实时事件流，以彩条显示相对时长
  - 垃圾值过滤：丢弃明显异常的时长（例如 > 5 分钟）

## 注意
- tokens 信息严格以 OpenAI usage 为准（不做本地估算）；若服务端未返回，则隐藏 tokens 避免误导。
- Transcript 延迟基于“会话起始时间 + 片段 end_time”换算为墙钟时间；修复了早期 epoch 差值导致的超大分钟数显示问题。
