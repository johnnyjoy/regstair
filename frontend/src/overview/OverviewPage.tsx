import ArrowForwardRounded from '@mui/icons-material/ArrowForwardRounded'
import { Alert, Box, Chip, CircularProgress, Link as MuiLink, Stack, Typography } from '@mui/material'
import { useQueries } from '@tanstack/react-query'
import { Link } from 'react-router-dom'

import { apiRequest } from '../api/client'
import { EmptyState, Metric, MetricStrip, OperationalPanel, PageHeader, StatusSummary } from '../design/OperationalUI'

type RequestEvent = {
  id: number
  timestamp: string
  operation: 'pull' | 'push'
  logical_reference: string
  status: 'success' | 'denied' | 'error'
  cache_result: 'hit' | 'miss' | 'bypassed' | ''
  matched_route: string
  source_or_destination: string
  duration: number
}
type Source = { id: string; name: string }
type SourceHealth = { id: string; status: string }
type Blob = { digest: string; size: number }

export type OverviewModel = {
  requests: RequestEvent[]
  sources: Source[]
  sourceHealth: SourceHealth[]
  routeCount: number
  artifactCount: number
  blobs: Blob[]
}

const endpoints = [
  { key: 'requests', path: '/admin/api/requests?limit=12' },
  { key: 'sources', path: '/admin/api/sources' },
  { key: 'health', path: '/admin/api/source-health' },
  { key: 'routes', path: '/admin/api/routes' },
  { key: 'artifacts', path: '/admin/api/artifacts' },
  { key: 'cache', path: '/admin/api/cache' },
] as const

function bytes(value: number) {
  if (value === 0) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  const index = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1)
  return `${Math.round(value / 1024 ** index)} ${units[index]}`
}

function RequestPath() {
  const stages = ['Clients', 'Regstair', 'Routing', 'Cache', 'Registries']
  return (
    <Stack aria-label="Registry request path" direction={{ xs: 'column', sm: 'row' }} spacing={1} sx={{ alignItems: 'center', py: 1 }}>
      {stages.map((stage, index) => (
        <Stack direction={{ xs: 'column', sm: 'row' }} key={stage} spacing={1} sx={{ alignItems: 'center', flex: 1, width: '100%' }}>
          <Box sx={{ bgcolor: stage === 'Regstair' ? 'primary.main' : 'background.default', border: 1, borderColor: stage === 'Regstair' ? 'primary.main' : 'divider', color: stage === 'Regstair' ? 'primary.contrastText' : 'text.primary', fontWeight: 700, px: 1.5, py: 1.25, textAlign: 'center', width: '100%' }}>{stage}</Box>
          {index < stages.length - 1 && <ArrowForwardRounded aria-hidden="true" color="action" sx={{ transform: { xs: 'rotate(90deg)', sm: 'none' } }} />}
        </Stack>
      ))}
    </Stack>
  )
}

export function OverviewView({ model }: { model: OverviewModel }) {
  const hits = model.requests.filter((request) => request.cache_result === 'hit').length
  const cacheable = model.requests.filter((request) => request.cache_result === 'hit' || request.cache_result === 'miss').length
  const errors = model.requests.filter((request) => request.status !== 'success').length
  const unavailable = model.sourceHealth.filter((source) => source.status !== 'healthy' && source.status !== 'disabled').length
  const cacheSize = model.blobs.reduce((total, blob) => total + blob.size, 0)

  return (
    <Stack spacing={3}>
      <PageHeader title="Registry status" subtitle="Routing, delivery, and cache at a glance" />

      {unavailable > 0 ? (
        <StatusSummary tone="warning" title={`${unavailable} ${unavailable === 1 ? 'registry needs' : 'registries need'} attention`} detail="One or more configured upstreams did not pass their latest health check." action={<MuiLink component={Link} to="/sources">Review registries</MuiLink>} />
      ) : model.sources.length > 0 ? (
        <StatusSummary tone="good" title="Registry path operational" detail="All enabled registries passed their latest health check." />
      ) : null}

      <MetricStrip>
        <Metric label="Latest request sample" value={String(model.requests.length)} detail={`${errors} of the latest 12 need attention`} tone={errors ? 'warning' : 'neutral'} />
        <Metric label="Sample cache hit ratio" value={cacheable ? `${Math.round(hits / cacheable * 100)}%` : '—'} detail={`${hits} of ${cacheable} cacheable requests`} />
        <Metric label="Cached content" value={bytes(cacheSize)} detail={`${model.blobs.length} unique blobs`} />
        <Metric label="Artifacts" value={String(model.artifactCount)} detail="Known tag mappings" />
        <Metric label="Active routes" value={String(model.routeCount)} detail={`${model.sources.length} registries`} />
      </MetricStrip>

      <OperationalPanel title="Recent activity" action={<MuiLink component={Link} to="/requests">View all requests</MuiLink>}>
        {model.requests.length === 0 ? (
          <EmptyState title="No registry traffic yet" detail="Configure a client to use this Regstair address; the first pull or push will appear here." />
        ) : (
          <Stack divider={<Box sx={{ borderTop: 1, borderColor: 'divider' }} />} sx={{ px: 2.25 }}>
            {model.requests.slice(0, 8).map((request) => (
              <Box key={request.id} sx={{ alignItems: { sm: 'center' }, display: 'grid', gap: 1, gridTemplateColumns: { xs: '1fr auto', sm: '90px minmax(220px, 1fr) 130px 110px' }, py: 1.5 }}>
                <Chip color={request.operation === 'push' ? 'secondary' : 'default'} label={request.operation.toUpperCase()} size="small" sx={{ justifySelf: 'start' }} />
                <MuiLink component={Link} to={`/requests/${request.id}`} sx={{ fontFamily: 'monospace', overflowWrap: 'anywhere' }}>{request.logical_reference}</MuiLink>
                <Typography color="text.secondary" variant="body2">{request.matched_route || 'No route'}</Typography>
                <Chip color={request.status === 'success' ? 'success' : request.status === 'denied' ? 'warning' : 'error'} label={request.status} size="small" variant="outlined" />
              </Box>
            ))}
          </Stack>
        )}
      </OperationalPanel>

      <OperationalPanel title="Request path"><Box sx={{ px: 2.25, py: 1.5 }}><Typography color="text.secondary" variant="body2">The active delivery path from client authentication through routing and content resolution.</Typography><RequestPath /></Box></OperationalPanel>
    </Stack>
  )
}

export function OverviewPage() {
  const queries = useQueries({ queries: endpoints.map((endpoint) => ({ queryKey: ['overview', endpoint.key], queryFn: () => apiRequest<Record<string, unknown>>(endpoint.path) })) })
  if (queries.some((query) => query.isLoading)) return <Stack role="status" spacing={2} sx={{ alignItems: 'center', py: 10 }}><CircularProgress /><Typography>Loading operational state</Typography></Stack>
  if (queries.some((query) => query.isError)) return <Alert severity="error">Operational data could not be loaded. Regstair is running, but one or more status queries failed.</Alert>

  const [requests, sources, health, routes, artifacts, cache] = queries.map((query) => query.data!)
  return <OverviewView model={{
    requests: requests.requests as RequestEvent[],
    sources: sources.sources as Source[],
    sourceHealth: health.sources as SourceHealth[],
    routeCount: (routes.routes as unknown[]).length,
    artifactCount: (artifacts.artifacts as unknown[]).length,
    blobs: cache.blobs as Blob[],
  }} />
}
