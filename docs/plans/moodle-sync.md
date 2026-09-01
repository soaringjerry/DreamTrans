# DreamTrans Moodle Sync — PRD v0.1

> 一句话：一个只在用户浏览器内运行的 Moodle 同步扩展，把每周的课件、Echo360 录播和 DreamTrans 转写对齐成一份"本周学习包"。服务器永远不持有 Moodle 凭据，原始 PDF 默认不上云。

---

## 0. 先说清楚：我们赢不了 Asksia 什么

Asksia 有 2M+ 用户、四端产品、完整的 AI 学习套件（flashcard / quiz / 笔记 / 解题）。一个人在一个学期内不可能在"功能广度"上超过它，也不应该试。

**能赢的是四个点，每一个都是 Asksia 架构上不好改的：**

| 轴 | Asksia（基于公开信息推断） | 我们 |
|---|---|---|
| 抓取可靠性 | 大概率 DOM 爬取，靠"支持所有主题"的 selector 兜底 | API-first + 三级降级，元数据完整 |
| 增量与通知 | 手动点 Sync，全量重拉 | 基于 `timemodified` 的 diff，"PSY2041 新上传了 Week 6 slides" 主动推送 |
| 隐私/合规 | 整份 PDF 进第三方云 | 浏览器内抽取，默认只上传派生物（文本/结构），原文件本地或用户自有云 |
| 录播 | 声称拉 Echo360，但 LTI iframe 只能拿到链接 | **DreamTrans 的主场**：录播转写 ↔ 当周 slides 按页对齐 |

第四点是真正的护城河。Asksia 的转写是通用功能；我们的转写知道"这句话对应第 14 页幻灯片"，因为 slides 就是同一次 sync 拉下来的。

---

## 1. 目标用户与场景

- **P0**：Monash 学生（Moodle 4.x + Okta SSO + Echo360）。先在一所学校做到 100% 可靠，再谈泛化。
- **P1**：其他 Go8 Moodle 学校（UniMelb、UNSW 等）。
- 核心场景：周一早上打开 DreamTrans，看到本周 4 门课新增了哪些材料，每个录播已经转写并和 slides 对齐，可以直接开始复习。

---

## 2. 架构

四层，每层可独立替换。**所有对 Moodle 的请求只从 content script 发出。**

```
┌──────────── Browser (extension) ────────────┐
│ ① Discovery  → ② Fetch  → ③ Extract         │──④ Upload──▶ DreamTrans API
│    course tree    blobs      text/structure  │   (派生物 + 可选原文件)
└─────────────────────────────────────────────┘
```

### ① Discovery — 拿课程树（API-first，三级降级）

1. **Moodle 内部 AJAX**（首选）
   `POST /lib/ajax/service.php?sesskey=<M.cfg.sesskey>&info=core_course_get_contents`
   返回 section → module → contents，每个文件带 `fileurl / filesize / timemodified / mimetype`。这是 Moodle 前端自己用的接口，不依赖主题、不依赖 web service token，SSO 下可用。
   `sesskey` 从页面 `M.cfg` 读（content script 注入一段 page-world script 或正则抓 HTML）。
2. **HTML 解析**（AJAX 返回 `servicenotavailable` 时）
   同时兼容 3.x `li.section.main` 和 4.x `[data-for="section"]`；再解析 `li.activity` 的 `modtype_*` class。
3. **裸链接扫描**（讲师把 PDF 直接贴在 label 里时）
   扫 `[role="main"]` 下所有 `<a href>`，按扩展名和 `pluginfile.php` 匹配。

每层输出统一的 `CourseTree` 结构，下游不知道是哪一层产的。

### ② Fetch — 按模块类型取内容

| modtype | 取法 |
|---|---|
| resource | `fetch(mod/resource/view.php?id=X)` 跟 302 → `pluginfile.php`，从 `response.url` 或 `Content-Disposition` 取真实文件名 |
| folder | `mod/folder/download_folder.php?id=X` 一次拿 zip，浏览器内解压 |
| book | `mod/book/tool/print/index.php?id=X` 整本 HTML |
| page / label | 直接取 HTML 正文 |
| url | 记录外链，若是 Echo360/Panopto/YouTube 进 ③ 的录播分支 |
| assign | 只取标题、描述、due date，**不取提交物** |
| forum | 只取公告类（news forum），普通讨论区默认不同步（隐私） |
| lti (Echo360) | 见 §3 |

并发上限 3，请求间隔 ≥ 200ms，单课总时长目标 ≤ 30s。**不做后台轮询**，只在用户打开 Moodle 页面时按需增量。

### ③ Extract — 浏览器内抽取（差异化核心）

- PDF → `pdf.js` 在扩展内跑，输出按页文本 + 页缩略图（可选）。
- PPTX/DOCX → `jszip` + XML 解析，输出按 slide/段落文本。
- 输出统一为 `DocumentText { pages: [{n, text, hash}] }`。
- **原始文件默认不上传。** 用户可选：(a) 留本地 IndexedDB；(b) 上传到自己的 Google Drive（已有 MCP 集成）；(c) 上传 DreamTrans（明确 opt-in，界面上写清楚）。

这一层是合规叙事的支点：DreamTrans 服务器上只有"派生的学习结构"，没有课件副本。

### ④ Upload / Sync

