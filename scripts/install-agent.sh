#!/usr/bin/env bash
# FRPanel 节点 Agent 一键安装脚本
#   安装:   curl -fsSL <base>/install-agent.sh | bash
#   卸载:   curl -fsSL <base>/install-agent.sh | bash -s -- uninstall
#   自定义: --base-url <url> 指定分发地址；--mirror <url> 指定 GitHub 镜像前缀；--ip <ip> 手动指定公网IP
# 目标系统: Ubuntu 22.04（兼容 Debian 12/13）。重复执行 = 升级（保留 Token/证书/映射）。
set -euo pipefail

BASE_URL_DEFAULT="https://github.com/PigFully/FRP-Panel/releases/latest/download"
DATADIR="/opt/frp-agent"
BASE_URL="${FRPANEL_BASE_URL:-$BASE_URL_DEFAULT}"
MIRROR=""
MANUAL_IP=""
ACTION="install"

while [ $# -gt 0 ]; do
  case "$1" in
    uninstall) ACTION="uninstall" ;;
    --base-url) BASE_URL="$2"; shift ;;
    --mirror) MIRROR="$2"; shift ;;
    --ip) MANUAL_IP="$2"; shift ;;
    *) echo "未知参数: $1" ;;
  esac
  shift
done

log() { echo -e "\033[1;34m[frpanel]\033[0m $*"; }
err() { echo -e "\033[1;31m[frpanel]\033[0m $*" >&2; }
[ "$(id -u)" = "0" ] || { err "请以 root 运行"; exit 1; }

# 启用 BBR 拥塞控制（提升跨境隧道吞吐）。写入独立 sysctl 片段，可随卸载移除。
enable_bbr() {
  if [ "$(sysctl -n net.ipv4.tcp_congestion_control 2>/dev/null)" = "bbr" ]; then
    log "BBR 已启用"; return
  fi
  modprobe tcp_bbr 2>/dev/null || true
  cat >/etc/sysctl.d/99-frpanel-bbr.conf <<'EOF'
net.core.default_qdisc = fq
net.ipv4.tcp_congestion_control = bbr
EOF
  sysctl --system >/dev/null 2>&1 || sysctl -p /etc/sysctl.d/99-frpanel-bbr.conf >/dev/null 2>&1 || true
  local cc; cc="$(sysctl -n net.ipv4.tcp_congestion_control 2>/dev/null)"
  if [ "$cc" = "bbr" ]; then log "已启用 BBR 拥塞控制"; else log "BBR 设置已写入（当前: ${cc:-未知}，重启后生效或需内核支持）"; fi
}

if [ "$ACTION" = "uninstall" ]; then
  log "卸载 FRPanel Agent…"
  systemctl disable --now frpanel-agent 2>/dev/null || true
  systemctl disable --now frps 2>/dev/null || true
  rm -f /etc/systemd/system/frpanel-agent.service /etc/systemd/system/frps.service
  systemctl daemon-reload 2>/dev/null || true
  nft delete table inet frpanel 2>/dev/null || true   # 仅删除本项目的独立表，不影响其它防火墙规则
  # 移除 Agent / 安装脚本曾添加的 ufw 放行规则（# frpanel 标记）。
  # 按规则内容(spec)删除而非按编号：ufw 的编号右对齐补空格（"[ 9]" 但 "[10]"），
  # 按列取字段在规则超过 9 条后会取错列；且逐条删除时编号会重排。
  # --force 必需：否则 ufw delete 会等待 y/n 确认，在无终端环境下直接失败。
  if command -v ufw >/dev/null 2>&1 && ufw status 2>/dev/null | grep -q "Status: active"; then
    ufw status numbered 2>/dev/null | grep '# frpanel' \
      | sed -nE 's/^\[[[:space:]]*[0-9]+\][[:space:]]+([^[:space:]]+).*/\1/p' \
      | sort -u | while read -r spec; do
        [ -n "$spec" ] && ufw --force delete allow "$spec" >/dev/null 2>&1 || true
      done
    ufw reload >/dev/null 2>&1 || true
    log "已清理 ufw 放行规则"
  fi
  rm -f /etc/sysctl.d/99-frpanel-bbr.conf
  rm -rf "$DATADIR"
  log "卸载完成，未触碰系统其它服务。"
  exit 0
fi

ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64) GOARCH="amd64" ;;
  aarch64|arm64) GOARCH="arm64" ;;
  *) err "不支持的架构: $ARCH"; exit 1 ;;
