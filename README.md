# 代理池管理服务

这是一个 Go 单体代理池工具，提供代理管理、批量导入、免费代理采集、健康检测、匿名性识别、HTTP/SOCKS5 对外代理入口和 Web 管理界面。前端静态资源通过 Go `embed` 打包进最终二进制，可离线打开和试用基础流程。

## 运行方式

```powershell
go build -buildvcs=false -o .\bin\proxypool.exe .\cmd\proxypool
.\bin\proxypool.exe
```

默认地址：

- Web 管理界面：`http://127.0.0.1:8080/`
- HTTP/HTTPS CONNECT 代理入口：`127.0.0.1:18080`
- SOCKS5 代理入口：`127.0.0.1:18081`
- 默认账号：`admin`
- 首次密码：首次没有 `config.json` 且没有管理员密码哈希时自动随机生成

可指定配置文件和数据库路径：

```powershell
.\bin\proxypool.exe -config .\config.json -db .\data\proxypool.db
```

Linux/macOS amd64 解压发布包后运行：

```bash
tar -xzf dist/proxypool-linux-amd64.tar.gz -C proxypool-linux
cd proxypool-linux/proxypool-linux-amd64
./proxypool -config ./config.json -db ./data/proxypool.db
```

## 项目结构

```text
cmd/proxypool/              可执行程序入口
internal/proxypool/         后端业务代码、SQLite 仓储、代理协议和嵌入式 UI
internal/proxypool/storage/ sqlc schema、查询文件和生成的类型安全 Go 查询代码
internal/proxypool/webui/   离线 Web 控制台静态资源
configs/                    示例配置文件
docs/                       功能说明文档（自定义抓取等）
scripts/                    跨平台构建脚本
bin/                        本地开发构建产物
dist/                       跨平台发布压缩包（Windows/Linux/macOS × amd64/arm64）
data/                       SQLite 运行时数据
logs/                       运行日志
```

`config.json`、`data/`、`logs/` 属于运行时状态，保留在项目根目录以兼容现有启动方式；示例配置放在 `configs/config.example.json`。

## 已实现功能

### 代理池管理

- 支持 HTTP、HTTPS、SOCKS5 上游代理。
- 支持协议标签展示，同一主机、端口、账号、密码的多协议记录会合并显示能力标签，例如 `http`、`https`、`socks5`。
- 支持线路标签展示，检测后会标记为 `仅国内`、`仅海外`、`双向`、`均不可用` 或 `未知`。
- 支持代理质量分展示，按状态、成功率、失败次数、延迟、匿名性和线路能力实时计算 `0-100` 分。
- 支持启用、禁用、删除、手动检测代理。
- 支持查看代理详情，包含质量分构成、检测时间、使用时间、来源和运行状态。
- 支持按协议、状态、匿名性、线路、主机、账号、来源筛选。
- 支持分页浏览，页面可选择每页 `10`、`20`、`50`、`100` 条。
- 代理池列表显示的是当前筛选和分页下的连续序号；数据库内部 `id` 仍为唯一递增主键，删除代理后不会回收或重排，日志和接口操作仍使用该内部 `id`。
- 支持导出当前筛选结果为 `proxies.txt`。
- 支持清理不可用或已禁用代理。
- 支持后台检测全部代理。

### 失效档案

达到失败阈值被移出代理池的代理不会直接丢弃，而是进入“失效档案”页归档：

- 归档记录保留失败次数、来源、最近失败时间和最近错误。
- 归档中处于“正在跳过”状态的记录会拦截同参数代理再次导入，避免失效代理被采集源反复拉回。
- 对单个档案记录可“关闭跳过”（允许重新导入）或“启用跳过”。
- 支持按主机、来源或错误关键词搜索，按跳过状态筛选。
- 支持删除单条归档记录或清空全部档案。
- 档案列表支持分页浏览，页面可选择每页 `10`、`20`、`50`、`100` 条。

### 批量导入

支持文本粘贴和文件上传导入，兼容以下格式：

```text
socks5://user:pass@127.0.0.1:1080
http://127.0.0.1:8080
user:pass@host:port
host:port
http,host,port,user,pass
```

导入时会自动规范化和去重。导入结果会显示成功、重复、失败数量和错误明细。

### 免费代理采集

