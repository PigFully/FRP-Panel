// Mirrors internal/portutil reserved-segment policy for instant client-side
// interception (first line of the three-layer defense).
export function reservedReason(port: number): string {
  if (port === 22) return '该端口为 SSH 保留端口（22）'
  if (port === 7000) return '该端口为 frps 绑定端口（7000）'
  if (port === 8443) return '该端口为 Agent 管理端口（8443）'
  if (port >= 7400 && port <= 7500) return '该端口落在 frpc admin 保留段（7400-7500）'
  if (port < 1024) return '请使用 1024 以上端口'
  return ''
}

export function validPort(port: number): boolean {
  return Number.isInteger(port) && port >= 1 && port <= 65535
}
