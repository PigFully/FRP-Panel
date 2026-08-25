import { Button, Dialog, DialogActions, DialogContent, DialogContentText, DialogTitle, TextField } from '@mui/material'
import { useEffect, useState, type ReactNode } from 'react'

export interface ConfirmProps {
  open: boolean
  title: string
  body?: ReactNode
  danger?: boolean
  confirmText?: string
  // If set, the user must type this exact string to enable the confirm button.
  requireText?: string
  loading?: boolean
  onCancel: () => void
  onConfirm: () => void
}

export function ConfirmDialog({ open, title, body, danger, confirmText = '确认', requireText, loading, onCancel, onConfirm }: ConfirmProps) {
  const [typed, setTyped] = useState('')
  useEffect(() => {
    if (open) setTyped('')
  }, [open])
  const canConfirm = !requireText || typed === requireText
  return (
    <Dialog open={open} onClose={loading ? undefined : onCancel} maxWidth="xs" fullWidth>
      <DialogTitle sx={{ fontWeight: 700 }}>{title}</DialogTitle>
      <DialogContent>
        {typeof body === 'string' ? <DialogContentText>{body}</DialogContentText> : body}
        {requireText && (
          <TextField
            autoFocus
            fullWidth
            size="small"
            sx={{ mt: 2 }}
            placeholder={`请输入 ${requireText} 以确认`}
            value={typed}
            onChange={(e) => setTyped(e.target.value)}
          />
        )}
      </DialogContent>
      <DialogActions sx={{ px: 3, pb: 2 }}>
        <Button onClick={onCancel} disabled={loading} color="inherit">
          取消
        </Button>
        <Button onClick={onConfirm} disabled={!canConfirm || loading} variant="contained" color={danger ? 'error' : 'primary'}>
          {confirmText}
        </Button>
      </DialogActions>
    </Dialog>
  )
}