- 内置多个公开免费代理源。
- 代理源支持按行声明默认协议标签，格式为 `协议标签|URL`，例如 `http|https://example.com/http.txt`、`socks5|https://example.com/socks5.txt`、`http,socks5|https://example.com/unknown.txt`、`auto|https://example.com/mixed.txt`。
- 默认协议标签可写单个协议，也可写多个协议。裸 `host:port` 候选会按标签集合展开，例如 `http,socks5` 会导入 HTTP 和 SOCKS5 两条候选；`auto` 或 `all` 等价于 `http,https,socks5`。如果候选本身带 `http://`、`https://`、`socks5://` 或 CSV 协议字段，则按候选自身协议导入。
- 采集出口可选：
  - 本机直连
  - 通过当前代理池
  - 通过指定代理
- 支持本地示例源，断网时也能测试采集解析和导入流程。
- 采集任务后台运行，UI 可查看任务运行中、完成或停止结果。
- 任务过程日志会显示请求 URL、HTTP 状态、响应大小、响应预览、解析数量、导入/重复/验证统计和错误明细。
- 支持停止当前采集任务，正在进行的请求会通过上下文取消尽快退出。
- 支持采集后立即验证，也可关闭验证用于离线试用。
- 本机地址目标会自动直连，避免 `127.0.0.1` 示例源通过本机代理入口绕回自身。
- 采集源质量统计支持按来源/错误关键词查询、仅失败来源筛选、删除单条统计和清空全部统计。
- 采集源质量统计支持分页浏览，页面可选择每页 `10`、`20`、`50`、`100` 条。

### 自定义网站抓取

支持在 UI 的“自定义抓取”页面配置不同代理网站的抓取模板，例如 `https://proxy.scdn.io/`。

> 详细的字段说明、正则写法、分页策略、API 调用和问题排查见 [docs/custom-scraper.md](docs/custom-scraper.md)。

#### 快速上手

1. 进入“自定义抓取”页面，点击“新建”，填写名称和 URL 模板（支持 `{page}`、`{size}` 占位符）。
2. 页面只提供 `IP:端口` 时，在“默认协议标签”勾选候选协议（可多选，会按所选协议分别导入）。
3. 点击“测试提取”，预览实际请求的 URL 和提取到的代理，确认无误后“保存模板”。
4. 点击“运行抓取”后台执行，可在“运行结果”页签查看过程日志，也可中途停止。
5. 导入结果进入代理池，来源统计进入“采集源质量统计”。

模板支持：

- URL 页码模板，例如 `https://example.com/page/{page}`。
- 请求方法支持 `GET` 和 `POST`。
- 支持自定义请求头，每行一个，格式为 `Header: value` 或 `Header=value`。
- 支持自定义请求体，POST body 中同样可以使用 `{page}` 占位符。
- 使用文本/HTML 正则提取代理，JSON 响应也按普通文本处理。
- 固定范围页码：起始页和结束页。
- 全部页自动识别：先请求起始页，再根据页数识别正则或内置分页规则解析最大页。
- 自动识别最多页数限制，避免目标站分页异常导致无限抓取。
- 请求超时可按模板配置；配置为 `0` 时使用全局请求超时。
- 支持按模板配置并发页数，使用工作池并发抓取多个页面。
- 支持按模板配置失败重试次数，失败后自动重试当前页面。
- 支持按模板配置失败自动停止阈值，页面请求失败达到阈值后停止处理剩余页面。
- 自定义页数识别正则和分组，例如 `(?P<page>\d+)` 或数字分组 `1`。
- 默认代理协议标签支持多选：`http`、`https`、`socks5` 可任意组合。
- 对于无法确定类型的 `host:port` 页面，可选择多个默认协议标签，例如 `http,socks5`；系统会按所选协议分别导入并给同一 endpoint 合并协议能力标签。旧配置中的 `auto` 或 `all` 仍兼容，等价于全选 `http,https,socks5`。
- 正则提取规则。
- 自定义分组名：
  - `scheme_group`
  - `host_group`
  - `port_group`
  - `user_group`
  - `password_group`
- 启用/禁用模板。
- 测试提取，最多展示前 20 个匹配代理。
- 自定义抓取出口可单独配置：
  - 本机直连
  - 通过当前代理池
  - 通过指定代理
