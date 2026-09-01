# AI 上下文、项目知识库与混合检索

DreamTrans 的聊天、上下文预览、摘要、笔记和行动项使用同一套上下文装配逻辑。
实时转录本身不依赖 OpenAI；用户主动发起 AI 请求、明确确认建立语义索引，或主动
启用 legacy 自动入库后，才会产生对应的模型调用和费用。

运行和测试目标仅限 Linux、WSL 和 Docker；生产服务器使用 Linux/Docker，WSL
用于本地开发和测试。认证后的项目知识库使用 PostgreSQL 16、`pgvector` 和
`pg_trgm`；SQLite RAG 与旧 `REAL[]` 向量只保留作匿名或迁移兼容路径。

## 上下文装配

`max_context_tokens` 是模型实际可读输入的总预算，不只是转录预算。以下内容在发送
给模型前会一起重新估算：

- 系统提示词；
- 聊天历史；
- 当前问题或生成指令；
- 会话转录；
- 项目知识块；
- 会话检索块及 legacy RAG 内容。

数据库仍保存 Speechmatics 的原子转录片段，便于精确同步和重放；进入 AI 上下文或
会话索引前，服务端会把同一说话人在相邻时间内的 micro-final 按句界、时长和长度
上限合并成可读段落，并排除尚未确认的 partial。这样不会修改原始转录，但可避免
模型看到“一词一行”的碎片化文本。

服务端输入预算上限由 `AI_MAX_CONTEXT_TOKENS` 控制，默认 `256000`。同时会从
`AI_MODEL_CONTEXT_WINDOW_TOKENS` 中扣除
`AI_CONTEXT_OUTPUT_RESERVE_TOKENS`（默认分别为 `260096` 和 `4096`）；两者计算出的
可用输入空间更小时，以更小值为准。请求没有指定策略时，会优先使用当前会话所关联
项目的默认策略；用户仍可在单次请求中覆盖。

三种策略的含义如下：

- `full`：保留完整转录和已经选中的知识块。总输入超限时返回 `422`，不会静默截断，
  也不会自动改成检索模式。`full` 指完整会话转录，不表示把整个项目知识库无条件
  塞入模型；项目知识仍先按问题选出相关块。
- `smart`：总输入放得下时等同 `full`；超限时优先保留按相关度排序的知识块，再从
  最新的完整转录片段向前填充。不会在片段中间随意切断，因此来源信息仍可追溯。
- `retrieval`：不发送整篇转录，只发送检索出的会话和项目知识块。

如果系统提示、历史和当前问题本身已经超过预算，任何模式都会拒绝请求。响应中的
`effective_mode`、`estimated_tokens`、`truncated` 和 `sources` 反映最终真正装配出的
输入，不是前端猜测值。

`POST /api/ai/context/preview` 接受与实际生成相同的 `question`、`history`、
`project_id`、`client_transcript`、`context_policy` 和 `retrieval_preference`；
还可传入 `artifact_type` 预览摘要、笔记或行动项的真实生成指令。默认
`execute_semantic: false`，这是自动 preflight 使用的免费词法预览：它不会暗中产生
查询 Embedding 费用，但正式请求使用 `auto` 时，语义索引已就绪的排序可能与预览
略有不同。

只有用户明确要求实际语义预览时才传 `execute_semantic: true`。此时后端会按
`retrieval_preference` 运行正式检索路径；若索引已就绪且首选项为 `auto`，会真实
调用查询 Embedding，并按实际用量计费。响应中的 `semantic_query_executed`、
`semantic_skipped`、`preview_retrieval_preference`、`retrieval_mode`、
`index_targets`、`truncated` 和 `preview_truncated` 用于区分检索与显示截断，不能
把付费语义预览当成无副作用的 UI preflight。

示例：

```json
{
  "session_id": "3ac18a24-2fae-4bb6-a238-cfdf920547af",
  "project_id": "5702f670-b0b1-43c4-a06c-7e13f0bc11f4",
  "question": "我们确认了哪些下一步？",
  "history": [
    {"role": "user", "content": "先只看本周的决定"}
  ],
  "client_transcript": [
    {
      "id": "segment-1",
      "speaker": "S1",
      "text": "周五前完成上线检查。",
      "start_time": 12.4,
      "end_time": 16.8
    }
  ],
  "context_policy": {"mode": "smart", "max_tokens": 64000},
  "retrieval_preference": "auto",
  "execute_semantic": false
}
```

