import { Box, Button, Chip, Drawer, IconButton, Stack, Tab, Tabs, ToggleButton, ToggleButtonGroup, Typography } from '@mui/material'
import CloseRoundedIcon from '@mui/icons-material/CloseRounded'
import { useState } from 'react'
import { ApiError } from '../api/client'
import { useMappings, useMe, useNodeHistory, useUpdateAgent } from '../api/hooks'
import type { MetricPoint, Node } from '../api/types'
import { BandwidthChart, type Series } from '../components/BandwidthChart'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { CopyField, StatusBadge } from '../components/common'
import { useToast } from '../components/Toast'
import { fmtBps, fmtBytes, regionLabel } from '../utils/format'

export function NodeDrawer({ node, onClose }: { node: Node | null; onClose: () => void }) {
  const [hours, setHours] = useState(24)
  const [tab, setTab] = useState(0)
  const { data, isLoading } = useNodeHistory(node?.id ?? 0, hours, !!node)
  const { data: mappings } = useMappings()
  const { data: me } = useMe()
  const updateAgent = useUpdateAgent()
  const toast = useToast()
  const [confirmUpdate, setConfirmUpdate] = useState(false)
  if (!node) return null

  const agentOutdated = node.connected && !!me?.version && !!node.agent_version && node.agent_version !== me.version
  const doUpdateAgent = async () => {
    try {
      const r = await updateAgent.mutateAsync(node.id)
      setConfirmUpdate(false)
      toast.success(`升级指令已下发${r.target ? `（目标 ${r.target}）` : ''}，Agent 下载校验后自动重启回连，隧道不中断`)
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : '下发升级指令失败')
    }
  }

  const pts: MetricPoint[] = data?.points ?? []
  const toTs = (s: string) => new Date(s).getTime()
  // Anchor the window to now: [now - span, now]. Leading gaps just render empty.
  const now = Date.now()
  const rangeMin = now - hours * 3600 * 1000
  const bw: Series[] = [
    { name: '下行峰值', color: '#2065D1', points: pts.map((p) => ({ ts: toTs(p.ts), val: p.rx_peak_bps })) },
    { name: '上行峰值', color: '#00B8D9', points: pts.map((p) => ({ ts: toTs(p.ts), val: p.tx_peak_bps })) },
  ]
  const res: Series[] = [
    { name: 'CPU', color: '#2065D1', points: pts.map((p) => ({ ts: toTs(p.ts), val: p.cpu_avg })) },
    { name: '内存', color: '#FFAB00', points: pts.map((p) => ({ ts: toTs(p.ts), val: p.mem_avg })) },
  ]
  const nodeMappings = (mappings ?? []).filter((m) => m.targets.some((t) => t.node_id === node.id))

  return (
    <Drawer anchor="right" open={!!node} onClose={onClose} slotProps={{ paper: { sx: { width: { xs: '100%', sm: 560 }, p: 3 } } }}>
      <Stack direction="row" alignItems="center" justifyContent="space-between" sx={{ mb: 2 }}>
        <Stack direction="row" spacing={1.5} alignItems="center">
          <Typography variant="h5">{node.name}</Typography>
          <StatusBadge online={node.connected || node.status === 'online'} />
          <Chip size="small" label={regionLabel(node.region)} />
        </Stack>
        <IconButton onClick={onClose}><CloseRoundedIcon /></IconButton>
      </Stack>

      <Stack spacing={1} sx={{ mb: 2 }}>
        <Row label="公网 IP"><CopyField value={node.ip} /></Row>
        <Row label="frps / Agent 版本">
          <Stack direction="row" spacing={1} alignItems="center">
            <Typography variant="body2">{node.frps_version || '—'} / {node.agent_version || '—'}</Typography>
            {agentOutdated && (
              <Button size="small" variant="outlined" onClick={() => setConfirmUpdate(true)} disabled={updateAgent.isPending}>
                升级到 {me?.version}
              </Button>
            )}
          </Stack>
        </Row>
        <Row label="证书指纹"><CopyField value={node.fingerprint} /></Row>
      </Stack>

      <Stack direction="row" justifyContent="space-between" alignItems="center">
        <Tabs value={tab} onChange={(_, v) => setTab(v)}>
          <Tab label="资源" /><Tab label="带宽" /><Tab label={`映射 (${nodeMappings.length})`} />
        </Tabs>
        {tab < 2 && (
          <ToggleButtonGroup size="small" exclusive value={hours} onChange={(_, v) => v && setHours(v)}>
            <ToggleButton value={24}>24小时</ToggleButton>
            <ToggleButton value={168}>7天</ToggleButton>
            <ToggleButton value={720}>30天</ToggleButton>
          </ToggleButtonGroup>
        )}
      </Stack>

      <Box sx={{ mt: 2 }}>
        {tab === 0 && (isLoading ? <Loading /> : pts.length ? <BandwidthChart series={res} unit="pct" min={rangeMin} max={now} /> : <NoData />)}
        {tab === 1 && (isLoading ? <Loading /> : pts.length ? <BandwidthChart series={bw} min={rangeMin} max={now} /> : <NoData />)}
        {tab === 2 && (
          <Stack spacing={1}>
            {nodeMappings.length ? nodeMappings.map((m) => {
              const t = m.targets.find((x) => x.node_id === node.id)!
              return (
                <Stack key={m.id} direction="row" justifyContent="space-between" sx={{ p: 1.5, borderRadius: 2, border: 1, borderColor: 'divider' }}>
                  <Typography variant="body2">本地 {m.local_port} → 公网 {t.remote_port} · {m.proto.toUpperCase()} {m.remark && `· ${m.remark}`}</Typography>
                  <Chip size="small" label={m.enabled ? (t.live_status === 'online' || t.tunnel_status === 'online' ? '在线' : '待建立') : '已停用'} />
                </Stack>
              )
            }) : <NoData text="该节点暂无映射" />}
          </Stack>
        )}
      </Box>

      <Box sx={{ mt: 3 }}>
        <Typography variant="subtitle2" gutterBottom>今日流量</Typography>
        <Typography variant="body2" color="text.secondary">
          隧道 {fmtBytes(node.today_tun_in + node.today_tun_out)} · 当前速率 {fmtBps(node.rx_bps + node.tx_bps)}
        </Typography>
      </Box>

      <ConfirmDialog
        open={confirmUpdate} title="在线升级 Agent"
        body={`将指示节点从更新源下载并校验 Agent 新版本，随后自动重启生效。frps 与隧道不受影响，Agent 重启期间该节点短暂显示离线后自动回连。确认升级？`}
        confirmText="下发升级" loading={updateAgent.isPending}
        onCancel={() => setConfirmUpdate(false)} onConfirm={doUpdateAgent}
      />
    </Drawer>
  )
}

function Row({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <Stack direction="row" justifyContent="space-between" alignItems="center">
      <Typography variant="body2" color="text.secondary">{label}</Typography>
      <Box sx={{ maxWidth: '65%' }}>{children}</Box>
    </Stack>
  )
}
function Loading() { return <Box sx={{ height: 280, display: 'grid', placeItems: 'center', color: 'text.secondary' }}>加载中…</Box> }
function NoData({ text = '暂无历史数据' }: { text?: string }) { return <Box sx={{ height: 200, display: 'grid', placeItems: 'center', color: 'text.secondary' }}>{text}</Box> }
