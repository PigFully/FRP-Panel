import { useEffect, useRef } from 'react'
import { useTheme } from '@mui/material'
import echarts from './echartsCore'
import { fmtAxisTime, fmtBps, fmtTipTime } from '../utils/format'

export interface Series {
  name: string
  color: string
  points: { ts: number; val: number }[]
}

// A theme-synced line/area chart. Axis and label colors follow light/dark.
type Unit = 'bps' | 'pct'
const fmtVal = (v: number, u: Unit) => (u === 'pct' ? `${v.toFixed(1)}%` : fmtBps(v))

export function BandwidthChart({ series, height = 280, unit = 'bps', min, max }: { series: Series[]; height?: number; unit?: Unit; min?: number; max?: number }) {
  const ref = useRef<HTMLDivElement>(null)
  const chart = useRef<echarts.ECharts | null>(null)
  const theme = useTheme()
  const dark = theme.palette.mode === 'dark'

  useEffect(() => {
    if (!ref.current) return
    chart.current = echarts.init(ref.current, undefined, { renderer: 'canvas' })
    const ro = new ResizeObserver(() => chart.current?.resize())
    ro.observe(ref.current)
    return () => {
      ro.disconnect()
      chart.current?.dispose()
      chart.current = null
    }
  }, [])

  useEffect(() => {
    if (!chart.current) return
    const axis = dark ? '#919EAB' : '#637381'
    const split = dark ? 'rgba(145,158,171,0.16)' : 'rgba(145,158,171,0.24)'
    // Anchor the visible window: explicit [min,max] (e.g. now-24h..now) else the
    // data's own extent. Evenly divide into ~6 readable, non-overlapping ticks.
    const xs = series.flatMap((s) => s.points.map((p) => p.ts))
    const dmin = xs.length ? Math.min(...xs) : 0
    const dmax = xs.length ? Math.max(...xs) : 0
    const lo = min ?? dmin
    const hi = max ?? dmax
    const span = Math.max(hi - lo, 1)
    chart.current.setOption({
      animationDuration: 300,
      grid: { left: 68, right: 16, top: 28, bottom: 28 },
      tooltip: {
        trigger: 'axis',
        backgroundColor: dark ? '#1C252E' : '#fff',
        borderColor: split,
        textStyle: { color: dark ? '#fff' : '#1C252E' },
        formatter: (ps: any[]) => {
          const t = fmtTipTime(ps[0]?.value?.[0], span)
          return `${t}<br/>` + ps.map((p) => `${p.marker}${p.seriesName}: <b>${fmtVal(p.value[1], unit)}</b>`).join('<br/>')
        },
      },
      legend: { data: series.map((s) => s.name), textStyle: { color: axis }, top: 0, right: 8 },
      xAxis: {
        type: 'time',
        min: min ?? undefined,
        max: max ?? undefined,
        splitNumber: 6,
        axisLine: { lineStyle: { color: split } },
        axisLabel: { color: axis, hideOverlap: true, formatter: (v: number) => fmtAxisTime(v, span) },
        splitLine: { show: false },
      },
      yAxis: {
        type: 'value',
        max: unit === 'pct' ? 100 : undefined,
        axisLabel: { color: axis, formatter: (v: number) => fmtVal(v, unit) },
        splitLine: { lineStyle: { color: split } },
      },
      series: series.map((s) => ({
        name: s.name,
        type: 'line',
        smooth: true,
        symbol: 'none',
        lineStyle: { width: 2, color: s.color },
        areaStyle: {
          color: new echarts.graphic.LinearGradient(0, 0, 0, 1, [
            { offset: 0, color: s.color + '55' },
            { offset: 1, color: s.color + '05' },
          ]),
        },
        data: s.points.map((p) => [p.ts, p.val]),
      })),
    })
  }, [series, dark, unit, min, max])

  return <div ref={ref} style={{ width: '100%', height }} />
}
