import {
  Box, Button, Card, CardContent, Chip, Dialog, DialogActions, DialogContent, DialogTitle, Divider, IconButton,
  InputAdornment, MenuItem, Paper, Stack, Table, TableBody, TableCell, TableContainer, TableHead, TableRow,
  TextField, Tooltip, Typography, useMediaQuery, useTheme,
} from '@mui/material'
import AddRoundedIcon from '@mui/icons-material/AddRounded'
import SearchRoundedIcon from '@mui/icons-material/SearchRounded'
import InfoOutlinedIcon from '@mui/icons-material/InfoOutlined'
import EditRoundedIcon from '@mui/icons-material/EditRounded'
import DeleteOutlineRoundedIcon from '@mui/icons-material/DeleteOutlineRounded'
import { useMemo, useState } from 'react'
import { ApiError } from '../api/client'
import { useDeleteNode, useMe, useNodes, useRotateToken, useUpdateNode } from '../api/hooks'
import type { Node } from '../api/types'
import { CopyField, EmptyState, LatencyChip, StatusBadge, TableSkeleton } from '../components/common'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { useToast } from '../components/Toast'
import { useRealtime } from '../realtime/RealtimeProvider'
import { fmtBps, fmtBytes, regionLabel } from '../utils/format'
import { AddNodeDialog } from './AddNodeDialog'
import { NodeDrawer } from './NodeDrawer'

