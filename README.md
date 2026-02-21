# API Monitor (Go)

`API Monitor (Go)` 是 `api_monitor` 的 Go 版本实现，用于批量管理 API 渠道并周期性执行模型测活。

仓库地址：`https://github.com/ZhantaoLi/api_monitor_go`

## 功能概览

- 渠道管理：增删改查目标渠道（`name + base_url + api_key`）
- 定时巡检：后台每分钟扫描到期目标并触发检测
- 并发检测：目标内并发检测模型，目标间并行运行
- 结果落库：SQLite 保存 `targets / runs / run_models`
- 实时推送：SSE 推送 `run_completed`、`target_updated` 事件
- Web 页面：
  - 主界面：`/`
  - 日志页面：`/viewer.html?target_id=<id>`
  - 分析页面：`/analysis.html?target_id=<id>`
- 渠道排序：主界面拖拽排序，持久化到 `sort_order`
- 日志查询支持指定 `run_id`：
  - `GET /api/targets/{id}/logs?run_id=<run_id>`
- API 代理（Proxy）：
  - `GET /v1/models`
  - `POST /v1/chat/completions`
  - `POST /v1/messages`
  - `POST /v1/responses`
  - `POST /v1beta/models/{model}:generateContent`
  - `POST /v1beta/models/{model}:streamGenerateContent`
  - 代理文档页：`GET /docs/proxy`

## 技术栈

- 后端：Go `net/http`（Go 1.22+ 路由语法）
- 数据库：SQLite（`modernc.org/sqlite`）
- 前端：HTML + Tailwind + Alpine.js + Chart.js
- 实时：Server-Sent Events (SSE)
- 部署：Docker Compose（Linux）

## 目录结构

```text
api_monitor_go/
├── data/                   # 运行期数据目录（默认忽略）
│   ├── logs/               # JSONL 明细日志目录
│   └── registry.db         # SQLite 主数据库文件
├── web/                    # 前端页面与静态资源
│   ├── assets/             # 页面脚本、样式与第三方资源
│   │   ├── vendor/         # 第三方前端库与字体文件
│   │   ├── analysis.js     # 分析页逻辑
│   │   ├── dashboard.js    # 主界面逻辑（含拖拽排序）
│   │   ├── log-viewer.js   # 日志页逻辑
│   │   ├── main.js         # 通用工具与鉴权逻辑
│   │   ├── styles.css      # 全局样式
│   │   └── tailwind.config.js # Tailwind 运行时配置
│   ├── analysis.html       # 分析页面
│   ├── index.html          # 主页面（渠道总览）
│   └── log_viewer.html     # 日志查看页面
├── .dockerignore           # Docker 构建忽略规则
├── .gitignore              # Git 忽略规则
├── db.go                   # 数据层：表结构、迁移、CRUD
├── docker-compose.yml      # Docker Compose 启动配置
├── Dockerfile              # 镜像构建定义
├── go.mod                  # Go 模块定义
├── go.sum                  # Go 依赖校验锁定
├── handler.go              # HTTP API 处理与参数校验
├── LICENSE                 # MIT 许可证
├── main.go                 # 程序入口与路由注册
├── monitor.go              # 调度、模型探测、结果写入
├── README.md               # 项目说明文档
└── sse.go                  # SSE 事件总线与鉴权中间件
```

## 环境变量

- `PORT`：服务端口，默认 `8081`
- `DATA_DIR`：数据目录，默认 `data`
- `API_MONITOR_TOKEN`：API 鉴权 Token；为空则首次启动随机生成并打印到终端
- `LOG_CLEANUP_ENABLED`：日志清理开关（默认开）
- `LOG_MAX_SIZE_MB`：日志目录总大小上限（默认 `500`）
- `ADMIN_PANEL_PASSWORD`：后台管理面板登录密码；为空则首次启动随机生成并打印到终端
- `DEFAULT_INTERVAL_MIN`：默认渠道检测间隔（分钟，默认 `30`）
- `PROXY_MASTER_TOKEN`：可直接访问代理端点的全局 token（可在后台面板修改）


