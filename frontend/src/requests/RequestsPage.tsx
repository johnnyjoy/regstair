import FilterAltOutlined from '@mui/icons-material/FilterAltOutlined'
import SearchRounded from '@mui/icons-material/SearchRounded'
import { Alert, Box, Button, Chip, CircularProgress, FormControl, InputLabel, Link as MuiLink, MenuItem, Select, Stack, TextField, Typography } from '@mui/material'
import { useQuery } from '@tanstack/react-query'
import { FormEvent, useRef } from 'react'
import { Link, useSearchParams } from 'react-router-dom'

import { apiRequest } from '../api/client'
import { EmptyState, OperationalPanel, PageHeader } from '../design/OperationalUI'
import { requestQueryFromForm } from './requestFilters'

export type RequestEvent = {
  id: number
  timestamp: string
  operation: 'pull' | 'push'
  logical_reference: string
  status: 'success' | 'denied' | 'error'
  cache_result: 'hit' | 'miss' | 'bypassed' | ''
  client_identity: string
  matched_route: string
  source_or_destination: string
  credential_source?: string
  duration: number
  bytes_transferred: number
  error_classification: string
}

type RequestsResponse = { requests: RequestEvent[]; next_cursor?: string }

function FilterSelect({ name, label, value, children }: { name: string; label: string; value: string; children: React.ReactNode }) {
  return <FormControl size="small" fullWidth><InputLabel>{label}</InputLabel><Select name={name} label={label} defaultValue={value}>{children}</Select></FormControl>
}