- 自定义抓取可单独选择是否导入后立即验证。
- 后台运行抓取任务，结果进入代理池并记录来源统计。
- 运行中会显示自动分页、页面请求、响应状态、响应预览、提取数量、导入统计等过程日志。
- 支持停止当前自定义抓取任务。
- 自定义抓取模板列表支持分页浏览，页面可选择每页 `10`、`20`、`50`、`100` 条。
- 自定义抓取页面提供“字段说明”折叠区，用于说明每项配置的用途和常见填写方式。

内置示例模板（“载入示例”按钮）：

```text
名称：proxy.scdn.io 示例
URL 模板：https://proxy.scdn.io/get_proxies.php?protocol=SOCKS5&country=&per_page={size}&page={page}
页码模式：固定范围，1-100，每页 100
默认协议标签：socks5
请求超时：按全局设置
并发页数：1，失败重试 1 次，失败 1 次即停止
提取正则：默认通用正则（scheme/host/user/password/port 分组）
```

`proxy.scdn.io` 即使返回 JSON，也会按响应文本用通用正则提取其中的 `host:port`。

### 匿名性识别

代理会被标记为：

- `anonymous`：匿名代理
- `transparent`：透明代理
- `unknown`：未知

检测逻辑会访问匿名性检测地址，判断是否暴露本机公网 IP 或出现 `X-Forwarded-For`、`Forwarded`、`Via` 等转发痕迹。

对外服务可配置匿名性筛选：

- 不限
- 仅匿名
- 仅透明
- 仅未知

### 健康检测和轮询

- 后台代理池检测已作为可管理的自动任务运行，可配置启停、检测间隔、检测范围和协议类型。
- 支持配置国内和海外两组可用性检测地址，每行一个。
- 国内检测默认使用 `http://117.72.157.151:8880/`。
- 海外检测默认使用 `https://www.gstatic.com/generate_204`、`https://www.cloudflare.com/cdn-cgi/trace`、`https://httpbin.org/status/204`。
- 可用性判断只看“代理能否连通检测地址并获得 HTTP 响应”，响应状态码 `200-499` 视为该线路可达。
- 支持配置响应内容特征校验，防止被劫持的代理“假可用”：
  - **必须命中关键词**（每行一个）：响应体需要全部包含这些关键词才算检测成功，例如用 `httpbin.org/get` 做检测源时要求命中 `origin`。留空不校验。
  - **禁止命中关键词**（每行一个）：响应体包含任意一个即判定不可用，用于识别劫持门户页、错误页等返回了其他内容的代理。
  - 两者可同时配置，禁止命中优先判定；特征校验对 HTTP/HTTPS 和 SOCKS5 代理检测均生效。
  - 响应体最多读取前 256KB 做关键词匹配。
- 可用性检测不会要求检测服务返回的 IP 等于代理地址。代理入口 IP、出口 IP、NAT、负载均衡或代理链都可能导致二者不同。
- `http://117.72.157.151:8880/` 返回的“当前 IP”当前只用于人工观察线路出口，不参与可用性判定。
- 出口 IP 和透明代理识别属于匿名性检测范畴，由匿名性检测地址和转发头判断逻辑处理。
- 检测时会分别测试国内和海外地址，并把代理标记为：
  - `domestic`：仅国内可用
  - `foreign`：仅海外可用
  - `both`：国内和海外都可用
  - `none`：国内和海外都不可用
  - `unknown`：尚未检测
- 任意一个线路成功即可判定代理总体可用；两组都失败才计为不可用并按失败阈值处理。
- 支持配置自动检测间隔、超时时间、失败删除阈值。
- 支持配置检测并发数，默认 `20`。
- 手动后台检测可选择范围：
  - 全部启用代理
  - 仅不可用代理
  - 仅未检测代理
  - 仅可用代理
- 自动检测和手动后台检测都可选择协议类型：全部、HTTP、HTTPS 或 SOCKS5。
- 对外代理入口会从可用代理中按状态、失败次数、延迟、最近使用时间进行选择。
- 对外代理入口支持出口策略配置：
  - `质量优先`：优先选择高质量分、低延迟、低失败次数代理。
  - `质量加权随机`：按质量分加权随机，避免少数代理被过度使用。
  - `低延迟优先`：优先选择检测延迟最低的代理。
  - `最近最少使用`：优先选择最久未使用的代理。
  - `按目标粘性`：同一目标地址倾向选择稳定一致的代理，候选失效时自动切换。
- 对外服务可配置线路筛选：不限、国内可用、海外可用、双向可用或未知；`国内可用` 会包含 `both`，`海外可用` 也会包含 `both`。
- 使用过程中发现代理不可用会记录失败并按阈值删除。