## PostgreSQL 混合检索

迁移 `019_ai_knowledge_production.sql` 启用：

- `vector`：保存固定 `1536` 维语义向量，并使用 HNSW 余弦索引召回候选；
- `pg_trgm`：使用 GIN trigram 索引召回词法候选；
- RRF（Reciprocal Rank Fusion）：按两路排名合并、去重并返回 Top K。

默认 Embedding 模型为管理员批准的 `text-embedding-3-small`，请求始终显式指定
`dimensions: 1536`。兼容服务若返回其他维度，任务会失败，不会把错误向量写入索引。
模型配置变化后，旧向量标记为 `stale`；只有用户再次确认才会重建和收费。

上下文响应中的 `retrieval_mode` 可能为：

- `none`：这次装配没有使用检索块；
- `hybrid`：语义与词法候选都参与了 RRF；
- `semantic`：只有语义候选可用；
- `lexical_fallback`：只使用 PostgreSQL 词法结果，包括用户主动选择免费词法检索，
  或语义查询暂时不可用的回退；
- `legacy`：使用旧 `REAL[]` 或 SQLite RAG 兼容路径。

正式请求的 `retrieval_preference` 支持：

- `auto`：索引为 `ready` 时尝试一次查询 Embedding 并运行混合检索；查询 Embedding、
  模型或上游不可用时回退到词法检索；
- `lexical_only`：本次只运行免费词法检索，不调用 Embeddings，也不创建索引。

索引状态统一为 `unindexed`、`queued`、`processing`、`ready`、`stale` 和 `error`。
任务本身取消后还可能显示 `cancelled`。

## 首次索引确认与任务

索引是显式付费操作，不会因上传文件、升级数据库或普通词法搜索自动创建。

1. 调用 `POST /api/ai/index/preview`，查看模型、维度、总块数、
   `pending_chunks`、待处理块的估算 token、估算 DP、当前状态和短期有效的
   `confirmation_token`。
2. 向用户展示三个选择：
   - 确认建索引并继续；
   - 本次只使用 `lexical_only`；
   - 取消。
3. 只有确认后才调用 `POST /api/ai/index/jobs`，并同时发送
   `confirmed: true`、预览返回的 `confirmation_token` 和稳定的
   `client_request_id`。
4. 通过 `GET /api/ai/index/jobs/{id}` 恢复进度；失败或取消的任务可调用
   `POST /api/ai/index/jobs/{id}/retry`；`DELETE /api/ai/index/jobs/{id}` 请求取消。

预览请求：

```json
{
  "target_type": "project",
  "target_id": "5702f670-b0b1-43c4-a06c-7e13f0bc11f4"
}
```

预览响应中的 `estimated_tokens` 和 `estimated_dp` 只覆盖
`pending_chunks`，已经使用当前模型成功索引的块不会重复计入。确认令牌有效期为
10 分钟，并绑定租户、用户、目标、模型、维度、待处理块数量、估算费用和内容快照；
内容或模型变化后必须重新预览并确认。响应示例（令牌已缩短）：

```json
{
  "target_type": "project",
  "target_id": "5702f670-b0b1-43c4-a06c-7e13f0bc11f4",
  "model": "text-embedding-3-small",
  "dimensions": 1536,
  "chunk_count": 24,
  "indexed_chunks": 16,
  "pending_chunks": 8,
  "estimated_tokens": 14320,
  "estimated_dp": 0.03,
  "index_status": "stale",
  "requires_indexing": true,
  "confirmation_token": "<signed-preview-token>"
}
```

会话索引将 `target_type` 改为 `session` 并传入会话 UUID。创建任务示例：

```json
{
  "target_type": "project",
  "target_id": "5702f670-b0b1-43c4-a06c-7e13f0bc11f4",
  "confirmed": true,
  "confirmation_token": "<signed-preview-token>",
  "client_request_id": "index-project-5702f670-v1"
}
```

Embedding 每批最多 `64` 块，估算输入不超过 `100000` token；实际用量仍由现有计费
系统结算。固定 worker 池通过 PostgreSQL 租约领取任务，默认并发为
`AI_INDEX_WORKERS=2`。进程重启后，租约过期的任务会继续被领取，不受“只恢复前 100
个任务”之类的内存列表限制。

## 项目和会话关联

`GET /api/ai/projects?session_id=...` 返回：

```json
{
  "projects": [],
  "linked_project_id": null
}
```