export function RequestsPage() {
  const [search, setSearch] = useSearchParams()
  const formRef = useRef<HTMLFormElement>(null)
  const queryString = search.toString()
  const query = useQuery({ queryKey: ['requests', queryString], queryFn: () => apiRequest<RequestsResponse>(`/admin/api/requests${queryString ? `?${queryString}` : ''}`) })

  const apply = (event: FormEvent) => {
    event.preventDefault()
    if (formRef.current) setSearch(requestQueryFromForm(new FormData(formRef.current)))
  }
  const next = new URLSearchParams(search)
  if (query.data?.next_cursor) next.set('cursor', query.data.next_cursor)

  return <Stack spacing={3}>
    <PageHeader title="Requests" subtitle="Investigate how Regstair routed, authenticated, and served registry requests" />

    <OperationalPanel title="Filter requests"><Box component="form" ref={formRef} onSubmit={apply} aria-label="Request filters" sx={{ p: 2.25 }}>
      <Box sx={{ display: 'grid', gap: 1.5, gridTemplateColumns: { xs: '1fr', sm: 'repeat(2, 1fr)', lg: '2fr repeat(4, 1fr)' } }}>
        <TextField defaultValue={search.get('reference') ?? ''} label="Reference" name="reference" placeholder="repository:tag or digest" size="small" />
        <TextField defaultValue={search.get('client_identity') ?? ''} label="Client identity" name="client_identity" size="small" />
        <TextField defaultValue={search.get('route') ?? ''} label="Route" name="route" size="small" />
        <TextField defaultValue={search.get('source') ?? ''} label="Registry" name="source" size="small" />
        <TextField defaultValue={search.get('error_classification') ?? ''} label="Error classification" name="error_classification" size="small" />
        <FilterSelect label="Operation" name="operation" value={search.get('operation') ?? ''}><MenuItem value="">All</MenuItem><MenuItem value="pull">Pull</MenuItem><MenuItem value="push">Push</MenuItem></FilterSelect>
        <FilterSelect label="Status" name="status" value={search.get('status') ?? ''}><MenuItem value="">All</MenuItem><MenuItem value="success">Success</MenuItem><MenuItem value="denied">Denied</MenuItem><MenuItem value="error">Error</MenuItem></FilterSelect>
        <FilterSelect label="Cache" name="cache" value={search.get('cache') ?? ''}><MenuItem value="">All</MenuItem><MenuItem value="hit">Hit</MenuItem><MenuItem value="miss">Miss</MenuItem><MenuItem value="bypassed">Bypassed</MenuItem></FilterSelect>
        <FilterSelect label="Credential" name="credential" value={search.get('credential') ?? ''}><MenuItem value="">Any</MenuItem><MenuItem value="anonymous">Anonymous</MenuItem><MenuItem value="current_user">Current user</MenuItem></FilterSelect>
        <FilterSelect label="Time window" name="window" value={search.get('window') ?? ''}><MenuItem value="">Any time</MenuItem><MenuItem value="1h">Last hour</MenuItem><MenuItem value="24h">Last 24 hours</MenuItem><MenuItem value="7d">Last 7 days</MenuItem></FilterSelect>
        <FilterSelect label="Rows" name="limit" value={search.get('limit') ?? '25'}><MenuItem value="25">25</MenuItem><MenuItem value="50">50</MenuItem><MenuItem value="100">100</MenuItem></FilterSelect>
        <Stack direction="row" spacing={1}><Button startIcon={<FilterAltOutlined />} type="submit" variant="contained">Apply</Button>{queryString && <Button onClick={() => setSearch({})}>Clear</Button>}</Stack>
      </Box>
    </Box></OperationalPanel>

    <OperationalPanel title={`Request results${query.data ? ` · ${query.data.requests.length}` : ''}`}>{query.isLoading ? <Stack role="status" sx={{ alignItems: 'center', py: 8 }}><CircularProgress /></Stack> : query.isError ? <Alert severity="error" sx={{ m: 2 }}>Requests could not be loaded.</Alert> : query.data?.requests.length === 0 ? <EmptyState icon={<SearchRounded color="disabled" sx={{ fontSize: 42 }} />} title="No matching requests" detail="Change or clear the filters to broaden this investigation." /> : (
      <Box component="section" aria-label="Request results">
        <Box sx={{ bgcolor: 'action.hover', display: { xs: 'none', md: 'grid' }, gap: 1.5, gridTemplateColumns: '90px minmax(260px, 2fr) minmax(110px, 1fr) minmax(120px, 1fr) 100px 90px', px: 2.25, py: 1.25 }}><Typography variant="caption">Operation</Typography><Typography variant="caption">Reference</Typography><Typography variant="caption">Route</Typography><Typography variant="caption">Registry</Typography><Typography variant="caption">Cache</Typography><Typography variant="caption">Status</Typography></Box>
        <Stack divider={<Box sx={{ borderTop: 1, borderColor: 'divider' }} />} sx={{ px: 2.25 }}>
          {query.data?.requests.map((request) => <Box key={request.id} sx={{ alignItems: { md: 'center' }, display: 'grid', gap: 1.5, gridTemplateColumns: { xs: 'auto 1fr auto', md: '90px minmax(260px, 2fr) minmax(110px, 1fr) minmax(120px, 1fr) 100px 90px' }, px: 1, py: 1.5 }}>
            <Chip label={request.operation.toUpperCase()} size="small" sx={{ justifySelf: 'start' }} />
            <Box sx={{ minWidth: 0 }}><MuiLink component={Link} to={`/requests/${request.id}`} sx={{ fontFamily: 'monospace', fontWeight: 700, overflowWrap: 'anywhere' }}>{request.logical_reference}</MuiLink><Typography color="text.secondary" variant="caption" sx={{ display: 'block' }}>{new Date(request.timestamp).toLocaleString()}</Typography></Box>
            <Typography variant="body2" sx={{ display: { xs: 'none', md: 'block' } }}>{request.matched_route || '—'}</Typography>
            <Typography variant="body2" sx={{ display: { xs: 'none', md: 'block' } }}>{request.source_or_destination || '—'}</Typography>
            <Chip label={request.cache_result || 'n/a'} size="small" variant="outlined" sx={{ display: { xs: 'none', md: 'inline-flex' }, justifySelf: 'start' }} />
            <Chip color={request.status === 'success' ? 'success' : request.status === 'denied' ? 'warning' : 'error'} label={request.status} size="small" variant="outlined" />
            <Typography color="text.secondary" variant="caption" sx={{ display: { xs: 'block', md: 'none' }, gridColumn: '1 / -1' }}>Route {request.matched_route || '—'} · Registry {request.source_or_destination || '—'} · Cache {request.cache_result || 'n/a'}</Typography>
          </Box>)}
        </Stack>
        {(search.has('cursor') || query.data?.next_cursor) && <Stack direction="row" spacing={1} sx={{ justifyContent: 'flex-end', mt: 2 }}>{search.has('cursor') && <Button component={Link} to={`/requests?${new URLSearchParams([...search].filter(([key]) => key !== 'cursor'))}`}>Newer requests</Button>}{query.data?.next_cursor && <Button component={Link} to={`/requests?${next}`}>Older requests</Button>}</Stack>}
      </Box>
    )}</OperationalPanel>
  </Stack>
}
