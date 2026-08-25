import {
  Alert, Box, Button, Card, CardContent, CardHeader, Divider, FormControlLabel, Stack, Switch, TextField, Typography,
} from '@mui/material'
import { useEffect, useState } from 'react'
import { ApiError } from '../api/client'
import { useBackup, useCheckUpdate, useCleanLogs, useChangePassword, useSelfUpdate, useSettings, useUpdateSettings } from '../api/hooks'
import { ConfirmDialog } from '../components/ConfirmDialog'
import { CopyField } from '../components/common'
import { useToast } from '../components/Toast'

export default function Settings() {
  const { data } = useSettings()
  const upd = useUpdateSettings()
  const toast = useToast()
  const [name, setName] = useState('')
  const [rate, setRate] = useState('200')
  const [ping, setPing] = useState('15')
  const [autoBackup, setAutoBackup] = useState(false)
  const [mirror, setMirror] = useState('')

  useEffect(() => {
    if (data) { setName(data.panel_name); setRate(String(data.conn_rate_limit)); setPing(String(data.tcp_ping_interval)); setAutoBackup(data.auto_backup); setMirror(data.update_mirror ?? '') }
  }, [data])

  const saveGeneral = async () => {
    try {
      await upd.mutateAsync({ panel_name: name, conn_rate_limit: Number(rate), tcp_ping_interval: Number(ping), auto_backup: autoBackup, update_mirror: mirror.trim() })
      toast.success('设置已保存')
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : '保存失败')
    }
  }

  return (
    <Stack spacing={3}>
      <Typography variant="h4">面板设置</Typography>

      <Card>
        <CardHeader title="常规" titleTypographyProps={{ variant: 'h6' }} />
        <CardContent>
          <Stack spacing={2.5}>
            <TextField label="面板名称" value={name} onChange={(e) => setName(e.target.value)} sx={{ maxWidth: 360 }} helperText="显示在顶栏与浏览器标题" />
            <TextField
              label="最大回源数（每秒新建连接上限 / 每个公网端口）" value={rate}
              onChange={(e) => setRate(e.target.value.replace(/\D/g, ''))} sx={{ maxWidth: 360 }}
              helperText="0 表示不限制；默认 200。用于防止极端 TCP 攻击导致节点 IP 被误判为 PCDN / 对外攻击（由节点 nftables 执行）"
            />
            <TextField
              label="节点 TCP 延迟刷新间隔（秒）" value={ping}
              onChange={(e) => setPing(e.target.value.replace(/\D/g, ''))} sx={{ maxWidth: 360 }}
              helperText="面板每隔该秒数对各节点公网 IP 做一次 TCP Ping（连接探测），显示在节点列表与映射目标中。范围 5-3600，默认 15"
            />
            <TextField
              label="GitHub 镜像前缀（可选）" value={mirror}
              onChange={(e) => setMirror(e.target.value)} sx={{ maxWidth: 360 }}
              helperText={`在线升级/检查更新直连失败时的回退前缀（ghproxy 风格，如 https://mirror.example.com）。更新源：${data?.update_base || '未配置'}`}
            />
            <FormControlLabel control={<Switch checked={autoBackup} onChange={(e) => setAutoBackup(e.target.checked)} />} label="每日自动备份数据库" />
            <Box><Button variant="contained" onClick={saveGeneral} disabled={upd.isPending}>保存设置</Button></Box>
          </Stack>
        </CardContent>
      </Card>

      <PasswordCard />
      <MaintenanceCard />
      <UpdateCard current={data?.version} />
    </Stack>
  )
}

function PasswordCard() {
  const chg = useChangePassword()
  const toast = useToast()
  const [oldPw, setOld] = useState('')
  const [newPw, setNew] = useState('')
  const [confirm, setConfirm] = useState('')
  const submit = async () => {
    if (newPw.length < 8) return toast.error('新密码至少 8 位')
    if (newPw !== confirm) return toast.error('两次输入的新密码不一致')
    try {
      await chg.mutateAsync({ old_password: oldPw, new_password: newPw })
      toast.success('密码已修改，其他会话已失效')
      setOld(''); setNew(''); setConfirm('')
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : '修改失败')
    }
  }
  return (
    <Card>
      <CardHeader title="修改管理员密码" subheader="修改后其他浏览器/设备上的登录会立即失效" titleTypographyProps={{ variant: 'h6' }} />
      <CardContent>
        <Stack spacing={2.5} sx={{ maxWidth: 360 }}>
          <TextField label="原密码" type="password" value={oldPw} onChange={(e) => setOld(e.target.value)} />
          <TextField label="新密码" type="password" value={newPw} onChange={(e) => setNew(e.target.value)} />
          <TextField label="确认新密码" type="password" value={confirm} onChange={(e) => setConfirm(e.target.value)} />
          <Box><Button variant="contained" onClick={submit} disabled={chg.isPending}>修改密码</Button></Box>
        </Stack>
      </CardContent>
    </Card>
  )
}

