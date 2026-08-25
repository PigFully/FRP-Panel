import { CssBaseline, ThemeProvider } from '@mui/material'
import { createContext, useContext, useEffect, useMemo, useState, type ReactNode } from 'react'
import { createAppTheme, type Mode } from './theme'

interface ColorModeCtx { mode: Mode; toggle: () => void }
const Ctx = createContext<ColorModeCtx>({ mode: 'light', toggle: () => {} })
export const useColorMode = () => useContext(Ctx)

function initialMode(): Mode {
  const saved = localStorage.getItem('frpanel-theme')
  if (saved === 'light' || saved === 'dark') return saved
  return window.matchMedia?.('(prefers-color-scheme: dark)').matches ? 'dark' : 'light'
}

export function ColorModeProvider({ children }: { children: ReactNode }) {
  const [mode, setMode] = useState<Mode>(initialMode)
  useEffect(() => {
    localStorage.setItem('frpanel-theme', mode)
  }, [mode])
  const theme = useMemo(() => createAppTheme(mode), [mode])
  const ctx = useMemo(() => ({ mode, toggle: () => setMode((m) => (m === 'light' ? 'dark' : 'light')) }), [mode])
  return (
    <Ctx.Provider value={ctx}>
      <ThemeProvider theme={theme}>
        <CssBaseline />
        {children}
      </ThemeProvider>
    </Ctx.Provider>
  )
}
