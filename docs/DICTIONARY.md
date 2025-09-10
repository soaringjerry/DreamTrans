# 内置词典（SQLite）

本项目支持内置英文字典，用于在转写区点击英文单词弹出释义浮层。词典数据建议以 CSV 导入 SQLite，避免绑定外部数据库。

## 文件与路径
- 词典数据库：`/app/data/dict.db`（可用 `DICT_DB_PATH` 覆盖）
- 导入工具：`backend/cmd/tools/dict-import`（CSV → SQLite）

## 导入 CSV 到 SQLite

示例命令（本机开发）：
```bash
# 构建并运行导入器（需要 Go 1.21+）
go run ./backend/cmd/tools/dict-import \
  -in /path/to/EnWords.csv \
  -out ./backend/data/dict.db \
  -col-word word \
  -col-def definition \
  -col-phon phonetic \
  -col-pos pos

# 将 dict.db 放到运行目录（或容器的 /app/data）
mkdir -p ./backend/data
mv ./backend/data/dict.db ./backend/data/dict.db
```

参数说明：
- `-in`：CSV 文件路径（如 EnWords.csv）。
- `-out`：输出 SQLite 路径，默认 `./dict.db`。
- `-col-*`：CSV 列名（大小写不敏感），至少需要 `word` 与 `definition`。
- `-sep`：分隔符，默认`,`，也支持`\t`。

CSV 预期列：
- `word`：单词
- `definition`：释义
- `phonetic`（可选）：音标
- `pos`（可选）：词性

## 后端运行
- 启动后端时若在 `DICT_DB_PATH` 指定的路径存在 `dict.db`，即启用词典 API；否则接口返回 503。

环境变量：
```bash
DICT_DB_PATH=/app/data/dict.db
```

接口：
- `GET /api/dict?word=hello` → `{ found: true, entry: { word, phonetic, pos, definition, extra } }`
- `GET /api/dict/prefix?q=hel&limit=10` → `{ items: [Entry...] }`

## 前端交互
- 原文转写区域的英文单词可点击，弹出词典浮层，显示音标/词性/释义；查询结果会在浏览器内缓存，减少重复请求。

## 体积与版本管理建议
- 不建议将 200MB 以上的词库直接提交到 Git 仓库。
- 推荐做法：
  - 作为运行时挂载（Docker volume/宿主机路径），容器读取 `/app/data/dict.db`；
  - 或作为 Release 资产/对象存储（CI 下载并放置到数据目录）。