### 系统概览

UI 新增“系统概览”页，用于查看运行状态：

- 代理总数、可用数、平均延迟、平均质量分和高质量代理数量。
- 对外代理请求总数、成功数、失败数和最近错误。
- 当前后台采集任务、代理池检测任务状态和任务记录数量。
- 采集源数量、失败来源数量、自定义模板数量和启用数量。
- SQLite 数据库大小、日志文件大小。
- 定时采集状态、下次运行时间、今日运行次数、失败暂停状态。
- 数据维护状态、下次维护时间、最近维护结果。

### 任务中心

- 新增任务中心页，集中查看代理池检测、定时采集、数据维护和采集任务记录。
- 采集任务记录可查看开始时间、完成时间、运行结果和过程日志。

### 数据维护和定时采集

设置页新增“数据维护”配置：

- 是否启用自动维护。
- 维护间隔秒数。
- 日志保留天数，按日志行时间戳裁剪旧日志，`0` 表示不裁剪。
- 采集源统计保留天数，`0` 表示不清理。
- 数据库备份目录。
- 是否自动备份 SQLite 数据库。
- 是否自动执行 SQLite `VACUUM` 压缩。
- 是否自动清理不可用/禁用代理。

系统概览页可手动执行一次维护。

设置页新增“定时采集”配置：

- 是否启用调度。
- 调度间隔分钟。
- 是否运行免费代理源采集。
- 是否运行已启用的自定义抓取模板。
- 是否采集后立即验证。
- 每日最多运行次数。
- 失败后暂停分钟数，避免异常来源持续重试。

### 对外服务

提供两个本地代理入口：

- HTTP/HTTPS CONNECT：`127.0.0.1:18080`
- SOCKS5：`127.0.0.1:18081`

同时提供代理分发 API：

```http
GET /api/proxies/next?token=API_TOKEN
GET /api/proxies/next?token=API_TOKEN&anonymity=anonymous
GET /api/proxies/next?token=API_TOKEN&reachability=foreign
GET /api/proxies/next?token=API_TOKEN&strategy=sticky&target=example.com:443
```

### 配置文件同步

配置文件为 `config.json`。

启动流程：

1. 优先读取 `config.json`。
2. 将配置同步到 SQLite。
3. 如果 `config.json` 不存在，则根据当前设置自动生成。
4. 首次没有管理员密码/哈希时会随机生成管理员密码，只保存哈希到配置文件。

首次随机密码会输出到：

- 当前控制台输出
- `logs/proxypool.log`
- 配置文件同目录的 `initial-admin-password.txt`

保存密码后可以删除 `initial-admin-password.txt`。如果是 Windows 后台隐藏窗口或 Linux systemd 方式启动，看不到控制台时，直接查看日志文件或一次性密码文件即可。

UI 保存设置时会同时写入：

- SQLite settings 表
- `config.json`

配置项包括：

- 管理页面地址
- HTTP/HTTPS 代理入口
- SOCKS5 代理入口
- 管理员账号
- 管理员密码哈希
- API Token
- 健康检测地址
- 国内健康检测地址
- 海外健康检测地址
- 匿名性检测地址
- 对外服务匿名性筛选
- 对外服务线路筛选
- 对外服务出口策略
- 采集源
- 默认采集出口
- 检测并发数
- 自动检测开关、检测间隔、检测范围和检测协议类型
- 失败删除阈值
- 请求超时
- 检测必须命中关键词
- 检测禁止命中关键词
- 数据维护开关、维护间隔、日志保留天数、来源统计保留天数、备份目录、自动备份、自动压缩、自动清理
- 定时采集开关、调度间隔、免费源/自定义模板运行开关、采集后验证、每日最大次数、失败暂停分钟数
- 日志等级
- 日志文件路径
- 是否仅监听本机
- 管理入口黑白名单
- 代理入口黑白名单

管理员密码不会再以明文保存。旧版明文 `admin_password` 配置仍可兼容登录，启动后会自动迁移为 `admin_password_hash`。

### 日志系统

默认日志文件：

```text
logs/proxypool.log
```

支持日志等级：

- `debug`
- `info`
- `warn`
- `error`
- `off`

日志页可查看最近 100、200 或 500 行日志。

日志页支持：

- 按关键词查询。
- 按日志等级筛选。
- 清空当前日志文件。

已记录的关键事件：

