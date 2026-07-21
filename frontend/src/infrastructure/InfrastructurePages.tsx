import ArrowForwardRounded from '@mui/icons-material/ArrowForwardRounded'
import { Alert, Box, Chip, CircularProgress, Divider, Link as MuiLink, Stack, Typography } from '@mui/material'
import { useQueries, useQuery } from '@tanstack/react-query'
import { Link } from 'react-router-dom'

import { apiRequest } from '../api/client'
import { EmptyState, OperationalPanel, PageHeader } from '../design/OperationalUI'

type RouteConfig = { name: string; match: string; precedence: number; pull: { sources: string[]; authoritative: string; external_fallback: boolean }; push: { destination: string; deny: boolean }; rewrite: { strip_prefix: string; add_prefix: string } }
type Source = { id: string; name: string; endpoint: string; type: string; enabled: boolean; routes: string[]; user_credentials: { pull: boolean; push: boolean } }
type Health = { id: string; status: string }

function Loading() { return <Stack role="status" sx={{ alignItems: 'center', py: 10 }}><CircularProgress /></Stack> }
export function RoutesView({ routes }: { routes: RouteConfig[] }) {
  return <Stack spacing={3}><PageHeader title="Routes" subtitle="Ordered policy for pulls, pushes, fallback, and namespace rewriting" />
    <Alert severity="info"><strong>File-managed configuration</strong><br />Routes currently come from the deployment YAML and are read-only here. Runtime route editing is being introduced through versioned, database-owned route sets; YAML will remain available for bootstrap, import, and export.</Alert>
    <OperationalPanel title={`Active route set · ${routes.length} routes`}>{routes.length === 0 ? <EmptyState title="No routes configured" detail="Import or configure a route set before directing client traffic through Regstair." /> : <Stack divider={<Divider />} sx={{ px: 2.25 }}>{routes.map((route) => <Box component="section" key={route.name} sx={{ py: 2.5 }}>
      <Stack direction={{ xs: 'column', sm: 'row' }} sx={{ alignItems: { sm: 'baseline' }, justifyContent: 'space-between' }}><Typography component="h2" variant="h2">{route.name}</Typography><Stack direction="row" spacing={1}><Chip label={`Match ${route.match || 'all references'}`} size="small" /><Chip label={`Priority ${route.precedence}`} size="small" variant="outlined" /></Stack></Stack>
      <Typography color="text.secondary" variant="caption">Pull resolution</Typography>
      <Stack aria-label={`${route.name} route path`} direction={{ xs: 'column', sm: 'row' }} spacing={1} sx={{ alignItems: 'center', my: 1.5 }}>{route.pull.sources.map((source, index) => <Stack direction={{ xs: 'column', sm: 'row' }} spacing={1} sx={{ alignItems: 'center' }} key={source}><Chip color={source === route.pull.authoritative ? 'primary' : 'default'} label={source} />{index < route.pull.sources.length - 1 && <ArrowForwardRounded sx={{ transform: { xs: 'rotate(90deg)', sm: 'none' } }} />}</Stack>)}</Stack>
      <Box sx={{ display: 'grid', gap: 2, gridTemplateColumns: { xs: '1fr', md: 'repeat(3, 1fr)' } }}><Box><Typography color="text.secondary" variant="caption">Authoritative source</Typography><Typography><strong>{route.pull.authoritative || 'None'}</strong></Typography></Box><Box><Typography color="text.secondary" variant="caption">External fallback</Typography><Typography>{route.pull.external_fallback ? 'Allowed after configured sources' : 'Blocked'}</Typography></Box><Box><Typography color="text.secondary" variant="caption">Push destination</Typography><Typography>{route.push.deny ? 'Push denied' : route.push.destination || 'Not configured'}</Typography></Box></Box>
      {(route.rewrite.strip_prefix || route.rewrite.add_prefix) && <Typography sx={{ mt: 2 }}><Typography color="text.secondary" component="span" variant="caption">Rewrite </Typography><code>{route.rewrite.strip_prefix || '∅'} → {route.rewrite.add_prefix || '∅'}</code></Typography>}
    </Box>)}</Stack>}</OperationalPanel></Stack>
}

export function RoutesPage() {
  const query = useQuery({ queryKey: ['routes'], queryFn: () => apiRequest<{ routes: RouteConfig[] }>('/admin/api/routes') })
  if (query.isLoading) return <Loading />
  if (query.isError || !query.data) return <Alert severity="error">Routes could not be loaded.</Alert>
  return <RoutesView routes={query.data.routes} />
}

export function RegistriesView({ sources, health }: { sources: Source[]; health: Health[] }) {
  const statuses = new Map(health.map((item) => [item.id, item.status]))
  return <Stack spacing={3}><PageHeader title="Registries" subtitle="Configured OCI endpoints, availability, route usage, and credential capability" />
    <OperationalPanel title={`Registry inventory · ${sources.length}`}><Stack divider={<Divider />} sx={{ px: 2.25 }}>{sources.map((source) => { const status = statuses.get(source.id) ?? (source.enabled ? 'not checked' : 'disabled'); const credentials = source.user_credentials.pull && source.user_credentials.push ? 'Pull and push credentials' : source.user_credentials.push ? 'Push credentials' : source.user_credentials.pull ? 'Pull credentials' : 'Challenge-first access'; return <Box component="section" key={source.id} sx={{ py: 2.5 }}><Stack direction={{ xs: 'column', sm: 'row' }} sx={{ justifyContent: 'space-between' }}><Box><Typography component="h3" sx={{ fontSize: 16, fontWeight: 720 }}>{source.name}</Typography><Typography color="text.secondary" sx={{ overflowWrap: 'anywhere' }}>{source.endpoint}</Typography></Box><Chip color={status === 'healthy' ? 'success' : status === 'unavailable' ? 'error' : 'default'} label={status} size="small" sx={{ alignSelf: 'flex-start' }} /></Stack><Box sx={{ display: 'grid', gap: 2, gridTemplateColumns: { xs: '1fr', sm: 'repeat(3, 1fr)' }, mt: 2 }}><Box><Typography color="text.secondary" variant="caption">Credential capability</Typography><Typography>{credentials}</Typography></Box><Box><Typography color="text.secondary" variant="caption">Used by routes</Typography><Typography>{source.routes.length ? source.routes.join(', ') : 'No routes'}</Typography></Box><Box><Typography color="text.secondary" variant="caption">Configuration</Typography><Typography>Read-only · YAML owned</Typography></Box></Box></Box> })}</Stack></OperationalPanel>
    <MuiLink component={Link} to="/routes">View routing topology</MuiLink>
  </Stack>
}

export function RegistriesPage() {
  const [sources, health] = useQueries({ queries: [{ queryKey: ['sources'], queryFn: () => apiRequest<{ sources: Source[] }>('/admin/api/sources') }, { queryKey: ['source-health'], queryFn: () => apiRequest<{ sources: Health[] }>('/admin/api/source-health') }] })
  if (sources.isLoading || health.isLoading) return <Loading />
  if (sources.isError || health.isError || !sources.data || !health.data) return <Alert severity="error">Registry state could not be loaded.</Alert>
  return <RegistriesView sources={sources.data.sources} health={health.data.sources} />
}
