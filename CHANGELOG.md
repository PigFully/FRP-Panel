# Changelog

本项目遵循 [语义化版本](https://semver.org/lang/zh-CN/)。

## v1.2.1 — 2026-08-25

### 修复
- **安装脚本数据库地址默认值由 `localhost` 改为 `127.0.0.1`**：部分主机的 `localhost` 仅解析到 IPv6 `::1`，而 MySQL/MariaDB 常只监听 IPv4，面板会以 `dial tcp [::1]:3306: connection refused` 连不上库（2026-08-25 生产事故中即为该报错形态）。已有安装不受影响；如需修改，编辑 `/etc/frp-panel/config.yaml` 的 `mysql.host`。

## v1.2.0 — 2026-08-25

首个 GitHub 公开版本（包含未单独发布的 v1.1.1 全部修复）。

### 新增 —— 在线更新（面板 + Agent）
- **面板一键自升级**：「设置 → 检查更新」发现新版本后点「立即在线升级」——面板从更新源下载 `frpanel-panel-<arch>`，比对 `sha256sums.txt` 通过后原子替换自身二进制并优雅重启（systemd `Restart=always` 拉起新版本）。重启数秒内网页短暂不可用、隧道随 frpc 重建闪断后自动恢复；页面探测 `/healthz` 后自动刷新。
- **Agent 远程在线升级**：节点抽屉中 Agent 版本落后于面板时显示「升级到 vX」按钮。面板经已认证的控制链路下发 `update_agent`，Agent 自行下载校验、原子替换、干净退出由 systemd 拉起新版本后回连。**frps 与既有隧道全程不中断**（数据面独立于 Agent 进程）。升级失败以 `agent_update_failed` 事件写入操作日志，原二进制不受影响。
- **完整性校验链**：所有在线升级二进制均须通过发布物 `sha256sums.txt` 的 SHA-256 校验，不匹配即放弃且不触碰现有二进制；下载体积上下限防护（<1MB 判定不完整）。
- **镜像回退**：新设置项「GitHub 镜像前缀」（`update_mirror`，ghproxy 风格）；检查更新、面板自升级、Agent 升级的下载在直连失败时自动走镜像重试。
- 协议版本升至 **2**（新增 `update_agent`/`update_res`）。旧 Agent 按协议容忍规则忽略未知消息；面板对 proto<2 的节点明确提示需手动升级一次。
- 新增 `internal/selfupdate` 共享包及单元测试（校验清单解析、坏校验拒绝且不触碰目标、原子替换、镜像回退、直连错误透传）。

## v1.1.1 — 2026-08-25

由一次线上事故触发：某台面板重启时本机 MySQL 尚未就绪，面板此后连续 5 天不与任何节点建立控制连接、不起任何 frpc（所有隧道从面板视角全断），但 Web API 却已随 DB 自行恢复而可用——问题被长期掩盖。

### 修复 —— 启动自愈（本次事故根因）
- **面板启动时 DB 不可用会永久放弃拉起节点**：`NodeManager.StartAll` 在 `ListNodes` 失败时直接 `return` 且没有任何重试。面板其余部分都按「DB 可降级启动、随后自愈」设计，唯独节点控制面与 frpc 数据面在这里被静默地永久关停，只能靠人工重启面板恢复。现改为：首次加载失败后转入后台每 5 秒重试，DB 一旦可达即拉起全部节点连接并生成 frpc 配置（日志记录 `nodes started after DB became reachable`）。已在生产环境复现事故并验证自愈。
- **systemd 单元补齐 DB 启动顺序**：`install-panel.sh` 生成的 `frpanel-panel.service` 由仅 `After=network-online.target` 改为追加 `mysqld.service mariadb.service mysql.service`（`After=` 对不存在的单元为无害空操作，远程 DB 场景不受影响），从源头消除「面板早于本机 DB 启动」的竞态窗口。

### 修复 —— 内存安全
- **`cfg.PanelName` 数据竞争**：面板名是运行期可变字段，被登录/`/me`/设置接口与实时 Hub 的 `sendInit` 并发读取，同时 `handleUpdateSettings` 会写它——对一个裸 string 字段（指针+长度两字）的并发读写属数据竞争、可能读到撕裂值。改为 `atomic.Pointer[string]` 存取。
- **认证限流器 map 无界增长（内存泄漏）**：agent 的 8443 直接暴露公网、常年被扫描，`authLimiter.fails` 会为每个「只失败一两次便再不出现」的扫描 IP 永久留一条记录（`Blocked`/`Success` 仅在该 IP 再次到来时才清理），面板登录限流器同理。现每个窗口至多清扫一次过期失败记录与失效封禁，map 规模收敛到「窗口内出现过的 IP」。
- **面板 `linkLat`/`ctrlLat` 残留**：节点删除后其链路延迟缓存条目不再清理，随删改积累。删除节点时一并清空该节点相关条目。
- **agent `lastTun` 残留**：映射端口变更/移除后旧 `proto:port` 计数键永不回收。每次采样按 frps 当前代理集合裁剪，规模随实际隧道数收敛。

### 安全
- **数据库备份不再把密码写进命令行**：`mysqldump` 原以 `--password=` 传参，同机任何用户可经 `ps`/`/proc` 窥得 DB 密码。改为写入 0600 临时 `--defaults-extra-file`，用后即删。

### 测试
- 新增 `internal/agent/ratelimit_test.go`、`internal/panel/auth_limiter_test.go`：覆盖限流器过期清扫、封禁在清扫中存活至到期、map 规模收敛。
- 全套测试在 `go test -race` 下通过（Linux/amd64 真实环境执行，含面板与 agent 全部包）。

### 分发
- 分发渠道迁移至 GitHub Releases（`https://github.com/PigFully/FRP-Panel/releases/latest/download`），原自建分发服务器下线。安装脚本默认 BASE 同步更新；CI 发布时自动打包钉死的 frp v0.61.1（frpc/frps，amd64+arm64），`sha256sums.txt` 覆盖全部 8 个二进制。

## v1.1.0 (Stable) — 2026-08-20

稳定版。合并了 2026-08-20 排障期间的全部修复（其间的 v1.0.2 / v1.0.3 / v1.0.4 均为过程构建，未作为正式版本发布）。

### 修复 —— 控制面 / 请求解码
- **编辑节点 / 编辑映射 / 启停映射一律报「请求体格式错误」**：前端把资源 id 同时放进了 URL 路径和请求体，而面板用 `DisallowUnknownFields` 解码，多出来的 `id` 字段导致解码失败。三个接口（`PUT /nodes/{id}`、`PUT /mappings/{id}`、`POST /mappings/{id}/toggle`）全部受影响。id 现在只走路径。新增回归测试，锁定前端实际发送的请求体与后端结构体一致。
- **解码错误现在会指明具体字段**：原先所有解码失败都归并为「请求体格式错误」，无法定位。现在未知字段会提示字段名与「前端与面板版本可能不一致」。
- **`NodeConn.conn` 数据竞争**：`session()` 每次重连都重新赋值该字段，而 `request()` 从 HTTP 处理协程读取，无任何同步。改为 `atomic.Pointer`；心跳协程改为接收它自己那条连接（不再读字段），因此重连不会让它写到新链路上；输掉竞争的请求会干净地失败为「与节点通信失败」。

### 修复 —— 节点防火墙适配
- **节点 ufw 适配在首次生效后即失效**（v1.0.1 引入）：两个缺陷叠加 —— `ufw delete` 缺少 `--force` 会等待 y/n 确认，Agent 无 stdin 故必然失败并跳过后续规则重建；且规则编号按列解析，ufw 编号右对齐补空格（`[ 9]` 但 `[10]`），超过 9 条规则后取错列。实测节点日志持续 `apply ratelimit failed: ufw sync: exit status 1`。现改为按规则内容（spec）增删、`--force` 删除、且失败不再中断后续步骤。
- **ufw 规则不再反复重建**：改为与现有规则求差集，端口集未变化时不下发任何 ufw 命令（此前每次控制链路重连、每次映射变更都全量删除再添加），并且先补齐缺失规则再移除多余规则，消除映射端口被自身同步短暂关闭的窗口。
- 卸载脚本 `install-agent.sh uninstall` 的 ufw 清理存在同样的编号解析与确认提示问题，一并修复。
- Agent 处理 `set_ratelimit` 的超时从 15s 放宽到 60s，避免首次在 ufw 主机上同步多个端口时被中途截断。

### 修复 —— 流量记账 / 稳定性
- **WAL 补报被静默截断，exactly-once 流量统计实际不成立**：补报与 5 秒实时采样共用同一个发送通道，而面板的 `(node,seq)` 跟踪器只接受严格递增的 seq —— 补报期间任何一条实时样本插进来，都会把水位线直接顶到最新 seq，其后所有（更旧的）补报记录全部被判为 stale 丢弃。断线超过 5 秒的补报必然被截断。此外补报走的是「队列满即丢弃」的发送路径，128 槽缓冲之外的记录会静默消失。

  现在每个会话增加 `ready` 门控：面板重连后先按严格升序完成 WAL 重放，期间实时样本对该会话暂不下发（不会丢失 —— 每条样本都是先落 WAL 再广播，下一轮追平时补出去）；重放采用阻塞发送，不再丢弃；追平循环收敛后才放行实时样本。事件消息（frps 上下线）不带 seq，不受门控影响。

- **Agent 在补报中途被断开会 panic（进程崩溃）**：会话结束时直接 `close(sess.send)`，而补报协程是并发发送方 —— 向已关闭的 channel 发送即使在 `select` 中也会 panic。改为关闭独立的 `done` channel，发送通道永不关闭。

- **DB 写失败时提交水位线仍然前进**：`flush` 无论批次是否写入成功都会持久化 `last_commit_seq`。MySQL 抖动时流量行被丢弃、水位线却越过了这些记录，Agent 的 WAL 再也不会重放它们 —— 永久丢失，同时也堵死了上一条的恢复通路。现在按节点跟踪写入结果，失败的节点不推进其水位线；该节点会在面板下次重启（跟踪器重新从该值播种）时由 WAL 补回。

- **`NodeConn.conn` 数据竞争**：`session()` 每次重连都重新赋值该字段，而 `request()` 从 HTTP 处理协程读取，无任何同步。改为 `atomic.Pointer`；心跳协程改为接收它自己那条连接（不再读字段），因此重连不会让它写到新链路上；输掉竞争的请求会干净地失败为「与节点通信失败」。

- **WAL 重放不再把整个日志读进内存**：`Backfill` 原先一次性返回全部记录切片，7 天保留下约 12 万条（≈18MB JSON），与「稳态 RSS ≈ 10MB」的内存纪律冲突。改为流式 `Stream(afterSeq, fn)`，并跳过（进程被杀导致的）尾部残行而不中断其余重放。

- 移除 `pipeline.flush` 中的 `indexOf` 线性回查：它在找不到时返回 0，会把水位线写到错误的节点上。
- **`隧道 tcp:0 状态变更为 offline` 伪日志**：frps 对无 conf 块的 proxy 不返回远端端口，落到面板即 `remote_port=0`，会写入无意义的操作日志并执行匹配零行的更新。现跳过该类条目（其流量增量仍计入节点总量）。

### 测试
- 新增 `internal/panel/handlers_decode_test.go`：把前端实际发送的 9 种请求体逐一喂给对应结构体，任何一侧再漂移即失败。
- 新增 `internal/agent/firewall_test.go`：以生产节点真实 `ufw status numbered` 输出为夹具，覆盖 1/2/3 位规则编号，并断言安装脚本自己的 `# frpanel agent mgmt`、`# frps bind` 标记绝不会被 Agent 当成自己的规则删除。
- 新增 `internal/agent/wal_test.go` 与 `internal/agent/backfill_test.go`：覆盖 seq 过滤/升序/提前终止/残行跳过/重启后 seq 单调/rotate；以及重放完整性与严格升序（1000 条记录穿过 8 槽缓冲，验证无丢弃）、水位线续传、实时样本门控与放行、中途断开不 panic、`close()` 幂等。
- 全套测试在 `go test -race` 下通过（含上述并发路径 20 次重复压测）。

### 其他
- 清理无用赋值（`frpcmgr.go` 的 `proc`、`server.go` 的 `fver`）。

## v1.0.1 — 2026-08-19

### 修复
- **节点 ufw/iptables 主机适配**：Agent 的 `inet frpanel` nftables 表在 ufw（iptables-nft 后端）主机上无法先于 ufw 默认 DROP 放行映射端口（实测优先级 -10 仍被 ufw 拦截）。Agent 现会在编程 nftables 表的同时为每个映射端口同步添加 `ufw allow` 规则（以 `# frpanel-agent` 注释标记，卸载/释放端口时自动移除）。纯 nftables 主机上行为不变（无 ufw 时跳过适配，不产生副作用）。

## v1.0.0 — 2026-08-19

首个正式版本。

### 核心
- 多节点 FRP 管理面板：Panel（Go，内嵌 React SPA，管理本机多个 frpc）+ Agent（Go，管理 frps、上报指标、端口预检）。
- 一个本地端口可同时映射到多个公网节点（国内 + 国外），每条映射公网端口自定义。
- 面板主动向外发起、与每个 Agent 维持一条 WSS 长连接；TLS 证书指纹 pinning + 每节点独立 Token + 时间戳防重放。
- proxy 增删改走 `frpc reload` 热加载；frps 地址/Token 变更走重启路径。
- 断线重连状态对账（DB 为唯一真源）；流量 `(node, seq)` 幂等去重 + WAL 补报（exactly-once）。
- 三层指标管道：内存环形缓冲 → 分钟聚合落库 → 小时/天 rollup；节点带宽与隧道流量两套口径分离。
- 浏览器实时通道（WebSocket 增量推送），断线降级为轮询。
- MySQL 故障降级：面板不退出，实时通道 / frpc 监督 / 节点连接继续运行。

### 安全
- frps `allowPorts` 白名单 + 保留段排除（22/7000/8443/7400–7500）三层防线；frps dashboard 仅监听 127.0.0.1。
- JWT（HttpOnly + SameSite=Lax）+ `pwd_version` 吊销 + CSRF 双提交 + 登录限流 + bcrypt。
- 统一安全响应头（CSP / X-Frame-Options / X-Content-Type-Options）；可选面板自签 TLS。
- **最大回源数限制**：每个公网端口每秒新建 TCP 连接上限（默认 200），由节点独立 `nftables` 表 `inet frpanel` 执行，防止极端 TCP 攻击导致节点 IP 被误判为 PCDN / 对外攻击。

### 前端 / 体验
- React 19 + Vite + TS + MUI v7 + ECharts（按需引入）+ TanStack Query + React Router。
- 路由级懒加载 + chunk 预取；HTTP/2（h2c）+ gzip + 静态资源不可变缓存，提升加载速度。
- 深/浅主题、可折叠侧边栏、骨架屏、空状态、错误边界、Toast、响应式；字体本地打包，零外部 CDN。
- 节点 TCP 延迟与**完整 FRP 链路延迟**（往返探测）显示，定时刷新，带说明提示。

### 运维
- 一键安装脚本（面板 Debian 13 / 节点 Ubuntu 22.04，均兼容对方大版本），镜像加速与 `--mirror`，sha256 校验。
- 自动启用 BBR 拥塞控制；systemd `Type=notify` + watchdog + `Restart=always`；`GOMEMLIMIT`/`GOGC`/`MemoryMax` 内存控制（稳态 RSS ≈ 10 MB）。
- 面板安装可自定义监听端口（回车随机）；`frppanel` 命令随时回看安装信息 / 重置密码 / 管理服务。
- 一键卸载（仅移除本项目组件，绝不影响宿主既有服务）；`--upgrade` 保留配置与数据库增量迁移。
- MySQL 备份（保留最近 7 份）与检查更新。
