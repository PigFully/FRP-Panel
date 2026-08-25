import { Box, Button, Stack, Typography } from '@mui/material'
import { Component, type ReactNode } from 'react'

interface State { err: Error | null }

// Global white-screen fallback with a refresh button (spec §5.7 error state).
export class ErrorBoundary extends Component<{ children: ReactNode }, State> {
  state: State = { err: null }
  static getDerivedStateFromError(err: Error): State {
    return { err }
  }
  render() {
    if (this.state.err) {
      return (
        <Box sx={{ height: '100vh', display: 'grid', placeItems: 'center', p: 3 }}>
          <Stack spacing={2} alignItems="center" textAlign="center">
            <Typography variant="h5">页面出现异常</Typography>
            <Typography variant="body2" color="text.secondary">
              {this.state.err.message || '未知错误'}
            </Typography>
            <Button variant="contained" onClick={() => window.location.reload()}>
              刷新页面
            </Button>
          </Stack>
        </Box>
      )
    }
    return this.props.children
  }
}
