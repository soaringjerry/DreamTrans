# 词典集成（暂缓方案）

当前阶段不在本仓库内置本地词库或词典接口，避免仓库与镜像体积膨胀、协作与 CI 负担加重。词典功能将改为调用云端 API 服务进行解耦。

## 决策
- 不将 221MB+ 词库文件纳入仓库或镜像。
- 不在主后端暴露词典接口。
- 前端后续对接云端词典 API（例如：/lookup、/suggest），并在 UI 中以弹层展示释义。

## 云端 API 接入建议（规划）
- 鉴权：使用 API Key 或同域 Cookie；限制跨域与速率。
- 接口约定（示例）：
  - `GET https://dict.example.com/lookup?word=hello` → `{ word, phonetic, pos, definition, extra } | 404`
  - `GET https://dict.example.com/suggest?q=hel&limit=10` → `{ items: [{ word, pos }] }`
- 前端集成：点击英文单词时请求云 API，结果在浏览器缓存（内存/IndexedDB）以减少重复请求。

## 未来待办（如需要再启用本地词库）
- 提供独立“词典服务”仓库/镜像，读取 SQLite 词库并提供只读查询；此仓库仅通过前端配置使用该服务。
- 或者在部署脚本层支持下载 Release 资产（dict.db）至数据卷，但不进入主仓库。

本文件仅做阶段性设计记录；具体实现将在确认业务与数据授权后再推进。
