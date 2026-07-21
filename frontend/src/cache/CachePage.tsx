import ContentCopyRounded from '@mui/icons-material/ContentCopyRounded'
import Inventory2Outlined from '@mui/icons-material/Inventory2Outlined'
import { Alert, Box, Chip, CircularProgress, InputAdornment, Stack, TextField, Typography } from '@mui/material'
import { useQueries } from '@tanstack/react-query'
import { useMemo, useState } from 'react'

import { apiRequest } from '../api/client'
import { EmptyState, Metric, MetricStrip, OperationalPanel, PageHeader } from '../design/OperationalUI'

type Mapping = { logical_repository: string; tag: string; digest: string; media_type: string; size: number; blob_digests: string[]; source: string; route: string; resolved_at: string; last_validated_at: string; fresh_until: string }
type Artifact = { logical_reference: string; mapping: Mapping }
type Blob = { digest: string; size: number }

function formatBytes(value: number) {
  if (!value) return '0 B'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  const index = Math.min(Math.floor(Math.log(value) / Math.log(1024)), units.length - 1)
  const amount = value / 1024 ** index
  return `${amount >= 10 ? Math.round(amount) : amount.toFixed(1)} ${units[index]}`
}

export function CacheView({ artifacts, blobs }: { artifacts: Artifact[]; blobs: Blob[] }) {
  const [search, setSearch] = useState('')
  const physical = blobs.reduce((sum, blob) => sum + blob.size, 0)
  const logical = artifacts.reduce((sum, artifact) => sum + artifact.mapping.size, 0)
  const shared = Math.max(logical - physical, 0)
  const filtered = useMemo(() => { const term = search.trim().toLowerCase(); return term ? artifacts.filter((artifact) => [artifact.logical_reference, artifact.mapping.digest, artifact.mapping.source, artifact.mapping.route].some((value) => value.toLowerCase().includes(term))) : artifacts }, [artifacts, search])

  return <Stack spacing={3}>
    <PageHeader title="Cache" subtitle="Stored OCI content, tag mappings, freshness, and deduplication" />
    <MetricStrip>
      <Metric label="Physical storage" value={formatBytes(physical)} detail={`${blobs.length} unique blobs`} />
      <Metric label="Logical content" value={formatBytes(logical)} detail={`${artifacts.length} tag mappings`} />
      <Metric label="Deduplicated" value={formatBytes(shared)} detail="Shared content not stored twice" />
      <Metric label="Digest reuse" value={blobs.length ? `${Math.max(artifacts.reduce((sum, item) => sum + item.mapping.blob_digests.length, 0) - blobs.length, 0)}` : '0'} detail="Repeated blob references" />
    </MetricStrip>
    <TextField fullWidth value={search} onChange={(event) => setSearch(event.target.value)} label="Search cached artifacts" type="search" placeholder="reference, digest, route, or registry" slotProps={{ input: { startAdornment: <InputAdornment position="start"><Inventory2Outlined /></InputAdornment> } }} />
    <OperationalPanel title="Cached artifacts">{artifacts.length === 0 ? <EmptyState title="The cache is empty" detail="Content appears here after the first successful pull through Regstair." /> : filtered.length === 0 ? <EmptyState title="No cached artifacts match" detail="Change the search to include a reference, digest, route, or registry." /> : <Stack divider={<Box sx={{ borderTop: 1, borderColor: 'divider' }} />} sx={{ px: 2.25 }}>
      {filtered.map((artifact) => { const fresh = new Date(artifact.mapping.fresh_until).getTime() > Date.now(); return <Box key={artifact.logical_reference} sx={{ display: 'grid', gap: 1.5, gridTemplateColumns: { xs: '1fr auto', md: 'minmax(260px, 2fr) 1fr 1fr 120px auto' }, py: 1.75 }}>
        <Box><Typography sx={{ fontFamily: 'monospace', fontWeight: 700, overflowWrap: 'anywhere' }}>{artifact.logical_reference}</Typography><Typography color="text.secondary" variant="caption" sx={{ overflowWrap: 'anywhere' }}>{artifact.mapping.digest}</Typography></Box>
        <Box sx={{ display: { xs: 'none', md: 'block' } }}><Typography color="text.secondary" variant="caption">Source</Typography><Typography>{artifact.mapping.source}</Typography></Box>
        <Box sx={{ display: { xs: 'none', md: 'block' } }}><Typography color="text.secondary" variant="caption">Route</Typography><Typography>{artifact.mapping.route}</Typography></Box>
        <Box sx={{ display: { xs: 'none', md: 'block' } }}><Typography color="text.secondary" variant="caption">Logical size</Typography><Typography>{formatBytes(artifact.mapping.size)}</Typography></Box>
        <Chip color={fresh ? 'success' : 'warning'} icon={fresh ? <ContentCopyRounded /> : undefined} label={fresh ? 'Fresh' : 'Stale'} size="small" variant="outlined" />
      </Box> })}
    </Stack>}</OperationalPanel>
  </Stack>
}

export function CachePage() {
  const [artifacts, cache] = useQueries({ queries: [{ queryKey: ['artifacts'], queryFn: () => apiRequest<{ artifacts: Artifact[] }>('/admin/api/artifacts') }, { queryKey: ['cache'], queryFn: () => apiRequest<{ blobs: Blob[] }>('/admin/api/cache') }] })
  if (artifacts.isLoading || cache.isLoading) return <Stack role="status" sx={{ alignItems: 'center', py: 10 }}><CircularProgress /></Stack>
  if (artifacts.isError || cache.isError || !artifacts.data || !cache.data) return <Alert severity="error">Cache state could not be loaded.</Alert>
  return <CacheView artifacts={artifacts.data.artifacts} blobs={cache.data.blobs} />
}
