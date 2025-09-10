# 用户使用指南（UI 与设置）

本指南介绍新版界面布局、全局设置与使用步骤。

## 界面速览
- 顶部：品牌 + 全局按钮（历史、设置、GitHub）
- 状态条：待命/录音中/重连/错误
- 主面板：
  - 左：原文转写（支持说话人/段落、部分→最终）
- 右：翻译（根据设置选择 Speechmatics / AI 滚动 / AI 压缩）
  - 右：翻译（Speechmatics / AI）。支持实验性“英中对照”（Bilingual Mode）：一行英文 + 一行中文对照显示（设置 → Experimental）。
- 下：学习助手（RAG）聊天，带打字动效
  - Chat 支持 Markdown（标题、列表、代码块、表格、链接）以更好呈现结构化答案。
- 附加面板：
  - 知识点摘要：定时汇总“当前会话重点”，用于快速回顾
- 性能监控：Summary 卡片（Transcript/Translation/Chat 平均时延与迷你条形）、Live Metrics 实时彩条（ms→s 人性化显示）
  - Lexicon（二级标签）：本地词频/术语频率排行（实时增量），支持“显示：全部/未掌握/学习清单”、停用词过滤、搜索、AI 释义、CSV 导出。
  - 翻译进度：已翻译到的转写片段将加粗显示（按说话人独立追踪，容忍 200ms 时间对齐误差）

## 全局设置（右上角 Settings）
- API Base（默认 `https://api.openai.com/v1`）
- RAG/Chat Model（默认 `gpt-5`）
- Prompts（完整系统提示，可替换默认模板）：
  - Chat Prompt / Translation Prompt / Summary Prompt
- API Key（可选；不会展示后端 Key；仅存在浏览器 localStorage）
- 翻译设置：
  - Translation Mode：Speechmatics / AI Rolling / AI Compressed
  - Translation Model：`gpt-5` / `gpt-5-mini` / `gpt-5-nano`

### Experimental（实验性，默认关闭）
- Typewriter Mode：打字机式视觉，观感更自然；可能稍有延迟或不完整
- Streaming Output：尝试更流畅的逐步呈现
- Smart Algorithm：启用更“压缩/智能”的上下文策略
- Bilingual Mode：英中对照学习视图（翻译列以一行英文配一行中文显示）。

说明：
- 设置保存后立即生效；RAG/Chat 覆盖通过每次请求传给后端；翻译 WS 也读取全局设置。
- API Key 不会展示后端默认值，且只在本地保存。

## 快速开始
1. 点击 Start 开始录音并授权麦克风。
2. 说话几句（建议两句以上或停顿≥2.5s），左侧原文会按段生成；
3. 右侧翻译会实时更新；
4. 底部学习助手输入“现在在讲什么”，系统会结合实时上下文给出要点。
  - 提示词支持 Markdown 输出；答案会按 Markdown 渲染显示。
5. 点击 History 查看会话历史（IndexedDB），可选择“恢复”之前的会话并断点续录。

---

更多：
- RAG 细节与接口见 `docs/RAG.md`
- 部署与环境变量见 `docs/ENVIRONMENT_VARIABLES.md`、`docs/DOCKER_DYNAMIC_CONFIG.md`
