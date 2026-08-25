import {
  AppBar, Avatar, Box, Divider, Drawer, IconButton, List, ListItemButton, ListItemIcon, ListItemText,
  Menu, MenuItem, Stack, Toolbar, Tooltip, Typography, useMediaQuery, useTheme,
} from '@mui/material'
import DashboardRoundedIcon from '@mui/icons-material/DashboardRounded'
import HubRoundedIcon from '@mui/icons-material/HubRounded'
import SwapHorizRoundedIcon from '@mui/icons-material/SwapHorizRounded'
import ReceiptLongRoundedIcon from '@mui/icons-material/ReceiptLongRounded'
import SettingsRoundedIcon from '@mui/icons-material/SettingsRounded'
import LightModeRoundedIcon from '@mui/icons-material/LightModeRounded'
import DarkModeRoundedIcon from '@mui/icons-material/DarkModeRounded'
import MenuRoundedIcon from '@mui/icons-material/MenuRounded'
import MenuOpenRoundedIcon from '@mui/icons-material/MenuOpenRounded'
import LogoutRoundedIcon from '@mui/icons-material/LogoutRounded'
import { useEffect, useState, type ReactNode } from 'react'
import { NavLink, useLocation, useNavigate } from 'react-router-dom'
import { Logo } from '../brand'
import { useColorMode } from '../ColorMode'
import { useLogout, useMe } from '../api/hooks'

const FULL = 280
const MINI = 76
const NAV = [
  { to: '/', label: '数据概览', icon: <DashboardRoundedIcon /> },
  { to: '/nodes', label: '节点列表', icon: <HubRoundedIcon /> },
  { to: '/mappings', label: '映射管理', icon: <SwapHorizRoundedIcon /> },
  { to: '/logs', label: '操作日志', icon: <ReceiptLongRoundedIcon /> },
  { to: '/settings', label: '面板设置', icon: <SettingsRoundedIcon /> },
]

