# API Monitor (Go)

`API Monitor (Go)` 是 `api_monitor` 的 Go 版本实现，用于批量管理 API 渠道并周期性执行模型检测。

仓库地址：`https://github.com/ZhantaoLi/api_monitor_go`

## 功能概览

- 渠道管理：增删改查目标渠道（`name + base_url + api_key`）
- 定时巡检：后台扫描到期目标并触发检测
- 并发检测：目标内并发检测模型，目标间并行运行
- 结果落库：SQLite 保存 `targets / runs / run_models`
- 实时推送：SSE 推送 `run_completed`、`target_updated` 事件
- Web 页面：
  - 主界面：`/`
  - 日志页面：`/viewer.html?target_id=<id>`
  - 分析页面：`/analysis.html?target_id=<id>`
  - 管理登录：`/admin/login`
  - 管理页面：`/admin.html`
  - 代理文档：`/docs/proxy`
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

## 技术栈

- 后端：Go `net/http`（Go 1.22+ 路由语法）
- 数据库：SQLite（`modernc.org/sqlite`）
- 前端：HTML + Tailwind + Alpine.js + Chart.js
- 实时：Server-Sent Events (SSE)
- 部署：Docker Compose（Linux）

## 目录结构

```text
api_monitor_go/
├── data/                        # 运行时数据目录（建议 volume 挂载）
│   ├── logs/                    # 按 target/run 切分的 JSONL 日志
│   └── registry.db              # SQLite 主数据库
├── internal/
│   └── app/                     # 后端核心业务实现
│       ├── admin.go             # 管理员登录会话、设置项定义与校验
│       ├── db.go                # 表结构、迁移、CRUD、统计查询
│       ├── handler.go           # /api/targets 等监控 API 与数据聚合
│       ├── monitor.go           # 调度器、并发探测、日志落盘、历史汇总
│       ├── proxy.go             # /v1/* 代理转发、proxy key 权限控制
│       ├── run.go               # 路由注册、中间件装配、环境变量初始化
│       └── sse.go               # SSE 事件总线与鉴权中间件
├── web/                         # 前端页面与静态资源（embed 到二进制）
│   ├── assets/
│   │   ├── css/
│   │   │   └── styles.css       # 全局样式变量与布局样式
│   │   ├── js/
│   │   │   ├── admin.js         # 管理面板交互逻辑
│   │   │   ├── analysis.js      # 分析页图表与统计渲染
│   │   │   ├── dashboard.js     # 主看板交互、拖拽排序、SSE 刷新
│   │   │   ├── log-viewer.js    # 日志过滤、分页、run_id 查询
│   │   │   └── main.js          # 公共工具、鉴权与主题切换
│   │   ├── vendor/              # 三方前端依赖（离线静态文件）
│   │   │   ├── fonts/           # 本地字体文件
│   │   │   ├── alpine.min.js
│   │   │   ├── chart.min.js
│   │   │   ├── DragDropTouch.js
│   │   │   ├── phosphor-*.css / *.woff2
│   │   │   └── Sortable.min.js
│   │   └── tailwind.config.js   # Tailwind 运行时配置
│   ├── admin.html               # 管理面板主页
│   ├── admin_login.html         # 管理面板登录页
│   ├── analysis.html            # 渠道分析页
│   ├── index.html               # 主监控看板
│   ├── log_viewer.html          # 日志查看页
│   └── proxy_docs.html          # 代理接口文档页
├── .dockerignore                # Docker 构建忽略规则
├── .gitignore                   # Git 忽略规则
├── docker-compose.yml           # 容器启动配置（固定镜像 tag）
├── Dockerfile                   # 多阶段构建定义（Go build + Alpine runtime）
├── go.mod                       # Go 模块定义
├── go.sum                       # Go 依赖锁定
├── LICENSE                      # MIT 许可证
├── main.go                      # 入口：嵌入 web 资源并启动 app.Start
└── README.md                    # 项目说明
```
## 环境变量