esac

# 下载：优先直连 base-url；失败时尝试镜像（若提供）。带 5s 直连探测。
fetch() { # fetch <path> <out>
  local path="$1" out="$2" url="${BASE_URL%/}/$1"
  if curl -fsSL -m 60 -o "$out" "$url"; then return 0; fi
  if [ -n "$MIRROR" ]; then
    log "直连失败，尝试镜像…"
    curl -fsSL -m 90 -o "$out" "${MIRROR%/}/$url" && return 0
  fi
  err "下载失败: $url"; return 1
}

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT
log "从 $BASE_URL 下载组件 (arch=$GOARCH)…"
fetch "frpanel-agent-$GOARCH" "$TMP/agent"
fetch "frps-$GOARCH" "$TMP/frps"
if fetch "sha256sums.txt" "$TMP/sums" 2>/dev/null; then
  ( cd "$TMP" && grep -E "frpanel-agent-$GOARCH|frps-$GOARCH" sums | sed "s#frpanel-agent-$GOARCH#agent#; s#frps-$GOARCH#frps#" | sha256sum -c - ) \
    && log "sha256 校验通过" || { err "sha256 校验失败"; exit 1; }
else
  log "未找到 sha256sums.txt，跳过校验"
fi

mkdir -p "$DATADIR"
install -m 755 "$TMP/agent" "$DATADIR/agent"
install -m 755 "$TMP/frps" "$DATADIR/frps"

# 时钟同步（防重放依赖正确时钟）
log "确保 systemd-timesyncd 已启用…"
timedatectl set-ntp true 2>/dev/null || true
systemctl enable --now systemd-timesyncd 2>/dev/null || true

enable_bbr

UPGRADE=0
[ -f "$DATADIR/config.json" ] && UPGRADE=1

log "初始化 Agent（生成/保留 Token 与证书）…"
INIT_ARGS=(init -datadir "$DATADIR" -bind :8443)
[ -n "$MANUAL_IP" ] && INIT_ARGS+=(-ip "$MANUAL_IP")
"$DATADIR/agent" "${INIT_ARGS[@]}"

cat >/etc/systemd/system/frps.service <<EOF
[Unit]
Description=frps (FRPanel node)
After=network-online.target
Wants=network-online.target
[Service]
Type=simple
ExecStart=$DATADIR/frps -c $DATADIR/frps.toml
Restart=always
RestartSec=3
LimitNOFILE=1048576
[Install]
WantedBy=multi-user.target
EOF

cat >/etc/systemd/system/frpanel-agent.service <<EOF
[Unit]
Description=FRPanel Agent
After=network-online.target frps.service
Wants=network-online.target
[Service]
Type=notify
ExecStart=$DATADIR/agent run -datadir $DATADIR
Restart=always
RestartSec=3
WatchdogSec=30
LimitNOFILE=1048576
# 内存控制：软上限 + 更积极 GC，保持 Agent 轻量、防内存膨胀
Environment=GOMEMLIMIT=256MiB
Environment=GOGC=50
MemoryMax=384M
[Install]
WantedBy=multi-user.target
EOF

# ufw 主机上放行 frps 隧道端口与 Agent 管理端口（仅在 ufw 启用时执行，
# 否则 Agent 安装后端口被 ufw 默认 DROP 拦截导致节点不可达）
if command -v ufw >/dev/null 2>&1 && ufw status 2>/dev/null | grep -q "Status: active"; then
  ufw allow 7000/tcp comment "frpanel frps" >/dev/null 2>&1 || true
  ufw allow 8443/tcp comment "frpanel agent" >/dev/null 2>&1 || true
  ufw reload >/dev/null 2>&1 || true
  log "已放行 ufw: 7000/tcp (frps) 8443/tcp (Agent)"
fi

systemctl daemon-reload
systemctl enable --now frps
sleep 1
systemctl restart frpanel-agent
systemctl enable frpanel-agent >/dev/null 2>&1 || true
sleep 2

if [ "$UPGRADE" = "1" ]; then
  log "升级完成。frps=$(systemctl is-active frps) agent=$(systemctl is-active frpanel-agent)。映射不中断。"
else
  log "安装完成。frps=$(systemctl is-active frps) agent=$(systemctl is-active frpanel-agent)。"
  echo
  "$DATADIR/agent" receipt -datadir "$DATADIR"
fi