- 每个文件以 `sha256(content)` 为主键，服务端按内容寻址去重。
- 增量：本地存每个 module 的 `timemodified`，只处理变化项。
- 推送：sync 后若有新增，触发 DreamTrans 通知（"PSY2042 新增 2 个文件"）。

---

## 3. 录播 ↔ 转写 ↔ Slides 对齐（护城河）

Monash 的 Echo360 通过 LTI 嵌入 Moodle，content script 在 Moodle 页面拿不到视频流。三种路径按成本递增：

1. **零集成**：sync 只记录"Week 6 有一个 Echo360 录播"。用户在 DreamTrans 里现有的方式转写（现场录音或上传），DreamTrans 用 sync 拿到的 Week 6 slides 做对齐。
2. **Echo360 页面 content script**：扩展在 `echo360.net.au` 域下另有一个 content script，读取当前播放的 media 元数据和（若可访问）transcript/caption 接口。Echo360 自带 ASR 字幕，质量一般，但可作为对齐锚点。
3. **音频流抓取**：从 Echo360 播放器抓 HLS → 送 DreamTrans 转写。技术可行但版权敏感度最高，**v1 不做**。

对齐算法：转写分段与 slides 每页文本做 embedding 相似度 + 时序约束（DP），输出 `[ {t_start, t_end, slide_n} ]`。这是 DreamTrans 已有转写能力的自然延伸。

---

## 4. 合规硬约束（设计即合规，不靠事后声明）

1. **凭据零接触**：服务器永远不接收 MoodleSession、sesskey、mobile token。所有 Moodle 请求在用户浏览器内、用户在场时发出。
2. **不做后台轮询**：只在用户打开 Moodle 时按需 sync。不给 eSolutions 留下"机器人流量"的把柄。
3. **原文件默认不上云**：只上传派生物。上传原文件是显式 opt-in。
4. **只读**：manifest 里不申请任何能写入的能力；代码里没有 POST 到 Moodle 的路径（`service.php` 的只读 method 除外）。
5. **不同步他人内容**：讨论区、提交物、成绩默认关闭。
6. **不分发**：DreamTrans 内不提供"分享课件给同学"功能。派生物也不跨用户共享，即使内容去重也只是存储层去重，不是访问层共享。
7. **图书馆资源除外**：Leganto / eReserve 链接识别后跳过，不抓。这是 Monash 政策里"scripts, agents or robots"明文禁止的对象。

---

## 5. 数据模型（最小）

```
Course      { id, shortname, fullname, lms_host, last_synced_at }
Section     { course_id, id, name, order }
Module      { section_id, cmid, modtype, name, url, timemodified, due_at? }
Document    { module_id, sha256, filename, mimetype, size, page_count, storage: local|gdrive|cloud }
DocumentText{ sha256, pages: [{n, text}] }
Recording   { module_id, provider, external_id, duration?, transcript_id? }
Alignment   { transcript_id, sha256, segments: [{t0, t1, page}] }
```

---

## 6. 范围与里程碑

**M0 — 验证（1 周）**
在 Monash Moodle 上手动跑通：`core_course_get_contents` 是否可用、`sesskey` 提取、四门课的 modtype 分布、Echo360 嵌入方式。产出一份"Monash 特征表"。

**M1 — 可用（3 周）**
扩展 MV3；Discovery 三级降级；resource/folder/page/book/assign 五种类型；pdf.js 抽取；上传派生物到 DreamTrans；增量 sync。只支持 Monash。

**M2 — 差异化（3 周）**
Echo360 路径 1 + 2；转写-slides 对齐 v1；新增材料通知。

**M3 — 泛化（按需）**
第二所 Go8 学校；Chrome Web Store 上架；隐私政策页。

**明确不做（v1）**
flashcard / quiz / 解题 / 多端同步 / Canvas·Blackboard / 后台定时 sync / 音频流抓取。

---

## 7. 风险

| 风险 | 概率 | 处理 |
|---|---|---|
| Monash 禁用 `core_course_get_contents` AJAX | 中 | 降级到 HTML 解析，M0 验证 |
| Monash Moodle 升级改 DOM | 低-中 | API-first 天然免疫；HTML 层用 data-attribute 不用 class |
| eSolutions 把扩展流量当异常 | 低 | 限速 + 无后台轮询 + 用户在场 |
| Echo360 页面结构变化 | 中 | 路径 1 永远可用，路径 2 是增强 |
| 被认定为版权问题 | 低 | 派生物模式 + 不分发 + 图书馆资源除外 |
| Chrome Web Store 审核（host permission） | 中 | 用 `optional_host_permissions`，用户首次在 Moodle 域上手动授权 |

---

## 8. M0 验证清单（在 Monash Moodle 上逐项确认）

- [ ] Moodle 版本（页面源码 `M.cfg` 或 `/lib/upgrade.txt`）
- [ ] `lib/ajax/service.php` + `core_course_get_contents` 是否返回数据
- [ ] `core_course_get_enrolled_courses_by_timeline_classification` 是否可用（拿课程列表）
- [ ] `sesskey` 在 HTML 中的位置与格式
- [ ] PSY2041 / PSY2042 / SCI1000 / EAE1022 各自的 modtype 分布
- [ ] Echo360 在 Moodle 中的嵌入方式（LTI iframe？外链？）及域名
- [ ] Echo360 页面是否暴露 transcript / caption 接口
- [ ] Leganto reading list 的链接特征（用于排除）
- [ ] 单课全量 fetch 的请求数与耗时
