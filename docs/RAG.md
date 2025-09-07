# RAG 学习助手（独立版）

本功能为独立版（standalone）后端提供自动摘要 + 向量检索（RAG）与问答能力：
- 实时转写片段在后端按“句子→段落”聚合后，自动进行摘要（减少碎片）并写入本地向量库
- 聊天提问时，将检索会话最近的高相关段落 + 会话摘要，构建上下文交给 LLM 生成答案

## 架构
- 存储：`modernc.org/sqlite` 纯 Go SQLite，默认持久化在 `RAG_DB_PATH`（容器为 `/app/data/rag.db`）
- 嵌入：OpenAI 兼容 Embeddings API（默认 `text-embedding-3-small`）
- 摘要和回答：OpenAI 兼容 Chat Completions（默认 `gpt-4o-mini`）
- 入口：
  - WebSocket `/ws/translate` 在“段落 flush”时会自动触发摘要+入库（即使未启用 AI 翻译也入库）
  - REST `POST /api/rag/ask` 进行问答

## 环境变量
```bash
# 必需
OPENAI_API_KEY=your_key
SM_API_KEY=your_speechmatics_key  # 原有转写所需

# 可选
OPENAI_API_BASE=https://api.openai.com/v1
OPENAI_MODEL=gpt-4o-mini
OPENAI_EMBEDDING_MODEL=text-embedding-3-small
RAG_DB_PATH=./rag.db  # 容器内已默认 /app/data/rag.db
```

## 接口
- `POST /api/rag/ask`
  - 请求：`{ "session_id": "current_session", "query": "今天老师讲了什么？", "top_k": 5 }`
  - 响应：`{ "answer": "..." }`

- `POST /api/rag/query`（调试用）
  - 请求：`{ "session_id": "current_session", "query": "topic", "top_k": 5, "candidate": 300 }`
  - 响应：`{ "summary": "会话摘要...", "docs": [{ id, speaker, start_time, end_time, original_text, summary }] }`

- `GET /api/rag/stats?session_id=...&limit=50`

## 前端使用
- 在 `src/App.tsx` 里新增了“学习助手（RAG）”面板，使用 `session_id='current_session'`
- WebSocket 初始化时会附带 `session_id`，服务端据此将段落摘要入库

## 数据流细节
1. 前端接收到最终转写片段后，按句子聚合并再按段落打包
2. 服务端在段落 flush 时：
   - 使用 LLM 对该段落“先进行摘要”
   - 更新会话级摘要（压缩上下文）
   - 对该段落摘要进行向量化并入库（避免碎片与噪音）
3. 提问时：对 query 进行向量化，与最近文档计算余弦相似度取 TopK，再连同会话摘要作为上下文回答

## 注意
- 生产环境请配置 HTTPS/WSS 与受限 CORS
- OpenAI 兼容后端（Azure/OpenRouter 等）也可通过 `OPENAI_API_BASE` 使用
- 如需外部向量库（Qdrant/PGVector），可替换 `internal/rag/store.go`

