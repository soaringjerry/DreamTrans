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
- Translate 卡片：显示 P50/P95/P99（基于翻译事件独立缓冲，避免混合事件稀释）
- Live Metrics：转写/翻译/聊天的实时事件流，以彩条显示相对时长
- Lexicon（二级标签）：本地词/术语频率（实时增量），支持筛选（全部/未掌握/学习清单）、停用词、搜索、AI 释义、CSV 导出
- 垃圾值过滤：丢弃明显异常的时长（例如 > 5 分钟）
 - API Metrics（二级菜单）：
   - Requests & Tokens：总体与 chat/translate/summarize 的请求与 P/C/T 用量
   - 按模型分布：展示 gpt‑5 / gpt‑5‑mini 等模型的请求与 tokens 占比
   - 最近调用日志：时间、功能、模型、tokens、latency_ms（最近 20 条），便于核对是否出现“多模型并发调用”

## 注意
- tokens 信息严格以 OpenAI usage 为准（不做本地估算）；若服务端未返回，则隐藏 tokens 避免误导。
- Transcript 延迟基于“会话起始时间 + 片段 end_time”换算为墙钟时间；修复了早期 epoch 差值导致的超大分钟数显示问题。
 - 若启用 Responses API 提示缓存（OPENAI_USE_RESPONSES=1 且 OPENAI_PROMPT_CACHE=1），可在日志中观察请求变化；缓存由供应商侧决定计费与 TTL。
