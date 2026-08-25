import {
  Alert, Box, Button, Dialog, DialogActions, DialogContent, DialogTitle, Divider, MenuItem,
  Stack, TextField, Typography,
} from '@mui/material'
import { useMemo, useState } from 'react'
import { ApiError } from '../api/client'
import { useCreateNode, useMe } from '../api/hooks'
import { CopyField } from '../components/common'
import { useToast } from '../components/Toast'

interface ParsedReceipt { ip: string; port: number; fp: string; ver: string; frps_port: number }

function parseReceipt(b64: string): ParsedReceipt | null {
  try {
    const clean = b64.replace(/\s+/g, '')
    if (!clean) return null
    const j = JSON.parse(atob(clean))
    if (!j.ip || !j.port) return null
    return { ip: j.ip, port: j.port, fp: j.fp || '', ver: j.ver || '', frps_port: j.frps_port || 0 }
  } catch {
    return null
  }
}

export function AddNodeDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  const { data: me } = useMe()
  const create = useCreateNode()
  const toast = useToast()
  const [name, setName] = useState('')
  const [region, setRegion] = useState('overseas')
  const [receipt, setReceipt] = useState('')
  const parsed = useMemo(() => parseReceipt(receipt), [receipt])

  const base = me?.install_base
  const installCmd = base
    ? `curl -fsSL ${base.replace(/\/$/, '')}/install-agent.sh | bash`
    : '（请先在「面板设置」中配置分发地址后再获取安装命令）'

  const submit = async () => {
    if (!name.trim()) return toast.error('请填写节点名称')
    if (!parsed) return toast.error('注册回执无法解析，请检查粘贴内容')
    try {
      await create.mutateAsync({ name: name.trim(), region, receipt: receipt.replace(/\s+/g, '') })
      toast.success('节点已添加并验证在线')
      setName(''); setReceipt('')
      onClose()
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : '添加失败')
    }
  }

  return (
    <Dialog open={open} onClose={create.isPending ? undefined : onClose} maxWidth="sm" fullWidth>
      <DialogTitle sx={{ fontWeight: 700 }}>添加节点</DialogTitle>
      <DialogContent>
        <Stack spacing={2.5} sx={{ pt: 1 }}>
          <Box>
            <Typography variant="subtitle2" gutterBottom>1. 在目标 Ubuntu 22 服务器以 root 执行一键安装脚本</Typography>
            <Box sx={{ p: 1.5, borderRadius: 2, bgcolor: 'action.hover', border: 1, borderColor: 'divider' }}>
              <CopyField value={installCmd} />
            </Box>
          </Box>
          <Divider />
          <Box>
            <Typography variant="subtitle2" gutterBottom>2. 粘贴脚本输出的注册回执</Typography>
            <TextField
              fullWidth multiline minRows={3} placeholder="粘贴 Base64 注册回执…"
              value={receipt} onChange={(e) => setReceipt(e.target.value)}
              slotProps={{ input: { sx: { fontFamily: 'monospace', fontSize: 13 } } }}
            />
            {receipt && !parsed && <Alert severity="warning" sx={{ mt: 1 }}>回执无法解析，请确认完整复制</Alert>}
            {parsed && (
              <Alert severity="success" sx={{ mt: 1 }}>
                公网 IP：{parsed.ip} · Agent 端口：{parsed.port} · frps 端口：{parsed.frps_port} · 版本：{parsed.ver}
                <br />证书指纹：<code style={{ fontSize: 12 }}>{parsed.fp}</code>
              </Alert>
            )}
          </Box>
          <Divider />
          <Stack direction="row" spacing={2}>
            <TextField label="节点名称" fullWidth value={name} onChange={(e) => setName(e.target.value)} />
            <TextField label="地区" select fullWidth value={region} onChange={(e) => setRegion(e.target.value)} sx={{ maxWidth: 160 }}>
              <MenuItem value="overseas">国外</MenuItem>
              <MenuItem value="domestic">国内</MenuItem>
            </TextField>
          </Stack>
        </Stack>
      </DialogContent>
      <DialogActions sx={{ px: 3, pb: 2 }}>
        <Button color="inherit" onClick={onClose} disabled={create.isPending}>取消</Button>
        <Button variant="contained" onClick={submit} disabled={create.isPending || !parsed}>
          {create.isPending ? '验证中…' : '验证并添加'}
        </Button>
      </DialogActions>
    </Dialog>
  )
}
