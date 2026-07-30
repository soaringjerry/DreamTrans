# RAG 学习助手

DreamTrans 的统一 UI 提供基于当前会话的问答、会话摘要和可选的向量检索。
它和实时转录是两条明确分开的费用路径：

- Speechmatics 转录可以独立工作；
- AI 对话只在用户主动发送问题时调用模型；
- “自动 AI 入库”默认关闭。只有用户打开它后，最终转录才会自动发送到
  `/api/rag/ingest` 做摘要记录和向量化。

因此，默认状态下长时间录音不会静默触发摘要/Embeddings 请求。主动提问仍会
产生一次 Chat 请求，并由前端附带当前会话的近期文本。

## 统一 UI 中的数据流

启用自动入库后：

1. 前端只处理 Speechmatics 返回的**最终**转录，不发送反复变化的 partial。
2. 片段进入有界客户端队列，逐条调用 `POST /api/rag/ingest`；每次请求有超时，
   失败不会阻塞录音和 IndexedDB 的本地保存。
3. 后端清理文本、去重、更新会话级摘要，并计算/保存 Embedding。
4. 用户提问时，`POST /api/rag/ask` 检索当前身份作用域内该会话的相关文档，
   合并摘要与近期上下文后调用 Chat 模型。
5. “会话摘要”标签通过 `GET /api/rag/summary` 读取已经保存的摘要；这个读取
   本身不会重新扫描浏览器转录。

当前 HTTP ingestion 路径默认把清理后的段落直接加入运行摘要，不额外调用
LLM 压缩每个段落，但 Embedding 仍可能产生模型用量。后端的其他实时/headless
路径可按其会话配置启用 LLM 段落压缩；不要把这一能力理解成统一 UI 默认行为。

## 身份与配置覆盖

RAG 接口先经过全局 API 鉴权。在启用 PostgreSQL 计费/配额的部署中，RAG
还必须具有带 tenant/user 的 JWT，单独使用服务 Key 会被拒绝；独立部署没有
计费组件时，可以使用服务 Key，或由服务器显式开启匿名 API。后端会把
`session_id` 绑定到 JWT 用户或匿名作用域，同一个浏览器里切换账号不会让
新账号读取前一个账号的 RAG 会话。

统一 UI 始终允许修改 AI 回答提示词。只有管理员启用
`ALLOW_USER_API_KEY=true` 后，界面才显示自带 API Key、API Base 和 Model：

- Key 只保存在当前标签页的 `sessionStorage`/内存中，关闭标签页或退出登录
  即清除；
- Base、Model 和提示词等非密钥偏好保存在当前浏览器的 `localStorage`；
- Key 不会回显服务器默认值；
- Base/Model 只有在填写了自带 Key 时随主动问答请求发送；
- 服务端仍会验证是否允许请求级覆盖，不能仅靠手工构造前端请求绕过策略。

## 存储与模型配置

- 存储：`modernc.org/sqlite`，默认位于 `RAG_DB_PATH`（容器中为
  `/app/data/rag.db`）。
- Embeddings：OpenAI 兼容 Embeddings API，默认
  `text-embedding-3-small`。
- Chat：OpenAI 兼容 Chat Completions，默认 `gpt-5.6-sol`。

```bash
# 配置 AI/RAG 时需要
OPENAI_API_KEY=your_key

# 实时转录独立需要
SM_API_KEY=your_speechmatics_key

# 可选
OPENAI_API_BASE=https://api.openai.com/v1
OPENAI_MODEL=gpt-5.6-sol
OPENAI_EMBEDDING_MODEL=text-embedding-3-small
RAG_DB_PATH=./rag.db
RAG_MAX_DB_MB=102400
```

`RAG_MAX_DB_MB=-1` 关闭 SQLite 总量限制。设置有限值时，预算会拆分给主库、
WAL/journal 与 1 MiB SHM 余量；WAL 份额是总预算的 1/16，并限制在
4–64 MiB。例如 64 MiB 配置约分为 59 MiB 主库、4 MiB WAL 与 1 MiB SHM。

主库的 `max_page_count`、`foreign_keys`、`busy_timeout` 和 WAL 参数通过
DSN 应用于每个连接。WAL 在约半份额时 checkpoint；启动、正常关闭以及 WAL
超额时执行 `TRUNCATE` checkpoint。单次序列化写入超过 16 MiB 会被拒绝。
这是近似硬总预算：正在执行的大事务仍可短暂生成额外 WAL，磁盘必须保留
应急余量。

旧版本曾把全部 `RAG_MAX_DB_MB` 分配给主文件。如果升级时主文件已超过新的
主库份额，服务会拒绝打开并提示增大预算，不会自动删除已有向量数据。

## 接口

### `POST /api/rag/ingest`

统一 UI 的可选自动入库接口：

```json
{
  "session_id": "session-id",
  "speaker": "S1",
  "text": "confirmed transcript",
  "start_time": 12.4,
  "end_time": 16.8
}
```

成功时返回 `{"status":"ok"}`；空文本、重复或不可向量化内容可能返回
`{"status":"skipped","reason":"..."}`。

### `POST /api/rag/ask`

最简请求：

```json
{
  "session_id": "session-id",
  "query": "刚才讨论了哪些行动项？",
  "top_k": 5
}
```

允许自带配置时可以附加：

```json
{
  "session_id": "session-id",
  "query": "请总结风险",
  "top_k": 5,
  "config": {
    "api_key": "<browser-local-key>",
    "api_base": "https://api.openai.com/v1",
    "model": "gpt-5",
    "prompt": "请用简洁中文回答。"
  }
}
```

响应包含 `answer`，并在上游提供时包含 `usage` 和 `latency_ms`。前端只显示
服务端实际返回的 Tokens，不进行本地猜测。

### 读取与诊断

- `GET /api/rag/summary?session_id=...`：读取运行摘要。
- `GET /api/rag/title?session_id=...`：读取或生成缓存标题。
- `POST /api/rag/query`：诊断检索结果，可设置 `top_k` 与 `candidate`。
- `GET /api/rag/stats?session_id=...&limit=50`：读取最近文档统计。

## 注意事项

- 生产环境应使用 HTTPS/WSS、受限 CORS 和明确的认证配置。
- OpenAI 兼容服务可以通过 `OPENAI_API_BASE` 使用；实际兼容程度取决于服务商。
- 自动入库关闭后，新转录不会进入向量库；此前已经保存的摘要/向量不会被
  自动删除。
- 浏览器的云端转录 outbox 和 RAG ingestion 队列用途不同：前者持久保存待
  同步转录，后者是有界、尽力而为且不跨刷新持久化的 AI 工作队列。网络长时间
  不可用时不能把 RAG 队列当作完整归档；AI 服务失败不会影响本地会话完整性。
- 如需 Qdrant/PGVector，可替换 `backend/internal/rag/store.go`。
