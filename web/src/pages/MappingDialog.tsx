import {
  Alert, Box, Button, Chip, Dialog, DialogActions, DialogContent, DialogTitle, Divider, FormControlLabel, IconButton,
  MenuItem, Stack, Switch, TextField, Typography,
} from '@mui/material'
import { LatencyChip } from '../components/common'
import AddRoundedIcon from '@mui/icons-material/AddRounded'
import DeleteOutlineRoundedIcon from '@mui/icons-material/DeleteOutlineRounded'
import { useEffect, useRef, useState } from 'react'
import { ApiError } from '../api/client'
import { localCheck, portCheck, useCreateMapping, useNodes, useUpdateMapping } from '../api/hooks'
import type { Mapping } from '../api/types'
import { useToast } from '../components/Toast'
import { regionLabel } from '../utils/format'
import { reservedReason, validPort } from '../utils/ports'

interface Row { node_id: number; remote_port: string }
interface Check { loading: boolean; ok: boolean; msg: string }

export function MappingDialog({ open, editing, onClose }: { open: boolean; editing: Mapping | null; onClose: () => void }) {
  const { data: nodes } = useNodes()
  const create = useCreateMapping()
  const update = useUpdateMapping()
  const toast = useToast()

  const [localPort, setLocalPort] = useState('')
  const [proto, setProto] = useState('tcp')
  const [remark, setRemark] = useState('')
  const [enabled, setEnabled] = useState(true)
  const [rows, setRows] = useState<Row[]>([{ node_id: 0, remote_port: '' }])
  const [checks, setChecks] = useState<Record<number, Check>>({})
  const [localMsg, setLocalMsg] = useState('')
  const timers = useRef<Record<number, number>>({})

  useEffect(() => {
    if (!open) return
    if (editing) {
      setLocalPort(String(editing.local_port)); setProto(editing.proto); setRemark(editing.remark); setEnabled(editing.enabled)
      setRows(editing.targets.map((t) => ({ node_id: t.node_id, remote_port: String(t.remote_port) })))
    } else {
      setLocalPort(''); setProto('tcp'); setRemark(''); setEnabled(true); setRows([{ node_id: 0, remote_port: '' }])
    }
    setChecks({}); setLocalMsg('')
  }, [open, editing])

  const usedNodeIds = rows.map((r) => r.node_id).filter(Boolean)
  const dupNode = new Set(usedNodeIds).size !== usedNodeIds.length

  const runCheck = (idx: number, nodeId: number, port: number) => {
    if (timers.current[idx]) window.clearTimeout(timers.current[idx])
    const reason = reservedReason(port)
    if (reason) { setChecks((c) => ({ ...c, [idx]: { loading: false, ok: false, msg: reason } })); return }
    if (!validPort(port) || !nodeId) { setChecks((c) => ({ ...c, [idx]: { loading: false, ok: false, msg: '' } })); return }
    setChecks((c) => ({ ...c, [idx]: { loading: true, ok: false, msg: '检查中…' } }))
    timers.current[idx] = window.setTimeout(async () => {
      try {
        const r = await portCheck({ node_id: nodeId, port, proto })
        setChecks((c) => ({ ...c, [idx]: r.available ? { loading: false, ok: true, msg: '端口可用' } : { loading: false, ok: false, msg: `该端口已被占用（${r.process || r.reason || '占用'}）` } }))
      } catch (e) {
        setChecks((c) => ({ ...c, [idx]: { loading: false, ok: false, msg: e instanceof ApiError ? e.message : '检查失败' } }))
      }
    }, 500)
  }

  const setRow = (idx: number, patch: Partial<Row>) => {
    setRows((rs) => {
      const next = rs.map((r, i) => (i === idx ? { ...r, ...patch } : r))
      const port = Number(next[idx].remote_port)
      runCheck(idx, next[idx].node_id, port)
      return next
    })
  }

  const checkLocal = async () => {
    const p = Number(localPort)
    if (!validPort(p)) { setLocalMsg(''); return }
    try {
      const r = await localCheck({ port: p, proto })
      setLocalMsg(r.listening ? `本地端口有进程监听（${r.process || '未知'}）` : '本地端口当前无进程监听，启用后将无法建立隧道')
    } catch { setLocalMsg('') }
  }

  const submit = async () => {
    const lp = Number(localPort)
    if (!validPort(lp)) return toast.error('本地端口无效')
    if (rows.some((r) => !r.node_id)) return toast.error('请为每一行选择节点')
    if (dupNode) return toast.error('同一映射内不允许重复选择同一节点')
    for (const r of rows) {
      const rp = Number(r.remote_port)
      if (!validPort(rp)) return toast.error('公网端口无效')
      if (reservedReason(rp)) return toast.error(reservedReason(rp))
    }
    const body = {
      local_port: lp, proto, remark, enabled,
      version: editing?.version ?? 0,
      targets: rows.map((r) => ({ node_id: r.node_id, remote_port: Number(r.remote_port) })),
    }
    try {
      if (editing) await update.mutateAsync({ id: editing.id, ...body })
      else await create.mutateAsync(body)
      toast.success(editing ? '映射已更新' : '映射已创建')
      onClose()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : '保存失败')
    }
  }

  const pending = create.isPending || update.isPending
  return (
    <Dialog open={open} onClose={pending ? undefined : onClose} maxWidth="sm" fullWidth>
      <DialogTitle sx={{ fontWeight: 700 }}>{editing ? '编辑映射' : '新增映射'}</DialogTitle>
      <DialogContent>
        <Stack spacing={2.5} sx={{ pt: 1 }}>
          <Stack direction="row" spacing={2}>
            <TextField label="本地端口" fullWidth value={localPort} onChange={(e) => setLocalPort(e.target.value.replace(/\D/g, ''))} onBlur={checkLocal}
              error={!!localPort && !validPort(Number(localPort))} helperText={localMsg} />
            <TextField label="协议" select value={proto} onChange={(e) => setProto(e.target.value)} sx={{ minWidth: 110 }}>
              <MenuItem value="tcp">TCP</MenuItem><MenuItem value="udp">UDP</MenuItem>
            </TextField>
          </Stack>
          <TextField label="备注名" fullWidth value={remark} onChange={(e) => setRemark(e.target.value)} />

          <Divider><Typography variant="caption" color="text.secondary">映射目标（可同时映射到国内 + 国外多个节点）</Typography></Divider>

          {rows.map((row, idx) => {
            const chk = checks[idx]
            const rpNum = Number(row.remote_port)
            const err = (!!row.remote_port && !validPort(rpNum)) || (chk && !chk.ok && !!chk.msg)
            return (
              <Stack key={idx} direction="row" spacing={1.5} alignItems="flex-start">
                <TextField label="云节点" select fullWidth value={row.node_id || ''} onChange={(e) => setRow(idx, { node_id: Number(e.target.value) })}>
                  {(nodes ?? []).map((n) => (
                    <MenuItem key={n.id} value={n.id} disabled={usedNodeIds.includes(n.id) && row.node_id !== n.id}>
                      <Stack direction="row" spacing={1} alignItems="center" sx={{ width: '100%' }}>
                        <Typography component="span" fontWeight={600}>{n.name}</Typography>
                        <Typography component="span" variant="body2" color="text.secondary">{n.ip}</Typography>
                        <Chip size="small" label={regionLabel(n.region)} />
                        {!n.connected && <Chip size="small" color="warning" label="离线" />}
                        <Box sx={{ flex: 1 }} />
                        <LatencyChip ms={n.latency_ms} />
                      </Stack>
                    </MenuItem>
                  ))}
                </TextField>
                <TextField label="公网端口" sx={{ minWidth: 150 }} value={row.remote_port}
                  onChange={(e) => setRow(idx, { remote_port: e.target.value.replace(/\D/g, '') })}
                  error={!!err} helperText={chk?.msg || (row.remote_port && !validPort(rpNum) ? '1-65535' : '')}
                  FormHelperTextProps={{ sx: { color: chk?.ok ? 'success.main' : undefined } }} />
                <IconButton onClick={() => setRows((rs) => rs.filter((_, i) => i !== idx))} disabled={rows.length === 1} sx={{ mt: 1 }}>
                  <DeleteOutlineRoundedIcon />
                </IconButton>
              </Stack>
            )
          })}
          <Button startIcon={<AddRoundedIcon />} onClick={() => setRows((rs) => [...rs, { node_id: 0, remote_port: '' }])} sx={{ alignSelf: 'flex-start' }}>
            添加一个节点
          </Button>
          {dupNode && <Alert severity="error">同一映射内不允许重复选择同一节点</Alert>}

          <FormControlLabel control={<Switch checked={enabled} onChange={(e) => setEnabled(e.target.checked)} />} label="创建后立即启用（需本地端口已有服务监听）" />
        </Stack>
      </DialogContent>
      <DialogActions sx={{ px: 3, pb: 2 }}>
        <Button color="inherit" onClick={onClose} disabled={pending}>取消</Button>
        <Button variant="contained" onClick={submit} disabled={pending}>{pending ? '保存中…' : '保存'}</Button>
      </DialogActions>
    </Dialog>
  )
}