## Linux Docker 运行

```bash
git clone https://github.com/ZhantaoLi/api_monitor_go
cd api_monitor_go
# 先把 docker-compose.yml 里的镜像 tagname 改成实际版本（如 v1.0.0）
docker compose pull
docker compose up -d
```

若 `docker-compose.yml` 中未提供 `API_MONITOR_TOKEN` / `ADMIN_PANEL_PASSWORD`，
首次启动会自动随机生成，并可通过 `docker compose logs -f` 在终端看到。

当前 `docker-compose.yml` 映射为 `8081:8081`，访问：

- `http://127.0.0.1:8081/`

## 鉴权说明

- 除 `GET /api/health` 和静态页面外，API 需要 `Authorization: Bearer <token>`
- SSE 端点额外支持 `?token=<token>`（EventSource 无法自定义 Header）
- 鉴权默认开启：若未配置 `API_MONITOR_TOKEN`，首次启动会随机生成并在终端打印

## 管理面板

- 登录页：`/admin/login`
- 管理页：`/admin.html`
- 支持配置：
  - `api_monitor_token`
  - `admin_panel_password`
  - `proxy_master_token`
  - 日志自动清理开关与最大体积
- `ADMIN_PANEL_PASSWORD` 默认开启：若未配置，首次启动会随机生成并在终端打印
- Proxy Keys 仅允许后台管理会话操作（`/api/admin/login` 登录后）


## 主要接口

- `GET /api/health`
- `GET /api/events`
- `GET /api/dashboard`
- `GET /api/targets`
- `GET /api/targets/{id}`
- `POST /api/targets`
- `PATCH /api/targets/{id}`
- `DELETE /api/targets/{id}`
- `POST /api/targets/{id}/run`
- `GET /api/proxy/keys`（管理员）
- `POST /api/proxy/keys`（管理员）
- `DELETE /api/proxy/keys/{id}`（管理员）
- `GET /v1/models`（代理）
- `POST /v1/chat/completions`（代理）
- `POST /v1/messages`（代理）
- `POST /v1/responses`（代理）
- `POST /v1beta/models/{model}:generateContent`（代理）
- `POST /v1beta/models/{model}:streamGenerateContent`（代理）

## API 代理

部署后可作为 API 代理使用。默认端口为 `8081`（可通过 `PORT` 修改）。

请求时在 `Authorization` 头携带代理密钥：

```bash
curl http://localhost:8081/v1/models \
  -H "Authorization: Bearer sk-your-proxy-key"

curl http://localhost:8081/v1/chat/completions \
  -H "Authorization: Bearer sk-your-proxy-key" \
  -H "Content-Type: application/json" \
  -d '{"model":"gpt-4o-mini","messages":[{"role":"user","content":"Hello"}]}'
```

`/v1/models` 只返回最近检测中“成功通过”的模型；若为空，请先在首页执行一次渠道检测。

代理密钥支持按渠道/模型维度限制访问权限，可通过管理员 API 管理：

- `GET /api/proxy/keys`
- `POST /api/proxy/keys`
- `DELETE /api/proxy/keys/{id}`

完整文档请访问：`/docs/proxy`

## 数据说明

- SQLite：`data/registry.db`
- JSONL 日志：`data/logs/target_<id>_<timestamp>.jsonl`

`run_models` 关键字段：

- `protocol`, `model`, `success`, `duration`, `status_code`
- `error`, `content`, `route`, `endpoint`, `timestamp`, `run_id`

## 注意事项

- `api_key` 当前明文存储在 SQLite，请结合磁盘权限和部署隔离使用。
- 如果你在公网部署，建议配合反向代理、IP 白名单或额外鉴权。

## 许可证

本项目采用 MIT License，见 `LICENSE`。
