import { Box, Card, CardContent, Chip, IconButton, Skeleton, Stack, Tooltip, Typography } from '@mui/material'
import ContentCopyRoundedIcon from '@mui/icons-material/ContentCopyRounded'
import HelpOutlineRoundedIcon from '@mui/icons-material/HelpOutlineRounded'
import type { ReactNode } from 'react'
import { useState } from 'react'

// Explanation shown on the "?" hint next to every latency value.
export const FRP_LATENCY_HELP =
  '这是经过 frps → frpc → 本地服务的完整 FRP 链路往返延迟（接入公网映射端口的真实耗时），并非仅到节点的网络延迟。面板按设定间隔定时 TCP 探测。'

// HelpHint renders a small "?" icon with a hover tooltip.
export function HelpHint({ text }: { text: string }) {
  return (
    <Tooltip title={text} arrow>
      <HelpOutlineRoundedIcon sx={{ fontSize: 15, color: 'text.secondary', cursor: 'help', verticalAlign: 'middle' }} />
    </Tooltip>
  )
}

export function StatCard({ label, value, icon, color = '#2065D1' }: { label: string; value: ReactNode; icon?: ReactNode; color?: string }) {
  return (
    <Card sx={{ height: '100%' }}>
      <CardContent>
        <Stack direction="row" justifyContent="space-between" alignItems="flex-start">
          <Box>
            <Typography variant="body2" color="text.secondary" fontWeight={600}>
              {label}
            </Typography>
            <Typography variant="h4" sx={{ mt: 1 }}>
              {value}
            </Typography>
          </Box>
          {icon && (
            <Box sx={{ width: 44, height: 44, borderRadius: 2, display: 'grid', placeItems: 'center', bgcolor: color + '1F', color }}>
              {icon}
            </Box>
          )}
        </Stack>
      </CardContent>
    </Card>
  )
}

export function StatusBadge({ online, labels = ['在线', '离线'] }: { online: boolean; labels?: [string, string] }) {
  return (
    <Chip
      size="small"
      label={online ? labels[0] : labels[1]}
      sx={{
        fontWeight: 700,
        color: online ? '#118D57' : '#B71D18',
        bgcolor: online ? 'rgba(34,197,94,0.16)' : 'rgba(255,86,48,0.16)',
      }}
    />
  )
}

// LatencyChip renders a color-coded TCP RTT (0=unknown, -1=timeout).
export function LatencyChip({ ms, size = 'small' }: { ms: number; size?: 'small' | 'medium' }) {
  let label: string
  let fg: string
  let bg: string
  if (ms == null || ms === 0) {
    label = '测量中'; fg = '#637381'; bg = 'rgba(145,158,171,0.16)'
  } else if (ms < 0) {
    label = '超时'; fg = '#B71D18'; bg = 'rgba(255,86,48,0.16)'
  } else {
    label = `${ms} ms`
    if (ms < 80) { fg = '#118D57'; bg = 'rgba(34,197,94,0.16)' }
    else if (ms < 150) { fg = '#006C9C'; bg = 'rgba(0,184,217,0.16)' }
    else if (ms < 300) { fg = '#B76E00'; bg = 'rgba(255,171,0,0.16)' }
    else { fg = '#B71D18'; bg = 'rgba(255,86,48,0.16)' }
  }
  return (
    <Tooltip title={FRP_LATENCY_HELP} arrow>
      <Chip size={size} label={label} sx={{ fontWeight: 700, color: fg, bgcolor: bg, cursor: 'help' }} />
    </Tooltip>
  )
}

export function EmptyState({ title, hint, action }: { title: string; hint?: string; action?: ReactNode }) {
  return (
    <Stack alignItems="center" justifyContent="center" spacing={1.5} sx={{ py: 8, textAlign: 'center' }}>
      <Box sx={{ fontSize: 56, opacity: 0.5 }}>📭</Box>
      <Typography variant="h6">{title}</Typography>
      {hint && (
        <Typography variant="body2" color="text.secondary" sx={{ maxWidth: 360 }}>
          {hint}
        </Typography>
      )}
      {action}
    </Stack>
  )
}

export function CopyField({ value, mono = true }: { value: string; mono?: boolean }) {
  const [copied, setCopied] = useState(false)
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(value)
    } catch {
      const ta = document.createElement('textarea')
      ta.value = value
      document.body.appendChild(ta)
      ta.select()
      document.execCommand('copy')
      ta.remove()
    }
    setCopied(true)
    setTimeout(() => setCopied(false), 1200)
  }
  return (
    <Stack direction="row" alignItems="center" spacing={0.5} sx={{ minWidth: 0 }}>
      <Typography
        variant="body2"
        sx={{ fontFamily: mono ? 'monospace' : undefined, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}
        title={value}
      >
        {value}
      </Typography>
      <Tooltip title={copied ? '已复制' : '复制'}>
        <IconButton size="small" onClick={copy}>
          <ContentCopyRoundedIcon fontSize="inherit" />
        </IconButton>
      </Tooltip>
    </Stack>
  )
}

export function TableSkeleton({ rows = 5, cols = 5 }: { rows?: number; cols?: number }) {
  return (
    <Box sx={{ p: 2 }}>
      {Array.from({ length: rows }).map((_, i) => (
        <Stack key={i} direction="row" spacing={2} sx={{ mb: 1.5 }}>
          {Array.from({ length: cols }).map((__, j) => (
            <Skeleton key={j} variant="rounded" height={28} sx={{ flex: 1 }} />
          ))}
        </Stack>
      ))}
    </Box>
  )
}
