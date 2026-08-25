#!/usr/bin/env bash
# FRPanel 面板一键安装脚本（目标: Debian 13，兼容 Debian 12）
#   安装: curl -fsSL <base>/install-panel.sh | bash
#   升级: curl -fsSL <base>/install-panel.sh | bash -s -- --upgrade
#   非交互: 预置 FRPANEL_MYSQL_HOST/PORT/DB/USER/PASS 环境变量即可跳过提问
set -euo pipefail

BASE_URL_DEFAULT="https://github.com/PigFully/FRP-Panel/releases/latest/download"
BASE_URL="${FRPANEL_BASE_URL:-$BASE_URL_DEFAULT}"
MIRROR=""
DATADIR="/opt/frp-panel"
CFG="/etc/frp-panel/config.yaml"
UPGRADE=0

while [ $# -gt 0 ]; do
  case "$1" in
    --upgrade) UPGRADE=1 ;;
    --base-url) BASE_URL="$2"; shift ;;
    --mirror) MIRROR="$2"; shift ;;
  esac
  shift
done

log() { echo -e "\033[1;34m[frpanel]\033[0m $*"; }
err() { echo -e "\033[1;31m[frpanel]\033[0m $*" >&2; }
[ "$(id -u)" = "0" ] || { err "请以 root 运行"; exit 1; }

enable_bbr() {
  if [ "$(sysctl -n net.ipv4.tcp_congestion_control 2>/dev/null)" = "bbr" ]; then
    log "BBR 已启用"; return
  fi
  modprobe tcp_bbr 2>/dev/null || true
  cat >/etc/sysctl.d/99-frpanel-bbr.conf <<'EOF'
net.core.default_qdisc = fq
net.ipv4.tcp_congestion_control = bbr
EOF
  sysctl --system >/dev/null 2>&1 || true
  log "BBR 拥塞控制: $(sysctl -n net.ipv4.tcp_congestion_control 2>/dev/null)"
}

write_panel_unit() {
  cat >/etc/systemd/system/frpanel-panel.service <<EOF
[Unit]
Description=FRPanel Panel
After=network-online.target mysqld.service mariadb.service mysql.service
Wants=network-online.target
[Service]
Type=notify
ExecStart=$DATADIR/panel run -config $CFG
Restart=always
RestartSec=3
WatchdogSec=30
LimitNOFILE=1048576
Environment=GOMEMLIMIT=512MiB
Environment=GOGC=60
MemoryMax=768M
[Install]
WantedBy=multi-user.target
EOF
}

# write_helpers regenerates install-info.txt + the `frppanel` command.
write_helpers() {
  cat >"$DATADIR/install-info.txt" <<EOF
============= FRPanel 面板安装信息 =============
  访问地址 : http://${IP:-<本机内网IP>}:${LPORT}
  监听端口 : ${LPORT}
  管理员账户: admin
  配置文件 : ${CFG}
  分发地址 : ${BASE_URL}
  服务管理 : systemctl {status|restart|stop} frpanel-panel
  重置密码 : frppanel resetpw
  管理员密码仅在安装时显示一次；如遗忘请执行 frppanel resetpw 重置
===============================================
EOF
  chmod 600 "$DATADIR/install-info.txt"
  cat >/usr/local/bin/frppanel <<'FEOF'
#!/usr/bin/env bash
CFG=/etc/frp-panel/config.yaml
case "${1:-info}" in
  resetpw) /opt/frp-panel/panel reset-admin -config "$CFG" ;;
  restart) systemctl restart frpanel-panel && echo "已重启 frpanel-panel" ;;
  status)  systemctl status frpanel-panel --no-pager ;;
  logs)    journalctl -u frpanel-panel -n 120 --no-pager ;;
  *)
    [ -f /opt/frp-panel/install-info.txt ] && cat /opt/frp-panel/install-info.txt
    echo "  服务状态 : $(systemctl is-active frpanel-panel)"
    echo "  可用命令 : frppanel [info|resetpw|restart|status|logs]"
    ;;
esac
FEOF
  chmod 755 /usr/local/bin/frppanel
}

ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64) GOARCH="amd64" ;;
  aarch64|arm64) GOARCH="arm64" ;;
  *) err "不支持的架构: $ARCH"; exit 1 ;;
esac

fetch() {
  local path="$1" out="$2" url="${BASE_URL%/}/$1"
  if curl -fsSL -m 60 -o "$out" "$url"; then return 0; fi
  if [ -n "$MIRROR" ]; then curl -fsSL -m 90 -o "$out" "${MIRROR%/}/$url" && return 0; fi
  err "下载失败: $url"; return 1
}

TMP="$(mktemp -d)"; trap 'rm -rf "$TMP"' EXIT
log "从 $BASE_URL 下载组件 (arch=$GOARCH)…"
fetch "frpanel-panel-$GOARCH" "$TMP/panel"
fetch "frpc-$GOARCH" "$TMP/frpc"
if fetch "sha256sums.txt" "$TMP/sums" 2>/dev/null; then
  ( cd "$TMP" && grep -E "frpanel-panel-$GOARCH|frpc-$GOARCH" sums | sed "s#frpanel-panel-$GOARCH#panel#; s#frpc-$GOARCH#frpc#" | sha256sum -c - ) \
    && log "sha256 校验通过" || { err "sha256 校验失败"; exit 1; }
fi

