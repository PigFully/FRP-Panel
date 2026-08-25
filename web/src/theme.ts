import { createTheme, type Theme } from '@mui/material/styles'

// Design tokens extracted from the spec (§4): blue accent, Minimal-UI style.
const PRIMARY = { main: '#2065D1', dark: '#1C54B2', light: '#6BA6FF' }

export type Mode = 'light' | 'dark'

export function createAppTheme(mode: Mode): Theme {
  const light = mode === 'light'
  return createTheme({
    palette: {
      mode,
      primary: PRIMARY,
      success: { main: '#22C55E' },
      warning: { main: '#FFAB00' },
      error: { main: '#FF5630' },
      info: { main: '#00B8D9' },
      background: {
        default: light ? '#FFFFFF' : '#141A21',
        paper: light ? '#FFFFFF' : '#1C252E',
      },
      text: {
        primary: light ? '#1C252E' : '#FFFFFF',
        secondary: light ? '#637381' : '#919EAB',
      },
      divider: 'rgba(145,158,171,0.2)',
    },
    shape: { borderRadius: 8 },
    typography: {
      fontFamily:
        '"Public Sans", -apple-system, "Segoe UI", Roboto, "PingFang SC", "Microsoft YaHei", "Noto Sans SC", sans-serif',
      fontSize: 13.5,
      h4: { fontWeight: 700, fontSize: '1.5rem' },
      h5: { fontWeight: 700, fontSize: '1.25rem' },
      h6: { fontWeight: 700, fontSize: '1.02rem' },
      subtitle1: { fontSize: '0.95rem' },
      subtitle2: { fontSize: '0.85rem', fontWeight: 700 },
      body1: { fontSize: '0.9rem' },
      body2: { fontSize: '0.83rem' },
      button: { fontWeight: 700, textTransform: 'none' },
    },
    components: {
      MuiCssBaseline: {
        styleOverrides: {
          body: { backgroundColor: light ? '#FFFFFF' : '#141A21' },
          '*::-webkit-scrollbar': { width: 8, height: 8 },
          '*::-webkit-scrollbar-thumb': { background: 'rgba(145,158,171,0.4)', borderRadius: 8 },
        },
      },
      MuiPaper: {
        styleOverrides: {
          root: {
            backgroundImage: 'none',
            borderRadius: 16,
            boxShadow: light
              ? '0 1px 2px 0 rgba(145,158,171,0.2), 0 0 2px 0 rgba(145,158,171,0.24)'
              : '0 1px 2px 0 rgba(0,0,0,0.4)',
          },
        },
      },
      MuiButton: {
        styleOverrides: {
          root: { borderRadius: 8 },
          containedPrimary: { boxShadow: 'none', '&:hover': { backgroundColor: PRIMARY.dark, boxShadow: 'none' } },
          sizeLarge: { height: 48 },
        },
      },
      MuiOutlinedInput: { styleOverrides: { root: { borderRadius: 8 } } },
      MuiCard: { styleOverrides: { root: { borderRadius: 16 } } },
      MuiChip: { styleOverrides: { root: { fontWeight: 600 } } },
      MuiTableCell: { styleOverrides: { head: { fontWeight: 700, color: light ? '#637381' : '#919EAB' } } },
    },
  })
}

// Status tint helpers (12% alpha backing per spec).
export const statusTint = {
  success: { bg: 'rgba(34,197,94,0.12)', fg: '#118D57' },
  successDark: { bg: 'rgba(34,197,94,0.16)', fg: '#5BE49B' },
  error: { bg: 'rgba(255,86,48,0.12)', fg: '#B71D18' },
  errorDark: { bg: 'rgba(255,86,48,0.16)', fg: '#FFAC82' },
  warning: { bg: 'rgba(255,171,0,0.12)', fg: '#B76E00' },
  info: { bg: 'rgba(0,184,217,0.12)', fg: '#006C9C' },
}
