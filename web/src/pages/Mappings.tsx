import {
  Box, Button, Card, Chip, Collapse, Grid, IconButton, Paper, Stack, Switch, Table, TableBody, TableCell,
  TableContainer, TableHead, TableRow, Tooltip, Typography,
} from '@mui/material'
import AddRoundedIcon from '@mui/icons-material/AddRounded'
import KeyboardArrowDownRoundedIcon from '@mui/icons-material/KeyboardArrowDownRounded'
import KeyboardArrowUpRoundedIcon from '@mui/icons-material/KeyboardArrowUpRounded'
import EditRoundedIcon from '@mui/icons-material/EditRounded'
import DeleteOutlineRoundedIcon from '@mui/icons-material/DeleteOutlineRounded'
import { Fragment, useState } from 'react'
import { ApiError } from '../api/client'
import { useDeleteMapping, useMappings, useToggleMapping } from '../api/hooks'
import type { Mapping, Target } from '../api/types'
import { CopyField, EmptyState, FRP_LATENCY_HELP, HelpHint, LatencyChip, TableSkeleton } from '../components/common'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { useToast } from '../components/Toast'
import { regionLabel } from '../utils/format'
import { MappingDialog } from './MappingDialog'

function tunnelChip(m: Mapping, t: Target) {
  if (!m.enabled) return <Chip size="small" label="已停用" />
  if (!t.node_online) return <Chip size="small" color="warning" label="节点离线" />
  const st = t.live_status || t.tunnel_status
  if (st === 'online') return <Chip size="small" sx={{ bgcolor: 'rgba(34,197,94,0.16)', color: '#118D57', fontWeight: 700 }} label="在线" />
  return <Chip size="small" label="待建立" />
}

