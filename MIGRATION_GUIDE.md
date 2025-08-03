# DreamTrans 前端迁移至 DreamScribe 指南

本文档将指导您如何将 `DreamTrans` 项目中的前端功能完整、安全地迁移到 `DreamScribe` 项目中。

## 第 1 步：复制前端文件

您需要将 `DreamTrans` 项目根目录下的整个 `frontend` 文件夹复制到您的 `DreamScribe` 项目中。

```bash
# 假设您在 DreamTrans 项目的根目录
# 将整个 frontend 目录复制到 DreamScribe 项目中
cp -r frontend/ /path/to/your/DreamScribe/project/
```

这包括了所有的源代码、组件、钩子、样式以及配置文件。

## 第 2 步：合并依赖项

将 `DreamTrans/frontend/package.json` 文件中的 `dependencies` 和 `devDependencies` 合并到 `DreamScribe` 项目的 `package.json` 文件中。

**关键依赖项包括：**

*   `@speechmatics/real-time-client-react`
*   `@speechmatics/browser-audio-input-react`
*   `react` & `react-dom`
*   `lodash`
*   以及其他您可能需要的依赖。

合并后，在 `DreamScribe` 项目的根目录下运行 `npm install` 或 `yarn install` 来安装所有依赖。

## 第 3 步：迁移 CI/CD 工作流

为了确保 `DreamScribe` 项目的开发流程完整，您需要迁移与前端相关的 CI/CD 配置。

1.  **代码质量检查 (`.github/workflows/ci.yml`)**:
    *   从 `DreamTrans` 项目的 `.github/workflows/ci.yml` 文件中，找到 **“Frontend CI”** (`# ---- Frontend CI ----`) 的所有步骤。
    *   将这部分内容（从 `Set up Node.js` 到 `Check frontend build`）完整地复制到 `DreamScribe` 项目的 CI 配置文件中。

2.  **Docker 镜像构建 (`.github/workflows/docker-build.yml`)**:
    *   您需要为 `DreamScribe` 创建一个**全新的 Docker 构建工作流**。
    *   这个新的工作流将只负责构建和推送 `DreamScribe` 的**纯前端 Docker 镜像**。
    *   您可以使用 `DreamTrans/frontend/Dockerfile` 的内容作为新工作流中 Docker 构建步骤的基础。

## 第 4 步：配置环境变量

迁移后，最重要的一步是确保新的 `DreamScribe` 前端能够正确地连接到 `DreamTrans` 后端服务。

在 `DreamScribe` 项目中，创建一个 `.env` 文件，并设置以下环境变量：

```
# DreamTrans 后端服务的地址
VITE_BACKEND_URL=http://localhost:8080

# DreamTrans 后端 WebSocket 服务的地址
VITE_BACKEND_WS_URL=ws://localhost:8080
```

**请注意：**
*   如果您的 `DreamTrans` 后端部署在不同的服务器或端口上，请务必将 `http://localhost:8080` 替换为实际的地址。
*   `DreamScribe` 的前端代码（例如 `src/api.ts` 和 `src/hooks/useBackendWebSocket.ts`）已经配置为使用这些环境变量。

## 第 5 步：后续步骤（在 DreamTrans 项目中）

在您确认已成功将前端功能迁移并运行在 `DreamScribe` 中之后，我们将回到 `DreamTrans` 项目并执行以下清理操作：

1.  **调整后端 CORS 策略**：修改 Go 后端代码，以允许来自 `DreamScribe` 前端域名的跨域请求。
2.  **移除前端代码**：删除 `DreamTrans` 项目中的 `frontend` 目录。
3.  **简化 Docker 部署**：更新根 `Dockerfile` 和 `.github/workflows/docker-build.yml`，使其只构建和运行纯后端服务。

---

请按照此指南完成前端迁移。完成后，请通知我，我们将继续进行 `DreamTrans` 的后端重构。