# 用户使用指南（UI 与设置）

本指南介绍新版界面布局、全局设置与使用步骤。

## 界面速览
- 顶部：品牌 + 全局按钮（历史、设置、GitHub）
- 状态条：待命/录音中/重连/错误
- 主面板：
  - 左：原文转写（支持说话人/段落、部分→最终）
  - 右：翻译（根据设置选择 Speechmatics / AI 滚动 / AI 压缩）
  - 下：学习助手（RAG）聊天，带打字动效
- 附加面板：
  - 知识点摘要：定时汇总“当前会话重点”，用于快速回顾
  - 性能监控：显示最近一次问答的 Tokens、Model、延迟（ms）

## 全局设置（右上角 Settings）
- API Base（默认 `https://api.openai.com/v1`）
- RAG/Chat Model（默认 `gpt-5`）
- Prompt（默认提示已填入，可编辑）
- API Key（可选；不会展示后端 Key；仅存在浏览器 localStorage）
- 翻译设置：
  - Translation Mode：Speechmatics / AI Rolling / AI Compressed
  - Translation Model：`gpt-5` / `gpt-5-mini` / `gpt-5-nano`

### 实验性设置（谨慎启用）
- 流式输出（实验，默认关闭）：启用后将尝试更流畅的逐步呈现回答（可能影响性能）
- 智能算法（实验，默认关闭）：启用后在后端使用更“压缩/智能”的上下文策略（等价于智能模式）

说明：
- 设置保存后立即生效；RAG/Chat 覆盖通过每次请求传给后端；翻译 WS 也读取全局设置。
- API Key 不会展示后端默认值，且只在本地保存。

## 快速开始
1. 点击 Start 开始录音并授权麦克风。
2. 说话几句（建议两句以上或停顿≥2.5s），左侧原文会按段生成；
3. 右侧翻译会实时更新；
4. 底部学习助手输入“现在在讲什么”，系统会结合实时上下文给出要点。
5. 点击 History 查看本地历史（localStorage），可清空。

---

更多：
- RAG 细节与接口见 `docs/RAG.md`
- 部署与环境变量见 `docs/ENVIRONMENT_VARIABLES.md`、`docs/DOCKER_DYNAMIC_CONFIG.md`