function MaintenanceCard() {
  const clean = useCleanLogs()
  const backup = useBackup()
  const toast = useToast()
  const [confirm, setConfirm] = useState<null | boolean>(null) // true=all
  const doClean = async () => {
    try {
      const r: any = await clean.mutateAsync(confirm === true)
      toast.success(`已清理 ${r?.deleted ?? 0} 条日志`)
      setConfirm(null)
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : '清理失败')
    }
  }
  const doBackup = async () => {
    try {
      const r = await backup.mutateAsync()
      toast.success(`备份完成：${r.file}`)
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : '备份失败')
    }
  }
  return (
    <Card>
      <CardHeader title="维护" titleTypographyProps={{ variant: 'h6' }} />
      <CardContent>
        <Stack spacing={2}>
          <Stack direction="row" spacing={1.5} flexWrap="wrap">
            <Button variant="outlined" onClick={() => setConfirm(false)}>清理 30 天前日志</Button>
            <Button variant="outlined" color="error" onClick={() => setConfirm(true)}>清理全部日志</Button>
            <Button variant="outlined" onClick={doBackup} disabled={backup.isPending}>{backup.isPending ? '备份中…' : '立即备份数据库'}</Button>
          </Stack>
          <Typography variant="caption" color="text.secondary">备份保存在面板 /opt/frp-panel/backups/，保留最近 7 份。</Typography>
        </Stack>
      </CardContent>
      <ConfirmDialog
        open={confirm !== null} danger={confirm === true} title="清理操作日志"
        body={confirm === true ? '将删除全部操作日志，且不可恢复。确认继续？' : '将删除 30 天前的操作日志。确认继续？'}
        confirmText="确认清理" loading={clean.isPending} onCancel={() => setConfirm(null)} onConfirm={doClean}
      />
    </Card>
  )
}

function UpdateCard({ current }: { current?: string }) {
  const check = useCheckUpdate()
  const selfUpdate = useSelfUpdate()
  const toast = useToast()
  const [result, setResult] = useState<{ latest: string; has_update: boolean; upgrade_cmd?: string; message?: string } | null>(null)
  const [confirm, setConfirm] = useState(false)
  const [restarting, setRestarting] = useState(false)
  const run = async () => {
    try {
      const r = await check.mutateAsync()
      setResult(r)
      if (r.message) toast.show(r.message, 'warning')
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : '检查失败')
    }
  }
  const doSelfUpdate = async () => {
    try {
      await selfUpdate.mutateAsync()
      setConfirm(false)
      setRestarting(true)
      toast.success('新版本已就位，面板正在重启，页面将自动刷新…')
      // The panel restarts in ~2s; poll /healthz until it answers, then reload.
      const t0 = Date.now()
      const probe = () => {
        fetch('/healthz', { cache: 'no-store' })
          .then(() => location.reload())
          .catch(() => { if (Date.now() - t0 < 60000) setTimeout(probe, 1500) })
      }
      setTimeout(probe, 3000)
    } catch (e) {
      toast.error(e instanceof ApiError ? e.message : '在线升级失败')
    }
  }
  return (
    <Card>
      <CardHeader title="检查更新" subheader={`当前版本 ${current ?? ''}`} titleTypographyProps={{ variant: 'h6' }} />
      <CardContent>
        <Stack spacing={2}>
          <Box><Button variant="outlined" onClick={run} disabled={check.isPending}>{check.isPending ? '检查中…' : '检查更新'}</Button></Box>
          {result && !result.message && (
            result.has_update ? (
              <Alert
                severity="info"
                action={
                  <Button color="inherit" size="small" variant="outlined" onClick={() => setConfirm(true)} disabled={selfUpdate.isPending || restarting}>
                    {restarting ? '重启中…' : '立即在线升级'}
                  </Button>
                }
              >
                发现新版本 {result.latest}
                {result.upgrade_cmd && <Box sx={{ mt: 1 }}><CopyField value={result.upgrade_cmd} /></Box>}
              </Alert>
            ) : (
              <Alert severity="success">已是最新版本（{result.latest || current}）</Alert>
            )
          )}
          {result?.message && <Divider />}
        </Stack>
      </CardContent>
      <ConfirmDialog
        open={confirm} title="在线升级面板"
        body={`将下载并校验 ${result?.latest ?? '最新'} 版面板二进制，替换后自动重启。重启期间（数秒）网页短暂不可用、隧道会随 frpc 重建闪断后自动恢复。确认升级？`}
        confirmText="升级并重启" loading={selfUpdate.isPending}
        onCancel={() => setConfirm(false)} onConfirm={doSelfUpdate}
      />
    </Card>
  )
}