mkdir -p "$DATADIR"
install -m 755 "$TMP/panel" "$DATADIR/panel"
install -m 755 "$TMP/frpc" "$DATADIR/frpc"

if [ "$UPGRADE" = "1" ]; then
  [ -f "$CFG" ] || { err "未找到 $CFG，无法升级"; exit 1; }
  log "升级：保留配置与数据库，刷新服务单元并重启（迁移自动执行）…"
  enable_bbr
  write_panel_unit
  LPORT="$(grep -E '^listen:' "$CFG" | grep -oE '[0-9]+' | tail -1)"
  IP="$(hostname -I 2>/dev/null | awk '{print $1}')"
  write_helpers
  systemctl daemon-reload
  systemctl restart frpanel-panel
  sleep 3
  log "升级完成。panel=$(systemctl is-active frpanel-panel)"
  cat "$DATADIR/install-info.txt" 2>/dev/null || true
  exit 0
fi

# ---- 全新安装：询问 MySQL 连接信息 ----
ask() { # ask VAR prompt default
  local var="$1" prompt="$2" def="${3:-}" cur
  cur="$(printenv "$var" || true)"
  if [ -n "$cur" ]; then eval "$var=\"\$cur\""; return; fi
  if [ -n "$def" ]; then read -rp "$prompt [$def]: " ans </dev/tty || true; ans="${ans:-$def}";
  else read -rp "$prompt: " ans </dev/tty || true; fi
  eval "$var=\"\$ans\""
}
asksecret() {
  local var="$1" cur; cur="$(printenv "$var" || true)"
  if [ -n "$cur" ]; then eval "$var=\"\$cur\""; return; fi
  read -rsp "数据库密码: " ans </dev/tty || true; echo; eval "$var=\"\$ans\""
}
log "请输入 MySQL 连接信息（数据库需已创建，直接回车使用括号内默认值）"
# 默认 127.0.0.1 而非 localhost：部分主机 localhost 仅解析到 ::1，而 MySQL 常只
# 监听 IPv4，会得到 "dial tcp [::1]:3306: connection refused"。
ask FRPANEL_MYSQL_HOST "数据库连接地址" "127.0.0.1"
ask FRPANEL_MYSQL_PORT "数据库端口（MySQL 默认 3306）" "3306"
ask FRPANEL_MYSQL_DB   "数据库名"   "frpanel"
ask FRPANEL_MYSQL_USER "数据库账户" "frpanel"
asksecret FRPANEL_MYSQL_PASS

# 面板监听端口：回车默认随机高位端口
ask FRPANEL_LISTEN_PORT "面板监听端口（回车默认随机高位端口）" ""
LPORT="${FRPANEL_LISTEN_PORT}"
if [ -z "$LPORT" ]; then
  for _ in $(seq 1 30); do
    cand=$(( RANDOM % 20000 + 30000 ))
    if ! ss -tlnH 2>/dev/null | awk '{print $4}' | grep -q ":${cand}$"; then LPORT="$cand"; break; fi
  done
  [ -z "$LPORT" ] && LPORT=8080
  log "已随机分配面板监听端口: $LPORT"
fi
IP="$(hostname -I 2>/dev/null | awk '{print $1}')"

JWT="$(head -c32 /dev/urandom | od -An -tx1 | tr -d ' \n')"
mkdir -p /etc/frp-panel
cat >"$CFG" <<EOF
listen: "0.0.0.0:$LPORT"
mysql:
  host: "$FRPANEL_MYSQL_HOST"
  port: $FRPANEL_MYSQL_PORT
  user: "$FRPANEL_MYSQL_USER"
  password: "$FRPANEL_MYSQL_PASS"
  database: "$FRPANEL_MYSQL_DB"
jwt_secret: "$JWT"
data_dir: /opt/frp-panel
frpc_bin: /opt/frp-panel/frpc
panel_name: "FRPanel"
update_base_url: "$BASE_URL"
tls:
  enabled: false
EOF
chmod 600 "$CFG"

log "测试数据库连接并执行迁移…"
if ! "$DATADIR/panel" migrate -config "$CFG"; then
  err "数据库连接/迁移失败，请检查 MySQL 信息后重试。配置已写入 $CFG"
  exit 1
fi

log "创建管理员账户…"
"$DATADIR/panel" ensure-admin -config "$CFG"

write_helpers

cat >/etc/systemd/system/frpanel-panel.service <<EOF
[Unit]
Description=FRPanel Panel
After=network-online.target mysqld.service mariadb.service mysql.service
Wants=network-online.target
[Service]
Type=notify
ExecStart=$DATADIR/panel run -config $CFG
Restart=always
RestartSec=3
WatchdogSec=30
LimitNOFILE=1048576
# 内存控制：软上限 + 更积极 GC，防止长时间运行内存膨胀
Environment=GOMEMLIMIT=512MiB
Environment=GOGC=60
MemoryMax=768M
[Install]
WantedBy=multi-user.target
EOF

enable_bbr
systemctl daemon-reload
systemctl enable --now frpanel-panel
sleep 3
echo
log "安装完成！panel=$(systemctl is-active frpanel-panel)"
cat "$DATADIR/install-info.txt"
echo "  提示：随时在 SSH 中输入 frppanel 可重新查看以上安装信息"
echo "  （局域网 HTTP 存在嗅探风险，建议在 config.yaml 启用 tls）"