export default function Mappings() {
  const { data, isLoading } = useMappings()
  const [open, setOpen] = useState(false)
  const [editing, setEditing] = useState<Mapping | null>(null)
  const [del, setDel] = useState<Mapping | null>(null)
  const [expanded, setExpanded] = useState<Record<number, boolean>>({})
  const toggle = useToggleMapping()
  const delMut = useDeleteMapping()
  const toast = useToast()

  const onToggle = async (m: Mapping, enabled: boolean) => {
    try {
      await toggle.mutateAsync({ id: m.id, enabled, version: m.version })
      toast.success(enabled ? '映射已启用' : '映射已停用')
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : '操作失败')
    }
  }
  const doDelete = async () => {
    if (!del) return
    try {
      await delMut.mutateAsync(del.id)
      toast.success('映射已删除')
      setDel(null)
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : '删除失败')
    }
  }

  return (
    <Stack spacing={3}>
      <Stack direction="row" alignItems="center" justifyContent="space-between">
        <Typography variant="h4">映射管理</Typography>
        <Button variant="contained" startIcon={<AddRoundedIcon />} onClick={() => { setEditing(null); setOpen(true) }}>新增映射</Button>
      </Stack>

      <Card>
        {isLoading ? (
          <TableSkeleton rows={4} cols={6} />
        ) : (data?.length ?? 0) === 0 ? (
          <EmptyState title="还没有映射" hint="把家里的本地端口同时映射到一个或多个公网节点，对外提供访问。" action={<Button variant="contained" startIcon={<AddRoundedIcon />} onClick={() => { setEditing(null); setOpen(true) }}>新增第一条映射</Button>} />
        ) : (
          <TableContainer component={Paper} elevation={0}>
            <Table>
              <TableHead>
                <TableRow>
                  <TableCell width={40} />
                  <TableCell>本地端口</TableCell><TableCell>备注</TableCell><TableCell>协议</TableCell>
                  <TableCell>目标</TableCell><TableCell align="center">启用</TableCell><TableCell align="right">操作</TableCell>
                </TableRow>
              </TableHead>
              <TableBody>
                {data!.map((m) => (
                  <Fragment key={m.id}>
                    <TableRow hover>
                      <TableCell>
                        <IconButton size="small" onClick={() => setExpanded((e) => ({ ...e, [m.id]: !e[m.id] }))}>
                          {expanded[m.id] ? <KeyboardArrowUpRoundedIcon /> : <KeyboardArrowDownRoundedIcon />}
                        </IconButton>
                      </TableCell>
                      <TableCell><Typography fontWeight={700}>{m.local_port}</Typography></TableCell>
                      <TableCell>{m.remark || '—'}</TableCell>
                      <TableCell><Chip size="small" label={m.proto.toUpperCase()} /></TableCell>
                      <TableCell>{m.targets.length} 个节点</TableCell>
                      <TableCell align="center">
                        <Switch checked={m.enabled} onChange={(e) => onToggle(m, e.target.checked)} />
                      </TableCell>
                      <TableCell align="right">
                        <Tooltip title="编辑"><IconButton size="small" onClick={() => { setEditing(m); setOpen(true) }}><EditRoundedIcon fontSize="small" /></IconButton></Tooltip>
                        <Tooltip title="删除"><IconButton size="small" color="error" onClick={() => setDel(m)}><DeleteOutlineRoundedIcon fontSize="small" /></IconButton></Tooltip>
                      </TableCell>
                    </TableRow>
                    <TableRow>
                      <TableCell colSpan={7} sx={{ py: 0, border: 0 }}>
                        <Collapse in={expanded[m.id]} unmountOnExit>
                          <Stack spacing={1.5} sx={{ py: 1.5 }}>
                            <Typography variant="caption" color="text.secondary">
                              本地 127.0.0.1:{m.local_port} 映射到以下 {m.targets.length} 个公网目标
                            </Typography>
                            {m.targets.map((t) => (
                              <Box key={t.id} sx={{ p: 1.75, border: 1, borderColor: 'divider', borderRadius: 2, bgcolor: 'background.paper' }}>
                                <Grid container spacing={1.5} alignItems="center">
                                  <Grid size={{ xs: 12, sm: 4 }}>
                                    <Typography variant="caption" color="text.secondary">节点</Typography>
                                    <Stack direction="row" spacing={1} alignItems="center">
                                      <Typography fontWeight={700} noWrap>{t.node_name}</Typography>
                                      <Chip size="small" label={regionLabel(t.node_region)} />
                                    </Stack>
                                  </Grid>
                                  <Grid size={{ xs: 12, sm: 4 }}>
                                    <Typography variant="caption" color="text.secondary">公网地址</Typography>
                                    <CopyField value={`${t.node_ip}:${t.remote_port}`} />
                                  </Grid>
                                  <Grid size={{ xs: 6, sm: 2 }}>
                                    <Stack direction="row" spacing={0.5} alignItems="center">
                                      <Typography variant="caption" color="text.secondary">FRP 链路延迟</Typography>
                                      <HelpHint text={FRP_LATENCY_HELP} />
                                    </Stack>
                                    {m.enabled ? <LatencyChip ms={t.node_latency_ms} /> : <Chip size="small" label="—" />}
                                  </Grid>
                                  <Grid size={{ xs: 6, sm: 2 }}>
                                    <Typography variant="caption" color="text.secondary" display="block">隧道状态</Typography>
                                    {tunnelChip(m, t)}
                                  </Grid>
                                </Grid>
                              </Box>
                            ))}
                          </Stack>
                        </Collapse>
                      </TableCell>
                    </TableRow>
                  </Fragment>
                ))}
              </TableBody>
            </Table>
          </TableContainer>
        )}
      </Card>

      <MappingDialog open={open} editing={editing} onClose={() => setOpen(false)} />
      <ConfirmDialog
        open={!!del} danger title="删除映射" confirmText="删除" loading={delMut.isPending}
        body={del ? `确认删除本地端口 ${del.local_port} 的映射？其所有隧道将立即断开。` : ''}
        onCancel={() => setDel(null)} onConfirm={doDelete}
      />
    </Stack>
  )
}
