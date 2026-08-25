<div align="center">

# FRPanel — 多节点 FRP 管理面板

把家中/内网的本地服务，通过 FRP 内网穿透，一键映射到多台具备独立公网 IP 的云节点（国内 + 国外）对外提供访问。

**一个本地端口，可同时映射到多个公网 IP，每条映射的公网端口自定义。**

</div>

---

## 架构

```
家中 Debian 13（无公网 IP）                    公网云节点 × N（Ubuntu 22.04，各自独立公网 IP）
┌─────────────────────────────┐               ┌──────────────────────────────┐
│  Panel（Go 单二进制）         │  WSS 出站连接  │  Agent（Go 单二进制）           │
│  ├─ Web UI（内嵌 React SPA） │ ────────────► │  ├─ 管理端口 :8443（TLS+Token）│
│  ├─ MySQL（用户自备）        │  心跳/指令/指标 │  ├─ 管理本机 frps（:7000）      │
│  └─ 管理本机多个 frpc 实例    │               │  └─ 上报 CPU/内存/带宽/流量     │
│     frpc ──────────────────┼── frp 隧道 ──► │  frps 绑定用户指定的公网端口     │
└─────────────────────────────┘               └──────────────────────────────┘
```

- **方向约束**：面板无公网 IP，所有连接一律由面板主动向外发起。面板 ↔ 每个 Agent 维持一条面板发起的 WSS 长连接，指令/心跳/指标复用这条连接。
- **数据面独立于控制面**：`frpc ↔ frps` 隧道独立运行，面板 ↔ Agent 控制连接中断时，已建立的隧道不受影响。
- **DB 为唯一真源**：每次（重）连接执行状态对账（reconciliation），差异自动修复并记入操作日志。

## 功能

- 多节点管理、一键安装脚本（含注册回执自动解析）、在线/离线状态实时推送。
- 映射管理：一个本地端口 → 多个（节点，公网端口）目标；公网端口占用实时预检（前端拦截 + Agent 预检 + frps allowPorts 三层防线）；本地端口 LISTEN 前置校验。
- 数据概览：节点/映射统计、实时带宽曲线（WS 增量推送，节点整机与隧道两套口径分离）、流量 Top、最近日志。
- 节点 **TCP 延迟**与**完整 FRP 链路延迟**（经 frps→frpc→本地服务的往返探测）显示，可配置刷新间隔。
- 操作日志（FRP 事件 / 对账修复 / 面板操作，服务端分页过滤）、面板设置（改名、改密即时吊销旧会话、检查更新、清理日志、MySQL 备份）。
- 深/浅主题、可折叠侧边栏、骨架屏、空状态、错误边界、Toast、响应式、字体本地打包（零外部 CDN）。

## 安全

- TLS 证书指纹 pinning + 每节点独立随机 Token（HMAC 签名）+ 时间戳防重放（±60s，依赖 timesyncd）。
- frps `allowPorts` 白名单并排除保留段（22/7000/8443/7400–7500）；frps dashboard 仅 127.0.0.1。
- JWT（HttpOnly + SameSite=Lax）+ `pwd_version` 吊销 + CSRF 双提交 + 登录限流 + bcrypt；统一安全响应头（CSP 等）。
- **最大回源数限制**：每个公网端口每秒新建 TCP 连接上限（默认 200），由节点独立 `nftables inet frpanel` 表执行，防止极端 TCP 攻击导致节点 IP 被误判为 PCDN / 对外攻击。**该表独立，绝不触碰宿主既有防火墙规则**。

## 安装

> 二进制与脚本由 [GitHub Releases](https://github.com/PigFully/FRP-Panel/releases) 分发（默认 `BASE = https://github.com/PigFully/FRP-Panel/releases/latest/download`）。自建分发时用 `--base-url <url>`（或环境变量 `FRPANEL_BASE_URL`）覆盖。

### 1. 面板（家中 Debian 13，root）

```bash
curl -fsSL https://github.com/PigFully/FRP-Panel/releases/latest/download/install-panel.sh | bash
```

- 交互询问：数据库连接地址（默认 `127.0.0.1`）、端口（默认 `3306`）、库名、账户、密码，以及面板监听端口（回车随机高位端口）。
- 自动测试连接、迁移、创建 `admin`（随机 16 位密码，仅显示一次）、启用 BBR、安装 systemd 服务。
- 结束输出访问地址与端口；随时在 SSH 输入 `frppanel` 可回看安装信息，`frppanel resetpw` 重置管理员密码。

### 2. 节点（云服务器 Ubuntu 22.04，root）

在面板「节点列表 → 添加节点」弹窗中复制一键命令，到目标服务器执行：

```bash
curl -fsSL https://github.com/PigFully/FRP-Panel/releases/latest/download/install-agent.sh | bash
```

脚本自动下载校验、启用时间同步与 BBR、生成 Token 与自签证书、安装 frps + Agent 服务，并输出一段 **Base64 注册回执**。把回执粘回面板弹窗，点「验证并添加」即可。

中国网络：脚本自动探测直连，失败可加 `--mirror <ghproxy 前缀>`；亦可 `--base-url <url>` 指定分发地址。

## 卸载 / 升级

```bash
BASE=https://github.com/PigFully/FRP-Panel/releases/latest/download
# 节点卸载（仅移除本项目组件，不影响宿主既有服务）
curl -fsSL $BASE/install-agent.sh | bash -s -- uninstall
# 面板升级（保留配置与数据库，自动增量迁移）
curl -fsSL $BASE/install-panel.sh | bash -s -- --upgrade
# Agent 升级：重跑安装脚本即覆盖升级，保留 Token 与证书，映射不中断
```

## 开发与构建

```bash
make build     # 构建 web(SPA) + panel + agent（前端经 go:embed 打进面板二进制）
make test      # 单元测试（/proc 解析、端口校验、TOML 生成、回执编解码、seq 幂等去重…）
make dist      # 多架构二进制 + 安装脚本 + sha256（另需放入 frpc/frps 对应架构二进制）
```

- 后端 Go ≥ 1.25，纯静态单二进制（CGO 关闭）。前端 Node 24 + pnpm + Vite；仅构建期依赖 Node，运行时零 Node 依赖。
- frp 版本钉死 v0.61.x（TOML 配置）。数据库 MySQL/MariaDB（用户自备）。
- 内存纪律：固定长度环形缓冲、有界发送队列、ticker 及时 Stop、连接 goroutine 随 context 回收；`GOMEMLIMIT`/`GOGC`/`MemoryMax` 兜底，稳态 RSS ≈ 10 MB。

## 常见问题

- **端口被占用**：添加/编辑映射时公网端口实时预检并提示占用进程；落在保留段前端即拦截。
- **节点离线排查**：确认云服务器 8443 出/入站放行、`frps`/`frpanel-agent` 服务状态、时钟是否同步（防重放依赖）。
- **时钟不同步**：`timedatectl set-ntp true`，安装脚本会自动启用 `systemd-timesyncd`。
- **镜像下载失败**：`--mirror <前缀>` 指定 GitHub 镜像，或 `--base-url` 指向自建分发。
- **局域网 HTTP 嗅探风险**：可在面板 `config.yaml` 启用 `tls`。

## 目录结构

```
panel/    面板主程序（Go）        agent/   节点 Agent（Go）
web/      React SPA（go:embed）   internal/ 共享与各子系统（protocol/portutil/frpcfg/metrics/…）
scripts/  install-panel.sh / install-agent.sh
```
