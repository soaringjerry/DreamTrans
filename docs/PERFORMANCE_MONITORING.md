# 会话洞察与 API 性能监控

统一 UI 的“会话洞察”把两类数据分开显示：

1. 当前浏览器会话的本地统计与词汇表；
2. 有权限时读取的服务端 OpenAI 兼容 API 用量和最近调用。

它不再伪造旧版前端的 ASR/翻译 P50、P95、P99 图。当前服务端指标中的延迟
只代表被后端记录的 Chat/LLM Translate/Summarize 调用，不等于 Speechmatics
端到端识别延迟、浏览器音频延迟或网络全链路延迟。

## 当前会话统计

洞察面板显示：

- 会话时长；
- 最终转录片段数；
- 识别出的说话人数；
- 本地写入与云端 outbox 的待处理数量；
- 已完成译文占最终转录的比例。

这些统计来自已经规范化的当前会话状态，不会为了刷新卡片而重新读取全部
IndexedDB 音频 Blob。

## 本地词汇表

词汇表在浏览器中对最终转录做增量统计，包括单词和双词组。当前分词器面向
英文单词/缩写（含撇号形式），不应把这组计数当成中文分词结果。它支持：

- 搜索；
- 停用词过滤；
- “全部 / 未掌握 / 学习清单”筛选；
- 把单词标记为已掌握或加入学习清单；
- CSV 导出；
- 把词或词组带入 AI 助手请求释义。

计数、筛选和 CSV 都是本地功能，不产生模型费用。“AI 释义”会打开助手，
只有实际发送问题时才产生一次显式 Chat 请求。词汇学习状态保存在当前浏览器。

## 单条 AI 回答

`POST /api/rag/ask` 在上游返回数据时包含：

```json
{
  "answer": "...",
  "usage": {
    "prompt_tokens": 100,
    "completion_tokens": 40,
    "total_tokens": 140,
    "model": "..."
  },
  "latency_ms": 1234
}
```

AI 助手把可用的模型、总 Tokens 和延迟显示在对应回答下方。若供应商没有返回
usage，前端隐藏 Tokens，不用字符数做估算。

## 服务端 API Metrics

`GET /api/metrics` 返回当前后端进程内的聚合快照：

- `overall`：总请求与 Prompt/Completion/Total Tokens；
- `chat`、`translate`、`summarize`：按功能汇总；
- 每类中的 `per_model`：按模型拆分；
- `last_logs`：最多 200 条最近调用，包含 UTC 时间、功能、模型、Tokens 和
  `latency_ms`。

当前 UI 展示总量、Chat/Translate/Summarize 分类和最近 20 条调用；接口中的
`per_model` 仍可供外部诊断工具做更细的模型分布分析。

该接口由 `RequireSuperAdmin` 保护。统一 UI 只有在当前身份有权限且请求成功时
才能展示服务端用量；普通用户仍可看到自己的会话统计、词汇表和单条 AI 回答
元数据。

这些计数是**进程级诊断指标**，不是按用户隔离的正式账单：

- 服务重启会清空；
- 多副本部署各自维护一份；
- `POST /api/metrics/reset` 会清空当前进程的计数与最近日志，同样仅限
  Super Admin；
- reset 不会修改 PostgreSQL 中的配额、计费或审计记录。

## 费用判断

- Speechmatics 转录/翻译不会出现在 OpenAI Tokens 统计中。
- “自动 AI 入库”默认关闭。关闭时，新最终转录不会自动请求摘要/Embedding；
  主动 Chat 仍会计入 `chat`。
- 开启自动入库后，Embedding 供应商用量由对应 RAG 计量/计费路径处理；当前
  `/api/metrics` 的 `overall` 主要汇总 Chat、LLM Translate 和 Summarize，
  不应当被当作所有供应商费用的完整账单。
- Recent logs 中 Tokens 为 `0` 可能表示供应商没有返回 usage，并不必然表示
  请求免费。

如果启用供应商侧 Responses API 提示缓存，缓存命中、TTL 和最终计费仍以该
供应商账单为准；本面板只展示 DreamTrans 实际收到并记录的数据。
