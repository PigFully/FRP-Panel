import { Alert, Snackbar } from '@mui/material'
import { createContext, useCallback, useContext, useMemo, useState, type ReactNode } from 'react'

type Severity = 'success' | 'error' | 'info' | 'warning'
interface ToastState { open: boolean; msg: string; sev: Severity }
interface ToastCtx { show: (msg: string, sev?: Severity) => void; success: (m: string) => void; error: (m: string) => void }

const Ctx = createContext<ToastCtx>({ show: () => {}, success: () => {}, error: () => {} })
export const useToast = () => useContext(Ctx)

export function ToastProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<ToastState>({ open: false, msg: '', sev: 'info' })
  const show = useCallback((msg: string, sev: Severity = 'info') => setState({ open: true, msg, sev }), [])
  const api = useMemo<ToastCtx>(
    () => ({ show, success: (m) => show(m, 'success'), error: (m) => show(m, 'error') }),
    [show],
  )
  return (
    <Ctx.Provider value={api}>
      {children}
      <Snackbar
        open={state.open}
        autoHideDuration={4000}
        onClose={() => setState((s) => ({ ...s, open: false }))}
        anchorOrigin={{ vertical: 'top', horizontal: 'right' }}
      >
        <Alert
          severity={state.sev}
          variant="filled"
          onClose={() => setState((s) => ({ ...s, open: false }))}
          sx={{ boxShadow: 3, borderRadius: 2 }}
        >
          {state.msg}
        </Alert>
      </Snackbar>
    </Ctx.Provider>
  )
}
