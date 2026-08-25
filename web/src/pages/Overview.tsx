import { Box, Card, CardContent, CardHeader, Chip, Grid, LinearProgress, List, ListItem, ListItemText, Stack, Typography } from '@mui/material'
import HubRoundedIcon from '@mui/icons-material/HubRounded'
import CloudDoneRoundedIcon from '@mui/icons-material/CloudDoneRounded'
import SwapHorizRoundedIcon from '@mui/icons-material/SwapHorizRounded'
import BoltRoundedIcon from '@mui/icons-material/BoltRounded'
import { useOverview } from '../api/hooks'
import { useRealtime } from '../realtime/RealtimeProvider'
import { BandwidthChart, type Series } from '../components/BandwidthChart'
import { StatCard, TableSkeleton } from '../components/common'
import { fmtBytes, fmtBps, fmtTime } from '../utils/format'

export default function Overview() {
  const { data, isLoading } = useOverview()
  const rt = useRealtime()

  // Rolling realtime window anchored to now (last ~6 minutes).
  const now = Date.now()
  const winMin = now - 6 * 60 * 1000
  const nodeSeries: Series[] = [
    { name: '下行', color: '#2065D1', points: rt.global.map((p) => ({ ts: p.ts, val: p.rx })) },
    { name: '上行', color: '#00B8D9', points: rt.global.map((p) => ({ ts: p.ts, val: p.tx })) },
  ]
  const tunSeries: Series[] = [
    { name: '隧道入', color: '#22C55E', points: rt.global.map((p) => ({ ts: p.ts, val: p.tin })) },
    { name: '隧道出', color: '#FFAB00', points: rt.global.map((p) => ({ ts: p.ts, val: p.tout })) },
  ]

  const s = data?.stats
  return (
    <Stack spacing={3}>
      <Typography variant="h4">数据概览</Typography>

      <Grid container spacing={2.5}>
        <Grid size={{ xs: 6, md: 3 }}><StatCard label="节点总数" value={s?.node_total ?? '—'} icon={<HubRoundedIcon />} /></Grid>
        <Grid size={{ xs: 6, md: 3 }}><StatCard label="在线节点" value={s?.node_online ?? '—'} icon={<CloudDoneRoundedIcon />} color="#22C55E" /></Grid>
        <Grid size={{ xs: 6, md: 3 }}><StatCard label="映射总数" value={s?.mapping_total ?? '—'} icon={<SwapHorizRoundedIcon />} /></Grid>
        <Grid size={{ xs: 6, md: 3 }}><StatCard label="启用中映射" value={s?.mapping_enabled ?? '—'} icon={<BoltRoundedIcon />} color="#FFAB00" /></Grid>
      </Grid>

      <Grid container spacing={2.5}>
        <Grid size={{ xs: 12, lg: 6 }}>
          <Card>
            <CardHeader title="节点整机带宽" subheader="云服务器网卡实时速率（含非 frp 流量）· 5 秒级 · 单位 字节/秒" titleTypographyProps={{ variant: 'h6' }} />
            <CardContent sx={{ pt: 0 }}><BandwidthChart series={nodeSeries} min={winMin} max={now} /></CardContent>
          </Card>
        </Grid>
        <Grid size={{ xs: 12, lg: 6 }}>
          <Card>
            <CardHeader title="隧道流量带宽" subheader="仅 frp 隧道口径 · 5 秒级 · 单位 字节/秒" titleTypographyProps={{ variant: 'h6' }} />
            <CardContent sx={{ pt: 0 }}><BandwidthChart series={tunSeries} min={winMin} max={now} /></CardContent>
          </Card>
        </Grid>
      </Grid>

      <Grid container spacing={2.5}>
        <Grid size={{ xs: 12, md: 4 }}>
          <Card sx={{ height: '100%' }}>
            <CardHeader title="流量统计" titleTypographyProps={{ variant: 'h6' }} />
            <CardContent>
              <Stack spacing={2}>
                <TrafficRow label="今日隧道流量" value={fmtBytes((data?.traffic_today.tun_in || 0) + (data?.traffic_today.tun_out || 0))} />
                <TrafficRow label="今日节点流量" value={fmtBytes((data?.traffic_today.node_rx || 0) + (data?.traffic_today.node_tx || 0))} />
                <TrafficRow label="近 30 天隧道流量" value={fmtBytes((data?.traffic_last30.tun_in || 0) + (data?.traffic_last30.tun_out || 0))} />
                <TrafficRow label="当前隧道速率" value={fmtBps((data?.live.tun_in_bps || 0) + (data?.live.tun_out_bps || 0))} />
              </Stack>
            </CardContent>
          </Card>
        </Grid>
        <Grid size={{ xs: 12, md: 4 }}>
          <Card sx={{ height: '100%' }}>
            <CardHeader title="节点流量 Top" subheader="近 30 天隧道口径" titleTypographyProps={{ variant: 'h6' }} />
            <CardContent sx={{ pt: 0 }}>
              {data?.top_nodes?.length ? (
                <List dense>
                  {data.top_nodes.map((t, i) => (
                    <ListItem key={t.node_id} secondaryAction={<Typography variant="body2" fontWeight={700}>{fmtBytes(t.tun_in + t.tun_out)}</Typography>}>
                      <ListItemText primary={`${i + 1}. ${t.node_name}`} />
                    </ListItem>
                  ))}
                </List>
              ) : (
                <Typography variant="body2" color="text.secondary" sx={{ py: 3, textAlign: 'center' }}>暂无流量数据</Typography>
              )}
            </CardContent>
          </Card>
        </Grid>
        <Grid size={{ xs: 12, md: 4 }}>
          <Card sx={{ height: '100%' }}>
            <CardHeader title="最近操作日志" titleTypographyProps={{ variant: 'h6' }} />
            <CardContent sx={{ pt: 0 }}>
              {isLoading ? (
                <TableSkeleton rows={4} cols={1} />
              ) : data?.recent_logs?.length ? (
                <List dense>
                  {data.recent_logs.map((l) => (
                    <ListItem key={l.id} disableGutters>
                      <ListItemText
                        primary={l.detail}
                        secondary={fmtTime(l.created_at)}
                        primaryTypographyProps={{ variant: 'body2', noWrap: true, title: l.detail }}
                      />
                      <Chip size="small" label={logType(l.type)} sx={{ ml: 1 }} />
                    </ListItem>
                  ))}
                </List>
              ) : (
                <Typography variant="body2" color="text.secondary" sx={{ py: 3, textAlign: 'center' }}>暂无日志</Typography>
              )}
            </CardContent>
          </Card>
        </Grid>
      </Grid>
      {isLoading && <LinearProgress />}
    </Stack>
  )
}

function TrafficRow({ label, value }: { label: string; value: string }) {
  return (
    <Box>
      <Typography variant="body2" color="text.secondary">{label}</Typography>
      <Typography variant="h6">{value}</Typography>
    </Box>
  )
}

export function logType(t: string): string {
  return t === 'frp_event' ? 'FRP 事件' : t === 'reconcile' ? '对账修复' : '面板操作'
}
