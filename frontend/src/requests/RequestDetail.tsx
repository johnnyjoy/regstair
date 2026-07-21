import ArrowBackRounded from '@mui/icons-material/ArrowBackRounded'
import { Alert, Box, Chip, CircularProgress, Divider, Link as MuiLink, Stack, Typography } from '@mui/material'
import { useQuery } from '@tanstack/react-query'
import { Link, useParams } from 'react-router-dom'

import { apiRequest } from '../api/client'
import { OperationalPanel, PageHeader, StatusSummary } from '../design/OperationalUI'
import type { RequestEvent } from './RequestsPage'

type RequestDetail = {
  request: Omit<RequestEvent, 'duration'> & { credential: string; duration_ms: number; explanation: string[] }
  provenance?: { source?: string; route?: string; resolved_digest?: string; physical_source_reference?: string }
}

function Fact({ label, value }: { label: string; value: string }) {
  return <Box><Typography color="text.secondary" variant="caption">{label}</Typography><Typography sx={{ overflowWrap: 'anywhere' }}>{value || 'Not recorded'}</Typography></Box>
}

export function RequestDetailView({ detail }: { detail: RequestDetail }) {
  const request = detail.request
  return <Stack spacing={3}>
    <MuiLink component={Link} to="/requests" sx={{ alignItems: 'center', display: 'inline-flex', gap: 0.5, width: 'fit-content' }}><ArrowBackRounded fontSize="small" />Requests</MuiLink>
    <PageHeader title={request.logical_reference} subtitle={`${request.operation.toUpperCase()} on ${new Date(request.timestamp).toLocaleString()}`} actions={<Chip color={request.status === 'success' ? 'success' : request.status === 'denied' ? 'warning' : 'error'} label={request.status} size="small" />} />

    {request.error_classification && <StatusSummary tone="danger" title={request.error_classification} detail="The request did not complete. Review the summary and technical evidence below." />}

    <OperationalPanel title="Investigation summary"><Box sx={{ display: 'grid', gap: 2.5, gridTemplateColumns: { xs: '1fr 1fr', md: 'repeat(4, 1fr)' }, p: 2.25 }}>
        <Fact label="Client identity" value={request.client_identity} />
        <Fact label="Matched route" value={request.matched_route} />
        <Fact label="Registry" value={request.source_or_destination} />
        <Fact label="Credential source" value={request.credential} />
        <Fact label="Cache result" value={request.cache_result} />
        <Fact label="Duration" value={`${request.duration_ms.toLocaleString()} ms`} />
        <Fact label="Transferred" value={`${request.bytes_transferred.toLocaleString()} bytes`} />
        <Fact label="Request ID" value={String(request.id)} />
      </Box></OperationalPanel>

    <OperationalPanel><Box component="details"><Typography component="summary" sx={{ cursor: 'pointer', fontWeight: 720, px: 2.25, py: 2 }}>Technical evidence</Typography>
      <Stack divider={<Divider />} spacing={2} sx={{ borderTop: 1, borderColor: 'divider', p: 2.25 }}>
        <Box><Typography component="h3" sx={{ fontWeight: 700, mb: 1 }}>Decision steps</Typography>{request.explanation?.map((step, index) => <Typography key={`${index}-${step}`} sx={{ py: 0.5 }}>{index + 1}. {step}</Typography>)}</Box>
        {detail.provenance && <Box sx={{ display: 'grid', gap: 2, gridTemplateColumns: { xs: '1fr', sm: 'repeat(2, 1fr)' } }}><Fact label="Resolved digest" value={detail.provenance.resolved_digest ?? ''} /><Fact label="Physical reference" value={detail.provenance.physical_source_reference ?? ''} /><Fact label="Provenance source" value={detail.provenance.source ?? ''} /><Fact label="Provenance route" value={detail.provenance.route ?? ''} /></Box>}
      </Stack>
    </Box></OperationalPanel>
  </Stack>
}

export function RequestDetailPage() {
  const { id } = useParams()
  const query = useQuery({ queryKey: ['request', id], queryFn: () => apiRequest<RequestDetail>(`/admin/api/requests/${encodeURIComponent(id ?? '')}`), retry: false })
  if (query.isLoading) return <Stack role="status" sx={{ alignItems: 'center', py: 10 }}><CircularProgress /></Stack>
  if (query.isError || !query.data) return <Alert severity="error">This request could not be loaded.</Alert>
  return <RequestDetailView detail={query.data} />
}