- 服务启动
- 登录成功/失败
- 配置保存
- 代理导入
- 后台检测全部
- 清理代理
- 采集任务开始和完成

日志不会输出完整 API Token。

### 离线能力

Web UI 不依赖 CDN、外部字体、外部 JS 或外部 CSS。

静态资源包括：

- `internal/proxypool/webui/index.html`
- `internal/proxypool/webui/styles.css`
- `internal/proxypool/webui/app.js`
- `internal/proxypool/webui/sw.js`
- `internal/proxypool/webui/manifest.webmanifest`
- `internal/proxypool/webui/samples/http.txt`
- `internal/proxypool/webui/samples/socks5.txt`

首次访问后，service worker 会缓存 UI 静态资源。页面提供：

- 批量导入页：载入离线示例
- 免费代理采集页：使用本地示例源

## 数据文件

- SQLite 数据库：`data/proxypool.db`
- 配置文件：`config.json`
- 日志文件：`logs/proxypool.log`

历史功能演进的详细记录见 [CHANGELOG.md](CHANGELOG.md)。

## 主要 API

需要登录 Cookie 的管理 API：

```http
POST /api/login
POST /api/logout
GET  /api/me
GET  /api/stats
GET  /api/status
GET  /api/tasks
GET  /api/settings
PUT  /api/settings
POST /api/maintenance/run
GET  /api/proxies
GET  /api/proxies/{id}
GET  /api/proxies?reachability=domestic
GET  /api/proxies?page=1&page_size=20
POST /api/proxies/import
POST /api/proxies/{id}/check
PATCH /api/proxies/{id}
DELETE /api/proxies/{id}
POST /api/proxies/check-all
POST /api/proxies/check-all?scope=unavailable
POST /api/proxies/check-all?scope=unavailable&scheme=socks5
POST /api/proxies/cleanup
GET  /api/proxies/export
POST /api/collector/run
GET  /api/collector/jobs/{id}
POST /api/collector/jobs/{id}/stop
GET  /api/collector/sources
GET  /api/collector/sources?page=1&page_size=20
DELETE /api/collector/sources
DELETE /api/collector/sources?url=SOURCE_URL
GET  /api/dead-proxies
GET  /api/dead-proxies?q=host&disabled=false
PATCH /api/dead-proxies/{id}
DELETE /api/dead-proxies/{id}
DELETE /api/dead-proxies
GET  /api/custom-scrapers
GET  /api/custom-scrapers?page=1&page_size=20
POST /api/custom-scrapers
POST /api/custom-scrapers/preview
GET  /api/custom-scrapers/{id}
PUT  /api/custom-scrapers/{id}
DELETE /api/custom-scrapers/{id}
POST /api/custom-scrapers/{id}/preview
POST /api/custom-scrapers/{id}/run
GET  /api/logs
DELETE /api/logs
```

Token 保护的分发 API：

```http
GET /api/proxies/next?token=API_TOKEN
GET /api/proxies/next?token=API_TOKEN&reachability=foreign
GET /api/proxies/next?token=API_TOKEN&strategy=sticky&target=example.com:443
```

## 构建和测试

```powershell
sqlc generate
go test ./...
node --check .\internal\proxypool\webui\app.js
go build -buildvcs=false -o .\bin\proxypool.exe .\cmd\proxypool
```

如果本机没有 `sqlc`：

```powershell
go install github.com/sqlc-dev/sqlc/cmd/sqlc@v1.27.0
```

本地脚本发布全平台（Windows/Linux/macOS × amd64/arm64，共 6 个目标）：

```powershell
.\scripts\build.ps1
```

Linux/macOS shell：

```bash
sh ./scripts/build.sh
```

发布产物为压缩包（内含可执行文件、`README.md` 和 `configs/config.example.json`），并生成 `SHA256SUMS.txt` 校验文件：

- `dist/proxypool-windows-amd64.zip`
- `dist/proxypool-windows-arm64.zip`
- `dist/proxypool-linux-amd64.tar.gz`
- `dist/proxypool-linux-arm64.tar.gz`
- `dist/proxypool-darwin-amd64.tar.gz`
- `dist/proxypool-darwin-arm64.tar.gz`

也可以通过 GitHub Actions 手动触发发布（Actions → Release → Run workflow，填写 tag 如 `v1.0.0`），CI 会运行测试、构建 Windows/Linux amd64 产物并创建 Release。