每个会话最多关联一个项目。关联后，聊天、上下文预览和生成物请求没有显式传
`project_id` 时会恢复该项目，并采用项目的默认 `context_mode` 和
`max_context_tokens`。显式传入的单次上下文策略优先。

相关接口：

- `POST /api/ai/projects`：创建项目；
- `PATCH /api/ai/projects/{id}`：编辑名称、描述和默认上下文策略；
- `DELETE /api/ai/projects/{id}`：删除项目及其知识数据；
- `POST /api/ai/projects/{id}/sessions`：传入 `session_id` 建立或替换关联；
- `DELETE /api/ai/projects/{id}/sessions/{session_id}`：解除关联；
- `GET /api/ai/projects/{id}/sources`：列出知识源；
- `POST /api/ai/projects/{id}/sources`：创建显式记忆或上传文件；
- `PATCH /api/ai/projects/{id}/sources/{source_id}`：编辑显式记忆并重新分块；
- `POST /api/ai/projects/{id}/sources/{source_id}/retry`：重试失败的文件提取；
- `DELETE /api/ai/projects/{id}/sources/{source_id}`：删除知识源。

## 文件提取和 OCR

支持 `.txt`、`.md`、`.csv`、`.tsv`、`.json`、`.pdf`、`.docx`、`.pptx`（按幻灯
片顺序抽取正文和备注）、`.xlsx`、`.png`、`.jpg`、`.jpeg` 和 `.webp`。上传时会同时校验扩展名、声明的媒体类型和
文件内容特征；同一项目内重复文件返回 `409`。

Multipart 上传可重复提供 `ocr_language`，只接受：

- `eng`：英语；
- `chi_sim`：简体中文；
- `jpn`：日语；
- `kor`：韩语。

没有传入时，若上传关联了会话，则按该会话的源语言选择对应 OCR 语言；没有可用
会话语言时才回退为英语和简体中文。容器镜像已包含四种 Tesseract 数据。PDF
先运行 `pdftotext`；没有得到文本时才逐页渲染并 OCR。扫描 PDF 默认最多处理
`100` 页，超限会明确失败，不会只保留前 100 页。

默认防护限制：

- 原文件：`KNOWLEDGE_MAX_FILE_MB=50`；
- 提取文本：`KNOWLEDGE_MAX_EXTRACTED_MB=10`；
- DOCX/XLSX 压缩包总解压内容：`KNOWLEDGE_MAX_OFFICE_UNCOMPRESSED_MB=100`；
- 图片：`KNOWLEDGE_MAX_IMAGE_MEGAPIXELS=40`；
- 扫描 PDF：`KNOWLEDGE_MAX_PDF_PAGES=100`；
- 提取任务并发：`KNOWLEDGE_EXTRACT_WORKERS=2`。

提取文本、解压内容、图片像素、PDF 页数及外部命令输出均有硬边界。超过限制会把
知识源标记为 `error` 并给出原因，不会静默截断。文件任务同样使用 PostgreSQL 租约、
进度、最多三次尝试和重启恢复。

## 聊天与生成物

`POST /api/rag/ask`、`POST /api/ai/artifacts` 和上下文预览共享装配器。摘要、笔记和
行动项会读取所选项目的知识块，而不是只在生成物记录中保存 `project_id`。

生成物接口：

- `POST /api/ai/artifacts`：生成 `summary`、`notes` 或 `action_items`；
- `GET /api/ai/artifacts?session_id=...`：读取会话生成物；
- `DELETE /api/ai/artifacts/{id}`：删除生成物。

前端复制和 Markdown 导出在浏览器本地执行，不新增模型调用。

### 课程技能地图（学习模式）

- `POST /api/ai/projects/{id}/skill-map`：入队后台任务，立即返回
  `{job, artifact, map}`。Worker 通读课程已关联会话的全部转录和已上传、抽取完成
  的课程资料（教材、课件、论文、图片 OCR 文本，记为 `[资料 N]`，evidence 里以
  `source_id` 引用）；窗口放不下时按
  场、必要时按时间切段提炼再合并，**不会截掉后半节课**。各段并行提炼（
  `SKILL_MAP_CHUNK_CONCURRENCY`，默认 4，最多 8），单段坏 JSON 自动重问一次。请求体：
  `{client_request_id, reasoning_effort?, config?}`。同一 `client_request_id`
  且内容相同会复用任务；新的生成会取消该课上一份进行中的任务。
