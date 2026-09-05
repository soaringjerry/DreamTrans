# 浏览器内容安全策略

Go 服务默认返回 `Content-Security-Policy-Report-Only`。此阶段仅报告违规，
不拦截资源；不能把报告模式视为已经阻断 XSS。代理已有 CSP 时，两者都要核对。

浏览器将报告发送到同源 `/api/security/csp-report`。接口限制请求大小和频率，
服务器只记录指令名，例如 `CSP browser report: directive=connect-src`，
不记录页面地址、阻止的 URL 或脚本片段，避免泄露验证令牌及私人内容。
需要定位具体资源时，在浏览器开发者工具 Console 中查看 CSP 提示。
报告无需登录，不能视作可信安全告警。

上线先验证登录、实时录音、翻译、音频回放下载、MP3 Worker、学习空间和管理页。
策略允许同源脚本与连接、同源及 blob Worker、blob 音频、data/blob 图片，
并保留现有动态布局需要的内联样式。脚本不允许内联代码或 eval。
独立前端域名、额外 CDN 或旧版直连供应商工作流需先核对报告并调整策略。

确认兼容后，向应用进程传入 `CSP_MODE=enforce` 并重启，即改为
`Content-Security-Policy` 拦截模式。未设置、`report-only` 或无效值均为报告模式。
本文不修改生产环境、Compose 或反向代理配置。Vite 开发服务器不经过 Go 中间件，
应在 Go 托管的构建版本上完成部署验证。
