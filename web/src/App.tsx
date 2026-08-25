import { Box, CircularProgress } from '@mui/material'
import { lazy, Suspense, useEffect } from 'react'
import { Navigate, Route, Routes, useNavigate } from 'react-router-dom'
import { useMe } from './api/hooks'
import { Layout } from './components/Layout'
import { RealtimeProvider } from './realtime/RealtimeProvider'

// Route-level code splitting: each page is its own chunk, prefetched on idle.
const Login = lazy(() => import('./pages/Login'))
const Overview = lazy(() => import('./pages/Overview'))
const Nodes = lazy(() => import('./pages/Nodes'))
const Mappings = lazy(() => import('./pages/Mappings'))
const Logs = lazy(() => import('./pages/Logs'))
const Settings = lazy(() => import('./pages/Settings'))

function FullLoader() {
  return (
    <Box sx={{ height: '100vh', display: 'grid', placeItems: 'center' }}>
      <CircularProgress />
    </Box>
  )
}

function AuthWatcher() {
  const nav = useNavigate()
  useEffect(() => {
    const h = () => nav('/login', { replace: true })
    window.addEventListener('frpanel-unauthorized', h)
    return () => window.removeEventListener('frpanel-unauthorized', h)
  }, [nav])
  return null
}

function Protected() {
  const { isLoading, isError } = useMe()
  if (isLoading) return <FullLoader />
  if (isError) return <Navigate to="/login" replace />
  return (
    <RealtimeProvider>
      <Layout>
        <Suspense fallback={<FullLoader />}>
          <Routes>
            <Route path="/" element={<Overview />} />
            <Route path="/nodes" element={<Nodes />} />
            <Route path="/mappings" element={<Mappings />} />
            <Route path="/logs" element={<Logs />} />
            <Route path="/settings" element={<Settings />} />
            <Route path="*" element={<Navigate to="/" replace />} />
          </Routes>
        </Suspense>
      </Layout>
    </RealtimeProvider>
  )
}

export default function App() {
  // Prefetch route chunks after first paint so navigation is instant while the
  // initial bundle stays small.
  useEffect(() => {
    const id = window.setTimeout(() => {
      void import('./pages/Overview'); void import('./pages/Nodes'); void import('./pages/Mappings')
    }, 800)
    return () => clearTimeout(id)
  }, [])
  return (
    <>
      <AuthWatcher />
      <Suspense fallback={<FullLoader />}>
        <Routes>
          <Route path="/login" element={<Login />} />
          <Route path="/*" element={<Protected />} />
        </Routes>
      </Suspense>
    </>
  )
}
