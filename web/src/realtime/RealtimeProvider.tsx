import { useQueryClient } from '@tanstack/react-query'
import { createContext, useContext, useEffect, useRef, useState, type ReactNode } from 'react'

export interface RTPoint {
  ts: number
  cpu: number
  mem: number
  rx: number
  tx: number
  tin: number
  tout: number
}

interface RTState {
  connected: boolean
  statuses: Record<number, string>
  global: RTPoint[]
  byNode: Record<number, RTPoint[]>
}

const CAP = 120
const Ctx = createContext<RTState>({ connected: false, statuses: {}, global: [], byNode: {} })
export const useRealtime = () => useContext(Ctx)

// RealtimeProvider maintains a single WebSocket to /api/ws, keeps bounded live
// series, and falls back to 10s query polling if the socket cannot stay up.
export function RealtimeProvider({ children }: { children: ReactNode }) {
  const qc = useQueryClient()
  const [state, setState] = useState<RTState>({ connected: false, statuses: {}, global: [], byNode: {} })
  const last = useRef<Record<number, RTPoint>>({})
  const byNode = useRef<Record<number, RTPoint[]>>({})
  const global = useRef<RTPoint[]>([])
  const statuses = useRef<Record<number, string>>({})
  const pollTimer = useRef<number | null>(null)
  const failCount = useRef(0)

  useEffect(() => {
    let ws: WebSocket | null = null
    let stopped = false
    let reconnectTimer: number | null = null

    const publish = () =>
      setState({
        connected: ws?.readyState === WebSocket.OPEN,
        statuses: { ...statuses.current },
        global: global.current.slice(),
        byNode: Object.fromEntries(Object.entries(byNode.current).map(([k, v]) => [k, v.slice()])),
      })

    const startPolling = () => {
      if (pollTimer.current) return
      pollTimer.current = window.setInterval(() => {
        qc.invalidateQueries({ queryKey: ['nodes'] })
        qc.invalidateQueries({ queryKey: ['overview'] })
        qc.invalidateQueries({ queryKey: ['mappings'] })
      }, 10000)
    }
    const stopPolling = () => {
      if (pollTimer.current) {
        clearInterval(pollTimer.current)
        pollTimer.current = null
      }
    }

    const pushMetric = (nodeId: number, p: RTPoint) => {
      last.current[nodeId] = p
      const arr = byNode.current[nodeId] || (byNode.current[nodeId] = [])
      arr.push(p)
      if (arr.length > CAP) arr.splice(0, arr.length - CAP)
      // Global timeline = sum of latest per-node values, sampled at arrival.
      let rx = 0, tx = 0, tin = 0, tout = 0
      for (const v of Object.values(last.current)) {
        rx += v.rx; tx += v.tx; tin += v.tin; tout += v.tout
      }
      global.current.push({ ts: p.ts, cpu: 0, mem: 0, rx, tx, tin, tout })
      if (global.current.length > CAP) global.current.splice(0, global.current.length - CAP)
    }

    const connect = () => {
      if (stopped) return
      const proto = location.protocol === 'https:' ? 'wss' : 'ws'
      ws = new WebSocket(`${proto}://${location.host}/api/ws`)
      ws.onopen = () => {
        failCount.current = 0
        stopPolling()
        publish()
      }
      ws.onmessage = (ev) => {
        try {
          const m = JSON.parse(ev.data)
          switch (m.type) {
            case 'init':
              statuses.current = m.statuses || {}
              byNode.current = {}
              for (const [k, pts] of Object.entries(m.series || {})) {
                byNode.current[Number(k)] = (pts as any[]).map(mapPoint)
                const arr = byNode.current[Number(k)]
                if (arr.length) last.current[Number(k)] = arr[arr.length - 1]
              }
              break
            case 'metric':
              pushMetric(m.node_id, mapPoint(m.point))
              break
            case 'node_status':
              statuses.current = { ...statuses.current, [m.node_id]: m.status }
              qc.invalidateQueries({ queryKey: ['nodes'] })
              qc.invalidateQueries({ queryKey: ['overview'] })
              break
            case 'tunnel_status':
              qc.invalidateQueries({ queryKey: ['mappings'] })
              break
          }
          publish()
        } catch {
          /* ignore malformed frames */
        }
      }
      ws.onclose = () => {
        publish()
        if (stopped) return
        failCount.current++
        if (failCount.current >= 3) startPolling() // WS unstable -> degrade to polling
        reconnectTimer = window.setTimeout(connect, Math.min(1000 * failCount.current, 10000))
      }
      ws.onerror = () => ws?.close()
    }
    connect()

    return () => {
      stopped = true
      stopPolling()
      if (reconnectTimer) clearTimeout(reconnectTimer)
      ws?.close()
    }
  }, [qc])

  return <Ctx.Provider value={state}>{children}</Ctx.Provider>
}

function mapPoint(p: any): RTPoint {
  return {
    ts: p.ts, cpu: p.cpu || 0, mem: p.mem || 0,
    rx: p.rx_bps || 0, tx: p.tx_bps || 0, tin: p.tun_in_bps || 0, tout: p.tun_out_bps || 0,
  }
}
