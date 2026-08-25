export function fmtBytes(n: number): string {
  if (!n || n < 0) return '0 B'
  const u = ['B', 'KB', 'MB', 'GB', 'TB', 'PB']
  let i = 0
  let v = n
  while (v >= 1024 && i < u.length - 1) {
    v /= 1024
    i++
  }
  return `${v.toFixed(v < 10 && i > 0 ? 2 : v < 100 && i > 0 ? 1 : 0)} ${u[i]}`
}

export function fmtBps(n: number): string {
  return `${fmtBytes(n)}/s`
}

export function fmtTime(iso: string | null): string {
  if (!iso) return '—'
  const d = new Date(iso)
  if (isNaN(d.getTime())) return '—'
  return d.toLocaleString(undefined, { hour12: false })
}

export function fmtTimeShort(ts: number): string {
  const d = new Date(ts)
  return d.toLocaleTimeString(undefined, { hour12: false, hour: '2-digit', minute: '2-digit', second: '2-digit' })
}

export function regionLabel(r: string): string {
  return r === 'domestic' ? '国内' : '国外'
}

const pad = (n: number) => String(n).padStart(2, '0')

// Axis tick label, format chosen from the visible time span.
export function fmtAxisTime(ts: number, spanMs: number): string {
  const d = new Date(ts)
  // Multi-day spans show only the date; the 24h view shows only the time (the
  // date is obvious from context) so labels stay short and unambiguous.
  if (spanMs >= 2 * 86400000) return `${pad(d.getMonth() + 1)}-${pad(d.getDate())}`
  if (spanMs >= 3600000) return `${pad(d.getHours())}:${pad(d.getMinutes())}`
  return `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
}

// Tooltip timestamp, more precise than the axis label.
export function fmtTipTime(ts: number, spanMs: number): string {
  const d = new Date(ts)
  if (spanMs >= 86400000) return `${pad(d.getMonth() + 1)}-${pad(d.getDate())} ${pad(d.getHours())}:${pad(d.getMinutes())}`
  return `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`
}
