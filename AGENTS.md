# 提交与验证

- 每次提交前，按 `.github/workflows/ci.yml` 检查受影响的部分；所有修改完成后再验证，验证后修改文件必须重新运行受影响的检查。
- 前端修改使用 CI 的 Node 版本、`npm ci` 安装依赖与 Playwright Chromium，然后在 `frontend` 运行 `CI=true VITE_BACKEND_URL=/ VITE_BACKEND_WS_URL=/ npm run verify:ci`。必须运行完整 E2E，不能只跑新增或直接修改的测试。
- 后端修改执行 CI 的格式、依赖、带 `event_worker` 标签的 lint、数据库迁移及完整 race 测试；涉及安装或镜像时同时执行对应生命周期和运行时验证。数据库测试使用隔离的测试数据库。
- 不能把未执行、跳过或失败的检查描述为通过。发现本地无法执行的检查时，明确报告原因。
- 推送后查看对应提交的远端 CI，修复失败并确认结果后再报告完成。