- `GET /api/ai/projects/{id}/skill-map`：读取最新地图，并附带进行中（或最近一次
  失败）的 `job`（`queued` / `processing` 时前端轮询）。没有地图时 `artifact`
  与 `map` 为 `null`。
- `DELETE /api/ai/projects/{id}/skill-map`：取消进行中的生成任务（已有地图不受
  影响），返回与 GET 相同的结构。正在跑的 worker 在下一次续租时发现并中止。
- 曾经的实验性 `concept-map` 端点已在迁移 026 中移除并清理历史数据。

### 学习空间练习循环（迁移 027 / 029）

一次作答同时推动三条通道：学科能力、Academic English、会话内 Combo。AI 只在
后台出题和评分；用户看到的是技能地图、练习、等级和导师。

- `GET /api/ai/projects/{id}/study/state`：每项能力的进度
  （`learner → supervised → hazard → independent → mastered`、XP、连击），以及
  `continue`（按技能地图先修推荐下一练哪项；已精通或被先修挡住的跳过）。
- `POST /api/ai/projects/{id}/study/next`：按当前等级选题（入门/辅助 → 难度 1，
  挑战 → 2，独立/精通 → 3）。请求可带 `practice_session_id`，同一练习会话内
  不重复已做过的题。题库为空或本会话把可用题都做过时，同步生成 3 题（难度
  1/2/3 各一条）先把题给出来；当前难度每题都用过 2 次以上时先出现有的题，
  同时在后台补 6 题（同一学生/课程/能力同时只补一次）。题目贴课堂摘录出情
  境，并要求 `c_anchor` / `d_anchor` 能分开 C 和 D。首次同时冻结该能力的
  F/P/C/D/HD rubric。响应含 `scaffold`（是否给中文 / 是否先展示中文 / 是否给提示）和一句
  `coach_line`（例如「下一题不给中文」）。
- `POST /api/ai/projects/{id}/study/attempts`：对照冻结 rubric 定级。响应必含
  等级、一句「差在哪」和一句「下一步」——等级从不单独出现。`first_try` 按**这
  道题**是否第一次作答计算，不是该能力人生第一题。Combo 按练习会话记账：无提
  示 ≥C 加一，提示通过保持，&lt;C 清零；倍率 2/3/5/8/12 → ×1.1/1.2/1.5/2/3 计入
  XP。`language_issue` 为真且未过时，不惩罚学科连击（先修语言）；看了中文仍错
  才按学科失败处理。
- 题型随难度走（`scenario.format`）：难度 1 用 `single` / `multi`（辨认常见误
  区），难度 2 用 `cloze` / `multi` / `open`，难度 3 只有 `open`（迁移）。选择
  题请求带 `choices`（选项下标）和可选 `reason`，填空题 `answer` 是填的词、
  `reason` 可选，问答题 `answer` 是回答。选择题由服务端判对错：选错封顶 P，选
  对没理由 = C，理由由 rubric 决定 D/HD；填空由评分模型判 `answer_correct`，错
  则封顶 P。标准答案不下发。
- 语言脚手架逐级撤退：每题带 `glossary`（题干术语 → 中文）和 `starters`（英文
  学术句式起手）。入门给两者，辅助只给术语表，挑战以上都不给；答错时术语表补
  回。批改另返回 `language_tip`（一句英文表达改法）。
- 脚手架随表现撤退：独立/精通默认不给中文和提示；英文题干失败会先探中文；中
  文也失败才给提示。精通只靠反复无提示迁移，不能刷提示。
- 特殊事件随批改返回（`events`）：`language_save`（英文错、看中文后过）、
  `self_correction`、`transfer_success`（难度 3 无提示通过）、`critical_insight`、
  `misconception_broken`（上次记下的错误模式这次消失且 ≥D）。加成
  `language_independence` 只发一次：曾靠中文救场的学生首次纯英文连过 3 题。
- 出题和评分走同一套生成管线（`client_request_id` 幂等 + reserve→settle），
  使用 summary 用途模型。

计费与幂等与其他生成物一致（reserve→settle、`client_request_id` 去重）。

