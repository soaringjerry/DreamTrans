# CI/CD 工作流说明

## 概述

本项目使用 GitHub Actions 实现完整的 CI/CD 流程：

- **CI (持续集成)**: `ci.yml` - 代码质量检查
- **CD (持续部署)**: `docker-build.yml` - Docker 镜像构建和发布

## CI 工作流 (ci.yml)

### 触发条件
- 推送到 `main` 分支
- 针对 `main` 分支的 Pull Request

### 检查项目

#### 前端检查
1. **ESLint**: 代码风格和潜在错误检查
2. **TypeScript 类型检查**: 确保类型安全
3. **构建测试**: 验证代码可以成功编译

#### 后端检查
1. **golangci-lint**: 综合性的 Go 代码检查
   - 格式检查 (gofmt)
   - 潜在错误 (govet, errcheck)
   - 代码复杂度 (gocyclo)
   - 安全问题 (gosec)
   - 更多...
2. **单元测试**: 运行所有测试用例
3. **测试覆盖率**: 生成并上传覆盖率报告

#### 安全扫描
- **Trivy**: 扫描已知的安全漏洞

### 本地运行 CI 检查

#### 前端
```bash
cd frontend
npm run lint        # 运行 ESLint
npm run lint:fix    # 自动修复可修复的问题
npm run type-check  # TypeScript 类型检查
npm run build       # 测试构建
```

#### 后端
```bash
cd backend

# 安装 golangci-lint
go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest

# 运行 lint
golangci-lint run ./...

# 运行测试
go test -v -race ./...
```

## CD 工作流 (docker-build.yml)

### 功能
- 多平台构建 (AMD64, ARM64)
- 自动版本标签
- 推送到 GitHub Container Registry
- 构建缓存优化

### 镜像标签策略
- `latest`: 最新的 main 分支构建
- `main`: main 分支的最新版本
- `v1.0.0`: 语义化版本标签
- `main-abc1234`: 分支名+短 SHA

## 最佳实践

### 开发流程
1. 创建功能分支
2. 开发并本地测试
3. 提交 Pull Request
4. CI 自动检查代码质量
5. 代码审查
6. 合并到 main
7. CD 自动构建和发布 Docker 镜像

### 修复 CI 失败
1. 查看 Actions 标签页的错误日志
2. 本地运行相应的检查命令
3. 修复问题
4. 推送修复

### golangci-lint 配置
- 配置文件: `.golangci.yml`
- 可以根据项目需求调整启用/禁用的检查器
- 测试文件有更宽松的规则

## 常见问题

### Q: 如何跳过某个 lint 规则？
A: 
- 前端: 使用 `// eslint-disable-next-line` 注释
- 后端: 使用 `//nolint:规则名` 注释

### Q: 如何在本地模拟 CI 环境？
A: 使用 Docker 运行相同的命令：
```bash
docker run --rm -v "$PWD":/app -w /app golangci/golangci-lint:latest golangci-lint run
```

### Q: CI 通过了但 CD 失败？
A: 检查：
- 环境变量是否正确设置
- Docker 构建参数是否正确
- Go 版本是否匹配