export function Layout({ children }: { children: ReactNode }) {
  const theme = useTheme()
  const { mode, toggle } = useColorMode()
  const mdUp = useMediaQuery(theme.breakpoints.up('md'))
  const [mobileOpen, setMobileOpen] = useState(false)
  const [userCollapsed, setUserCollapsed] = useState(() => {
    const s = localStorage.getItem('frpanel-collapsed')
    if (s === null) return window.innerWidth < 1200 // default to icon rail on narrower desktops
    return s === '1'
  })
  const { data: me } = useMe()
  const logout = useLogout()
  const nav = useNavigate()
  const [anchor, setAnchor] = useState<null | HTMLElement>(null)
  const loc = useLocation()

  // Manual collapse (toggle in the top bar); defaults to an icon rail on
  // narrower desktops (spec §5.7 responsive behavior).
  const mini = mdUp && userCollapsed
  const width = mini ? MINI : FULL

  const panelName = me?.panel_name || 'FRPanel'
  useEffect(() => {
    const page = NAV.find((n) => n.to === loc.pathname)?.label || ''
    document.title = page ? `${page} — ${panelName}` : panelName
  }, [loc.pathname, panelName])
  useEffect(() => setMobileOpen(false), [loc.pathname])
  const toggleCollapse = () => {
    setUserCollapsed((c) => {
      localStorage.setItem('frpanel-collapsed', c ? '0' : '1')
      return !c
    })
  }

  const doLogout = async () => {
    try { await logout.mutateAsync() } catch { /* ignore */ }
    nav('/login')
  }

  const drawerInner = (collapsed: boolean) => (
    <Box sx={{ height: '100%', display: 'flex', flexDirection: 'column' }}>
      <Stack direction="row" alignItems="center" justifyContent={collapsed ? 'center' : 'flex-start'} spacing={1.5} sx={{ px: collapsed ? 0 : 3, height: 72 }}>
        <Logo size={34} />
        {!collapsed && <Typography variant="h6" fontWeight={800}>{panelName}</Typography>}
      </Stack>
      <List sx={{ px: collapsed ? 1 : 2, flex: 1 }}>
        {NAV.map((n) => {
          const btn = (
            <ListItemButton
              key={n.to} component={NavLink} to={n.to} end={n.to === '/'}
              sx={{
                borderRadius: 2, mb: 0.5, justifyContent: collapsed ? 'center' : 'flex-start', px: collapsed ? 1.5 : 2,
                '&.active': { bgcolor: 'rgba(32,101,209,0.12)', color: 'primary.main' },
                '&.active .MuiListItemIcon-root': { color: 'primary.main' },
              }}
            >
              <ListItemIcon sx={{ minWidth: collapsed ? 0 : 40, justifyContent: 'center' }}>{n.icon}</ListItemIcon>
              {!collapsed && <ListItemText primaryTypographyProps={{ fontWeight: 600 }}>{n.label}</ListItemText>}
            </ListItemButton>
          )
          return collapsed ? <Tooltip key={n.to} title={n.label} placement="right">{btn}</Tooltip> : btn
        })}
      </List>
    </Box>
  )

  return (
    <Box sx={{ display: 'flex', minHeight: '100vh' }}>
      <AppBar
        position="fixed" elevation={0} color="inherit"
        sx={{
          width: { md: `calc(100% - ${width}px)` }, ml: { md: `${width}px` },
          borderBottom: 1, borderColor: 'divider', backdropFilter: 'blur(6px)',
          bgcolor: mode === 'light' ? 'rgba(255,255,255,0.8)' : 'rgba(20,26,33,0.8)',
          transition: 'width .2s, margin .2s',
        }}
      >
        <Toolbar>
          {!mdUp && <IconButton edge="start" onClick={() => setMobileOpen(true)} sx={{ mr: 1 }}><MenuRoundedIcon /></IconButton>}
          {mdUp && (
            <Tooltip title={userCollapsed ? '展开侧边栏' : '收起侧边栏'}>
              <IconButton edge="start" onClick={toggleCollapse} sx={{ mr: 1 }}>
                {userCollapsed ? <MenuRoundedIcon /> : <MenuOpenRoundedIcon />}
              </IconButton>
            </Tooltip>
          )}
          <Box sx={{ flex: 1 }} />
          <Tooltip title={mode === 'light' ? '切换深色' : '切换浅色'}>
            <IconButton onClick={toggle}>{mode === 'light' ? <DarkModeRoundedIcon /> : <LightModeRoundedIcon />}</IconButton>
          </Tooltip>
          <IconButton onClick={(e) => setAnchor(e.currentTarget)}>
            <Avatar sx={{ width: 34, height: 34, bgcolor: 'primary.main', fontSize: 15 }}>{(me?.username || 'A').slice(0, 1).toUpperCase()}</Avatar>
          </IconButton>
          <Menu anchorEl={anchor} open={!!anchor} onClose={() => setAnchor(null)}>
            <MenuItem disabled>{me?.username}</MenuItem>
            <Divider />
            <MenuItem onClick={doLogout}><ListItemIcon><LogoutRoundedIcon fontSize="small" /></ListItemIcon>退出登录</MenuItem>
          </Menu>
        </Toolbar>
      </AppBar>

      <Box component="nav" sx={{ width: { md: width }, flexShrink: { md: 0 }, transition: 'width .2s' }}>
        {mdUp ? (
          <Drawer variant="permanent" open sx={{ '& .MuiDrawer-paper': { width, borderRight: 1, borderColor: 'divider', bgcolor: mode === 'light' ? '#F4F6F8' : '#16181D', transition: 'width .2s', overflowX: 'hidden' } }}>
            {drawerInner(mini)}
          </Drawer>
        ) : (
          <Drawer variant="temporary" open={mobileOpen} onClose={() => setMobileOpen(false)} ModalProps={{ keepMounted: true }} sx={{ '& .MuiDrawer-paper': { width: FULL } }}>
            {drawerInner(false)}
          </Drawer>
        )}
      </Box>

      <Box component="main" sx={{ flexGrow: 1, width: { md: `calc(100% - ${width}px)` }, minWidth: 0 }}>
        <Toolbar />
        <Box sx={{ p: { xs: 2, md: 3 }, maxWidth: 1200, mx: 'auto' }}>{children}</Box>
      </Box>
    </Box>
  )
}