聊天、生成物和索引创建都应发送由客户端稳定保存的 `client_request_id`，最长
`128` 字符。在认证的 PostgreSQL 部署中，同一个 ID 和同一个请求内容会复用已完成
结果，防止双击、超时重试或页面恢复造成重复调用和重复收费；同一个 ID 被用于不同
内容时返回 `409`。聊天幂等响应最多保留 24 小时；生成物通过持久化的
`ai_artifacts.client_request_id` 去重，直到生成物被删除；索引任务保留自己的请求
ID。请求正在另一 worker 处理中时返回冲突，客户端应稍后用同一 ID 重试，而不是
生成新 ID。

## 提示缓存不是上下文扩容

`OPENAI_PROMPT_CACHE` 只能降低重复、稳定前缀的输入费用和延迟。它不会：

- 扩大模型或 `max_context_tokens` 的可读窗口；
- 让超限的 `full` 请求绕过 `422`；
- 自动让模型读到未发送的全文；
- 免除动态历史、当前问题、检索块或首次索引的 token 用量。

要提高缓存命中率，应保持系统提示和稳定项目说明在输入前部，把聊天历史、当前问题
和动态检索内容放在后部。最终计费以 provider 返回的实际 usage 为准；响应会保留
`cached_tokens` 和 `cache_write_tokens`（上游提供时）。官方 Responses 请求固定
发送 `store: false`；启用提示缓存不会顺带开启 provider 端 response
application-state 存储。GPT-5.6 及后续模型把显式断点附在稳定的 `input_text`
内容块上，并固定使用当前唯一支持的 `prompt_cache_options.ttl: 30m`；旧模型的
24 小时扩展保留使用独立的 `prompt_cache_retention: 24h`，不会混用两种 schema。

## 存储配额、删除和保留

认证部署的租户存储配额包括：

- 云端转录文本；
- 知识库原文件；
- 提取文本和知识块；
- 旧向量及 1536 维语义向量；
- 会话语义块；
- AI 摘要、笔记和行动项。

删除知识源会在同一个数据库事务中删除文本和向量，并把
`KNOWLEDGE_DATA_PATH` 下的原文件加入持久化 blob 删除队列；删除项目会级联删除项目
关联、知识块和索引任务，并把项目原文件逐个加入同一队列。固定 worker 使用数据库
租约执行物理删除，失败会记录并重试，进程重启后仍可恢复，因而不会依赖 HTTP 请求
期间的一次性文件系统操作。

数据库元数据删除提交后，相关内容会立即从逻辑存储配额中释放；物理磁盘空间可能要
等 blob 删除 worker 完成后才释放。运维磁盘告警应以实际文件系统用量为准，而不能
只看租户逻辑配额。删除生成物会立刻释放其主数据所占配额。解除会话与项目关联只删除
关联关系，不删除会话、项目或索引。

应用删除不会修改已经生成的数据库备份、对象存储快照、代理日志或外部 provider
保留的数据，运营方必须单独配置这些系统的保留和销毁策略。聊天的
`ai_generation_requests` 幂等响应最多保留 24 小时，过期记录会被清理，并通过
`session_id` 外键在删除会话时级联删除。成功生成的摘要、笔记和行动项只持久化在
`ai_artifacts`，完成后会释放临时 generation reservation；删除生成物会同时移除
其持久化内容和去重依据，不会在 generation cache 中留下响应副本。

## Legacy 兼容路径

旧 `REAL[]` 向量和 `RAG_DB_PATH` 指向的 SQLite 数据不会被迁移 019 自动转换成
pgvector，也不会在部署时产生 Embedding 费用。它们仍可显示为 `legacy` 检索结果。
新生产项目应使用 PostgreSQL 混合检索；SQLite 的 `RAG_MAX_DB_MB` 仅约束 legacy
数据库，不替代租户 PostgreSQL 存储配额。

旧 `/api/rag/ingest` 仍用于匿名/迁移兼容场景。若其配置允许 Embedding，主动启用
自动入库仍可能产生模型用量；生产项目知识库则以显式上传、显式索引确认流程为准。

## 运行指标

Prometheus 指标包含 AI 索引队列深度、完成/失败数量、耗时、检索方式，以及已有的
聊天、Embedding 和缓存 token 用量。部署时应至少对以下情况告警：

- 队列持续增长但 worker 没有完成任务；
- `error` 比例或任务耗时突然上升；
- `lexical_fallback` 长时间增加；
- 缓存 token 异常下降；
- 租户存储或 DP 余额接近上限。

完整环境变量见 [ENVIRONMENT_VARIABLES.md](./ENVIRONMENT_VARIABLES.md)，升级步骤见
[DEPLOY.md](../DEPLOY.md)。
