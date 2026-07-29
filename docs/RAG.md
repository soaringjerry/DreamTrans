# RAG 学习助手（独立版）

本功能为独立版（standalone）后端提供自动摘要 + 向量检索（RAG）与问答能力：
- 实时转写片段在后端按“句子→段落”聚合后，自动进行摘要（减少碎片）并写入本地向量库
- 聊天提问时，将检索会话最近的高相关段落 + 会话摘要，构建上下文交给 LLM 生成答案

## 架构
- 存储：`modernc.org/sqlite` 纯 Go SQLite，默认持久化在 `RAG_DB_PATH`（容器为 `/app/data/rag.db`）
- 嵌入：OpenAI 兼容 Embeddings API（默认 `text-embedding-3-small`）
- 摘要和回答：OpenAI 兼容 Chat Completions（默认 `gpt-5-chat-latest`）
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
OPENAI_MODEL=gpt-5-chat-latest
OPENAI_EMBEDDING_MODEL=text-embedding-3-small
RAG_DB_PATH=./rag.db  # 容器内已默认 /app/data/rag.db
RAG_MAX_DB_MB=102400  # SQLite 总磁盘预算（MiB，含主库和 sidecar）；-1 关闭总量限制
```

有限的 `RAG_MAX_DB_MB` 会拆成主数据库、WAL/journal 和 1 MiB SHM
余量；WAL 份额为总预算的 1/16，并限制在 4–64 MiB。例如 64 MiB 配置会
分成 59 MiB 主库、4 MiB WAL 和 1 MiB SHM。主库的 `max_page_count`、
`foreign_keys`、`busy_timeout` 及 WAL 参数都通过 DSN 应用于每个新连接，
连接重建或服务重启不会丢失。

WAL 在约半份额时自动 checkpoint；启动、正常关闭以及 WAL 超出份额时会
执行 `TRUNCATE` checkpoint，超额 WAL 无法清理时后续写入会安全失败。
SQLite 的 `journal_size_limit` 只限制 checkpoint 后保留的 sidecar，不是
事务进行中的字节硬上限。本进程会串行化 RAG 写入且拒绝超过 16 MiB 的单次
序列化写入，所以正常稳态总量不超过配置值，瞬时最坏情况是“配置预算 +
一个进行中的事务所生成的 WAL”。病态的全库页改写或不受本进程控制的外部
SQLite 写入可令瞬时占用接近约 2 倍配置值（另加少量 WAL frame 元数据），
因此这是一项近似硬总预算，磁盘仍应留应急余量。设置 `-1` 会关闭主库总量
限制，但 WAL 清理和单次写入保护仍然生效。

旧版本曾把全部 `RAG_MAX_DB_MB` 都分配给主文件。如果升级时主文件已经超过
新拆分后的主库份额，服务会安全拒绝打开并提示增大 `RAG_MAX_DB_MB`；它不会
自动删除已有向量数据。

## 接口
- `POST /api/rag/ask`
  - 请求：
    - 最简：`{ "session_id": "current_session", "query": "今天老师讲了什么？", "top_k": 5 }`
    - 可选覆盖（前端设置面板会使用）：
      ```json
      {
        "session_id": "current_session",
        "query": "……",
        "top_k": 5,
        "config": {
          "api_key": "<可选-自定义key>",
          "api_base": "https://api.openai.com/v1",
          "model": "gpt-5",
          "prompt": "请用简洁中文、分点列出要点。"
        }
      }
      ```
  - 响应：`{ "answer": "..." }`

- `POST /api/rag/query`（调试用）
  - 请求：`{ "session_id": "current_session", "query": "topic", "top_k": 5, "candidate": 300 }`
  - 响应：`{ "summary": "会话摘要...", "docs": [{ id, speaker, start_time, end_time, original_text, summary }] }`

- `GET /api/rag/stats?session_id=...&limit=50`

## 前端使用
- 在 `src/App.tsx` 中集成“学习助手（RAG）”面板（默认 `session_id='current_session'`）
- 右上角“设置”按钮可打开设置浮窗：
  - API Base（默认 `https://api.openai.com/v1`）
  - Model（默认 `gpt-5`）
  - Prompt（展示默认提示，可编辑）
  - API Key（可选，不会展示默认后端 Key；仅本地 localStorage 保存）
  - 保存后，聊天请求会带上这些覆盖参数，仅作用于当前浏览器端
- WebSocket 初始化附带 `session_id`，服务端据此对段落进行“先摘要后向量”的自动入库

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
- 覆盖 API Key 仅在浏览器本地保存（localStorage），不会展示后端默认 Key；清空后将回退使用后端配置
- 回答采取结构化格式（分点/换行），前端以 `white-space: pre-wrap` 保留换行