- `PORT`：服务端口，默认 `8081`
- `DATA_DIR`：数据目录，默认 `data`
- `API_MONITOR_TOKEN`：API 鉴权 Token；为空时首次启动自动生成并持久化
- `ADMIN_PANEL_PASSWORD`：后台管理密码；为空时首次启动自动生成并持久化
- `DEFAULT_INTERVAL_MIN`：默认检测间隔（分钟），默认 `30`
- `LOG_CLEANUP_ENABLED`：日志清理开关，默认 `true`
- `LOG_MAX_SIZE_MB`：日志目录总大小上限，默认 `500`
- `PROXY_MASTER_TOKEN`：代理主令牌（可在后台管理页面修改）
- `MONITOR_DETECT_CONCURRENCY`：单次检测中模型探测并发数，默认 `3`
- `MONITOR_MAX_PARALLEL_TARGETS`：同时运行的渠道数上限，默认 `2`

## Linux Docker 运行

`docker-compose.yml` 默认使用固定镜像版本，可自行修改为 `latest`: `image: lming001/api-monitor-go:latest`

启动命令：

```bash
git clone https://github.com/ZhantaoLi/api_monitor_go.git
cd api_monitor_go
docker compose pull
docker compose up -d
```

首次启动若未显式设置 `API_MONITOR_TOKEN` / `ADMIN_PANEL_PASSWORD`，服务会自动生成并在日志中打印：

```bash
docker compose logs -f
```

默认访问地址：

- `http://127.0.0.1:8081/`

## 鉴权说明

- 除 `GET /api/health` 和静态页面外，API 需要：
  - `Authorization: Bearer <token>`
- SSE 端点额外支持：
  - `GET /api/events?token=<token>`

## 管理面板

- 登录页：`/admin/login`
- 管理页：`/admin.html`
- 管理 API（登录后）：
  - `POST /api/admin/logout`
  - `GET /api/admin/settings`
  - `PATCH /api/admin/settings`
  - `GET /api/admin/resources`
  - `GET /api/admin/channels`
  - `PATCH /api/admin/channels/{id}/advanced`

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
- `GET /api/targets/{id}/runs`
- `GET /api/targets/{id}/logs`
- `GET /api/proxy/keys`（管理员）
- `POST /api/proxy/keys`（管理员）
- `DELETE /api/proxy/keys/{id}`（管理员）
- `GET /v1/models`（代理）
- `POST /v1/chat/completions`（代理）
- `POST /v1/messages`（代理）
- `POST /v1/responses`（代理）
- `POST /v1beta/models/{model}:generateContent`（代理）
- `POST /v1beta/models/{model}:streamGenerateContent`（代理）

## API 代理示例

```bash
curl http://localhost:8081/v1/models \
  -H "Authorization: Bearer sk-your-proxy-key"

curl http://localhost:8081/v1/chat/completions \
  -H "Authorization: Bearer sk-your-proxy-key" \
  -H "Content-Type: application/json" \
  -d '{"model":"<channel>/<model>","messages":[{"role":"user","content":"Hello"}]}'
```

完整文档请访问：`/docs/proxy`

## 数据说明

- SQLite：`data/registry.db`
- JSONL 日志：`data/logs/target_<id>_<timestamp>.jsonl`

`run_models` 关键字段：

- `protocol`, `model`, `success`, `duration`, `status_code`
- `error`, `content`, `route`, `endpoint`, `timestamp`, `run_id`

## 注意事项

- `api_key` 当前明文存储在 SQLite，请结合磁盘权限和部署隔离使用。
- 生产环境建议配合反向代理、IP 白名单或额外鉴权。

## 许可证

本项目采用 MIT License，见 `LICENSE`。

## 致谢

 - https://github.com/BingZi-233/check-cx
 - https://github.com/chxcodepro/model-check