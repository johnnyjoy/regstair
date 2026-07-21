import { QueryClient, QueryClientProvider } from '@tanstack/react-query'
import { Alert, CircularProgress, Stack, Typography, useMediaQuery } from '@mui/material'
import { useQuery } from '@tanstack/react-query'
import { lazy, StrictMode, Suspense } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter, Route, Routes } from 'react-router-dom'

import { apiRequest, SessionExpiredError } from './api/client'
import { AppShell } from './shell/AppShell'
import { ThemeModeProvider } from './theme/ThemeModeProvider'

const OverviewPage = lazy(() => import('./overview/OverviewPage').then((module) => ({ default: module.OverviewPage })))
const RequestsPage = lazy(() => import('./requests/RequestsPage').then((module) => ({ default: module.RequestsPage })))
const RequestDetailPage = lazy(() => import('./requests/RequestDetail').then((module) => ({ default: module.RequestDetailPage })))
const RoutesPage = lazy(() => import('./infrastructure/InfrastructurePages').then((module) => ({ default: module.RoutesPage })))
const RegistriesPage = lazy(() => import('./infrastructure/InfrastructurePages').then((module) => ({ default: module.RegistriesPage })))
const CachePage = lazy(() => import('./cache/CachePage').then((module) => ({ default: module.CachePage })))
const UsersPage = lazy(() => import('./users/UsersPage').then((module) => ({ default: module.UsersPage })))
const AccountPage = lazy(() => import('./account/AccountPage').then((module) => ({ default: module.AccountPage })))
const RegistryAccessPage = lazy(() => import('./account/RegistryAccessPage').then((module) => ({ default: module.RegistryAccessPage })))
const AuditPage = lazy(() => import('./audit/AuditPage').then((module) => ({ default: module.AuditPage })))
const LoginPage = lazy(() => import('./auth/PublicAuth').then((module) => ({ default: module.LoginPage })))
const SetupPage = lazy(() => import('./auth/PublicAuth').then((module) => ({ default: module.SetupPage })))

const queryClient = new QueryClient({
  defaultOptions: { queries: { retry: 1, staleTime: 15_000 } },
})

const nonce = document.querySelector<HTMLMetaElement>('meta[name="csp-nonce"]')?.content
const emotionCache = createCache({ key: 'regstair', nonce })

type Account = { username: string; access: 'admin' | 'user' }

function RegstairApp() {
  const compact = useMediaQuery('(max-width:899px)')
  const account = useQuery({ queryKey: ['account'], queryFn: () => apiRequest<Account>('/admin/api/account'), retry: false })
  const controlHealth = useQuery({ queryKey: ['global-health'], queryFn: () => apiRequest<{ status: string }>('/admin/api/health'), refetchInterval: 30_000, retry: false })
  const registryHealth = useQuery({ queryKey: ['source-health'], queryFn: () => apiRequest<{ sources: { status: string }[] }>('/admin/api/source-health'), refetchInterval: 30_000, retry: false })

  if (account.isLoading) return <Stack role="status" spacing={2} sx={{ alignItems: 'center', py: 12 }}><CircularProgress /><Typography>Opening Regstair</Typography></Stack>
  if (account.error instanceof SessionExpiredError) {
    window.location.replace('/login')
    return null
  }
  if (account.isError || !account.data) return <Alert severity="error" sx={{ m: 3 }}>The Regstair session could not be loaded.</Alert>

  const signOut = async () => {
    await apiRequest('/admin/api/logout', { method: 'POST' })
    window.location.assign('/login')
  }
  const health = controlHealth.isError || registryHealth.isError ? 'unavailable' : registryHealth.data?.sources.some((source) => !['healthy', 'disabled'].includes(source.status)) ? 'degraded' : controlHealth.data ? 'healthy' : 'degraded'
  return (
    <AppShell role={account.data.access} username={account.data.username} health={health} compact={compact} onSignOut={signOut}>
      <Suspense fallback={<Stack role="status" sx={{ alignItems: 'center', py: 10 }}><CircularProgress /></Stack>}>
        <Routes>
          <Route path="/" element={<OverviewPage />} />
          <Route path="/requests" element={<RequestsPage />} />
          <Route path="/requests/:id" element={<RequestDetailPage />} />
          <Route path="/routes" element={<RoutesPage />} />
          <Route path="/sources" element={<RegistriesPage />} />
          <Route path="/cache" element={<CachePage />} />
          <Route path="/admin/users" element={<UsersPage />} />
          <Route path="/admin/audit" element={<AuditPage />} />
          <Route path="/account" element={<AccountPage />} />
          <Route path="/registry-access" element={<RegistryAccessPage />} />
        </Routes>
      </Suspense>
    </AppShell>
  )
}

const publicPage = window.location.pathname === '/login' ? <LoginPage /> : window.location.pathname === '/setup' ? <SetupPage /> : null

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <CacheProvider value={emotionCache}>
      <ThemeModeProvider>
        <QueryClientProvider client={queryClient}>
          <BrowserRouter><Suspense fallback={<Stack role="status" sx={{ alignItems: 'center', py: 10 }}><CircularProgress /></Stack>}>{publicPage ?? <RegstairApp />}</Suspense></BrowserRouter>
        </QueryClientProvider>
      </ThemeModeProvider>
    </CacheProvider>
  </StrictMode>,
)
import createCache from '@emotion/cache'
import { CacheProvider } from '@emotion/react'