export default function Nodes() {
  const { data, isLoading } = useNodes()
  const rt = useRealtime()
  const theme = useTheme()
  const desktop = useMediaQuery(theme.breakpoints.up('md'))
  const [q, setQ] = useState('')
  const [addOpen, setAddOpen] = useState(false)
  const [detail, setDetail] = useState<Node | null>(null)
  const [edit, setEdit] = useState<Node | null>(null)
  const [del, setDel] = useState<Node | null>(null)

  const nodes = useMemo(() => {
    const list = data ?? []
    const kw = q.trim().toLowerCase()
    return kw ? list.filter((n) => n.name.toLowerCase().includes(kw) || n.ip.includes(kw)) : list
  }, [data, q])

  const online = (n: Node) => (rt.statuses[n.id] ? rt.statuses[n.id] === 'online' : n.connected || n.status === 'online')

  return (
    <Stack spacing={3}>
      <Stack direction="row" alignItems="center" justifyContent="space-between" flexWrap="wrap" gap={2}>
        <Typography variant="h4">节点列表</Typography>
        <Stack direction="row" spacing={1.5}>
          <TextField
            size="small" placeholder="搜索名称 / IP" value={q} onChange={(e) => setQ(e.target.value)}
            InputProps={{ startAdornment: <InputAdornment position="start"><SearchRoundedIcon fontSize="small" /></InputAdornment> }}
          />
          <Button variant="contained" startIcon={<AddRoundedIcon />} onClick={() => setAddOpen(true)}>添加节点</Button>
        </Stack>
      </Stack>

      <Card>
        {isLoading ? (
          <TableSkeleton rows={4} cols={desktop ? 7 : 2} />
        ) : nodes.length === 0 ? (
          <EmptyState title="还没有节点" hint="点击「添加节点」在你的公网服务器上部署一个 Agent，把本地服务映射出去。" action={<Button variant="contained" startIcon={<AddRoundedIcon />} onClick={() => setAddOpen(true)}>添加第一个节点</Button>} />
        ) : desktop ? (
          <TableContainer component={Paper} elevation={0}>
            <Table>
              <TableHead>
                <TableRow>
                  <TableCell>名称</TableCell><TableCell>公网 IP</TableCell><TableCell>地区</TableCell><TableCell>状态</TableCell>
                  <TableCell align="right">CPU / 内存</TableCell><TableCell align="right">上/下行</TableCell>
                  <TableCell align="right">今日流量</TableCell><TableCell>frps</TableCell><TableCell align="right">操作</TableCell>
                </TableRow>
              </TableHead>
              <TableBody>
                {nodes.map((n) => (
                  <TableRow key={n.id} hover>
                    <TableCell sx={{ minWidth: 132 }}><Typography fontWeight={600} noWrap>{n.name}</Typography><Typography variant="caption" color="text.secondary" noWrap>{n.target_count} 条映射</Typography></TableCell>
                    <TableCell sx={{ maxWidth: 220 }}>
                      <Stack direction="row" spacing={1} alignItems="center">
                        <CopyField value={n.ip} />
                        <LatencyChip ms={n.latency_ms} />
                      </Stack>
                    </TableCell>
                    <TableCell><Chip size="small" label={regionLabel(n.region)} /></TableCell>
                    <TableCell><StatusBadge online={online(n)} /></TableCell>
                    <TableCell align="right">{n.cpu.toFixed(0)}% / {n.mem.toFixed(0)}%</TableCell>
                    <TableCell align="right">{fmtBps(n.tx_bps)} / {fmtBps(n.rx_bps)}</TableCell>
                    <TableCell align="right">{fmtBytes(n.today_tun_in + n.today_tun_out)}</TableCell>
                    <TableCell>{n.frps_version || '—'}</TableCell>
                    <TableCell align="right">
                      <Tooltip title="详情"><IconButton size="small" onClick={() => setDetail(n)}><InfoOutlinedIcon fontSize="small" /></IconButton></Tooltip>
                      <Tooltip title="编辑"><IconButton size="small" onClick={() => setEdit(n)}><EditRoundedIcon fontSize="small" /></IconButton></Tooltip>
                      <Tooltip title="删除"><IconButton size="small" color="error" onClick={() => setDel(n)}><DeleteOutlineRoundedIcon fontSize="small" /></IconButton></Tooltip>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </TableContainer>
        ) : (
          <Stack spacing={1.5} sx={{ p: 2 }}>
            {nodes.map((n) => (
              <Card key={n.id} variant="outlined">
                <CardContent>
                  <Stack direction="row" justifyContent="space-between" alignItems="center">
                    <Typography fontWeight={700}>{n.name}</Typography>
                    <StatusBadge online={online(n)} />
                  </Stack>
                  <Stack direction="row" spacing={1} alignItems="center" sx={{ mt: 0.5 }}>
                    <Typography variant="body2" color="text.secondary">{n.ip} · {regionLabel(n.region)}</Typography>
                    <LatencyChip ms={n.latency_ms} />
                  </Stack>
                  <Typography variant="body2" sx={{ mt: 1 }}>CPU {n.cpu.toFixed(0)}% · 内存 {n.mem.toFixed(0)}% · 今日 {fmtBytes(n.today_tun_in + n.today_tun_out)}</Typography>
                  <Stack direction="row" spacing={1} sx={{ mt: 1.5 }}>
                    <Button size="small" onClick={() => setDetail(n)}>详情</Button>
                    <Button size="small" onClick={() => setEdit(n)}>编辑</Button>
                    <Button size="small" color="error" onClick={() => setDel(n)}>删除</Button>
                  </Stack>
                </CardContent>
              </Card>
            ))}
          </Stack>
        )}
      </Card>

      <AddNodeDialog open={addOpen} onClose={() => setAddOpen(false)} />
      <NodeDrawer node={detail} onClose={() => setDetail(null)} />
      {edit && <EditNodeDialog node={edit} onClose={() => setEdit(null)} />}
      <DeleteNodeDialog node={del} onClose={() => setDel(null)} />
    </Stack>
  )
}

function EditNodeDialog({ node, onClose }: { node: Node; onClose: () => void }) {
  const upd = useUpdateNode()
  const rotate = useRotateToken()
  const toast = useToast()
  const [name, setName] = useState(node.name)
  const [region, setRegion] = useState(node.region)
  const [confirmRotate, setConfirmRotate] = useState(false)

  const save = async () => {
    try {
      await upd.mutateAsync({ id: node.id, name, region })
      toast.success('已保存')
      onClose()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : '保存失败')
    }
  }
  const doRotate = async () => {
    try {
      await rotate.mutateAsync(node.id)
      toast.success('已轮换 frps token，隧道将自动恢复')
      setConfirmRotate(false)
      onClose()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : '轮换失败')
    }
  }
  return (
    <>
      <Dialog open onClose={onClose} maxWidth="xs" fullWidth>
        <DialogTitle sx={{ fontWeight: 700 }}>编辑节点</DialogTitle>
        <DialogContent>
          <Stack spacing={2.5} sx={{ pt: 1 }}>
            <TextField label="节点名称" fullWidth value={name} onChange={(e) => setName(e.target.value)} />
            <TextField label="地区" select fullWidth value={region} onChange={(e) => setRegion(e.target.value)}>
              <MenuItem value="overseas">国外</MenuItem>
              <MenuItem value="domestic">国内</MenuItem>
            </TextField>
            <Divider />
            <Box>
              <Typography variant="subtitle2">安全维护</Typography>
              <Typography variant="body2" color="text.secondary" sx={{ mb: 1 }}>轮换 frps 认证 token（该节点所有隧道将短暂中断后自动恢复）</Typography>
              <Button color="warning" variant="outlined" onClick={() => setConfirmRotate(true)} disabled={!node.connected}>轮换 frps Token</Button>
            </Box>
          </Stack>
        </DialogContent>
        <DialogActions sx={{ px: 3, pb: 2 }}>
          <Button color="inherit" onClick={onClose}>取消</Button>
          <Button variant="contained" onClick={save} disabled={upd.isPending}>保存</Button>
        </DialogActions>
      </Dialog>
      <ConfirmDialog
        open={confirmRotate} danger title="轮换 frps Token？"
        body="该节点所有隧道将短暂中断后自动恢复。确认继续？"
        confirmText="确认轮换" loading={rotate.isPending}
        onCancel={() => setConfirmRotate(false)} onConfirm={doRotate}
      />
    </>
  )
}

function DeleteNodeDialog({ node, onClose }: { node: Node | null; onClose: () => void }) {
  const del = useDeleteNode()
  const { data: me } = useMe()
  const toast = useToast()
  if (!node) return null
  const base = me?.install_base?.replace(/\/$/, '')
  const uninstall = base ? `curl -fsSL ${base}/install-agent.sh | bash -s -- uninstall` : '/opt/frp-agent/agent 所在服务器执行 install-agent.sh uninstall'
  const doDel = async () => {
    try {
      await del.mutateAsync(node.id)
      toast.success('节点已删除')
      onClose()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : '删除失败')
    }
  }
  return (
    <ConfirmDialog
      open danger title="删除节点" requireText={node.name} confirmText="删除节点" loading={del.isPending}
      onCancel={onClose} onConfirm={doDel}
      body={
        <Stack spacing={1.5}>
          <Typography variant="body2">删除后该节点上的所有映射目标将被移除，隧道立即断开。此操作不可撤销。</Typography>
          <Typography variant="body2" color="text.secondary">如需彻底卸载节点上的 Agent，请在该服务器执行：</Typography>
          <Box sx={{ p: 1, borderRadius: 1, bgcolor: 'action.hover' }}><CopyField value={uninstall} /></Box>
        </Stack>
      }
    />
  )
}
