import {
  Card, Chip, MenuItem, Paper, Stack, Table, TableBody, TableCell, TableContainer, TableHead,
  TablePagination, TableRow, TextField, Typography,
} from '@mui/material'
import { useState } from 'react'
import { useLogs, useNodes } from '../api/hooks'
import { EmptyState, TableSkeleton } from '../components/common'
import { fmtTime } from '../utils/format'
import { logType } from './Overview'

function typeChip(t: string) {
  const map: Record<string, { c: 'info' | 'warning' | 'default' }> = {
    frp_event: { c: 'info' }, reconcile: { c: 'warning' }, panel_op: { c: 'default' },
  }
  return <Chip size="small" color={map[t]?.c ?? 'default'} label={logType(t)} variant={t === 'panel_op' ? 'outlined' : 'filled'} />
}

export default function Logs() {
  const [type, setType] = useState('')
  const [nodeId, setNodeId] = useState<number | ''>('')
  const [page, setPage] = useState(0)
  const [size, setSize] = useState(20)
  const { data: nodes } = useNodes()
  const { data, isLoading } = useLogs({ type: type || undefined, node_id: nodeId || undefined, page: page + 1, size })

  return (
    <Stack spacing={3}>
      <Typography variant="h4">操作日志</Typography>
      <Stack direction="row" spacing={1.5} flexWrap="wrap">
        <TextField size="small" select label="类型" value={type} onChange={(e) => { setType(e.target.value); setPage(0) }} sx={{ minWidth: 160 }}>
          <MenuItem value="">全部类型</MenuItem>
          <MenuItem value="frp_event">FRP 事件</MenuItem>
          <MenuItem value="reconcile">对账修复</MenuItem>
          <MenuItem value="panel_op">面板操作</MenuItem>
        </TextField>
        <TextField size="small" select label="节点" value={nodeId} onChange={(e) => { setNodeId(e.target.value ? Number(e.target.value) : ''); setPage(0) }} sx={{ minWidth: 160 }}>
          <MenuItem value="">全部节点</MenuItem>
          {(nodes ?? []).map((n) => <MenuItem key={n.id} value={n.id}>{n.name}</MenuItem>)}
        </TextField>
      </Stack>

      <Card>
        {isLoading ? (
          <TableSkeleton rows={6} cols={4} />
        ) : (data?.items.length ?? 0) === 0 ? (
          <EmptyState title="暂无日志" hint="节点上下线、映射变更、对账修复等事件会记录在这里。" />
        ) : (
          <>
            <TableContainer component={Paper} elevation={0}>
              <Table>
                <TableHead>
                  <TableRow><TableCell width={180}>时间</TableCell><TableCell width={120}>类型</TableCell><TableCell width={140}>来源</TableCell><TableCell>详情</TableCell></TableRow>
                </TableHead>
                <TableBody>
                  {data!.items.map((l) => (
                    <TableRow key={l.id} hover>
                      <TableCell>{fmtTime(l.created_at)}</TableCell>
                      <TableCell>{typeChip(l.type)}</TableCell>
                      <TableCell>{l.source || '—'}</TableCell>
                      <TableCell><Typography variant="body2" sx={{ whiteSpace: 'pre-wrap' }}>{l.detail}</Typography></TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </TableContainer>
            <TablePagination
              component="div" count={data!.total} page={page} rowsPerPage={size}
              onPageChange={(_, p) => setPage(p)} onRowsPerPageChange={(e) => { setSize(Number(e.target.value)); setPage(0) }}
              rowsPerPageOptions={[20, 50, 100]} labelRowsPerPage="每页"
            />
          </>
        )}
      </Card>
    </Stack>
  )
}
