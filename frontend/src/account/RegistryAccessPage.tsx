import DeleteOutlineRounded from '@mui/icons-material/DeleteOutlineRounded'
import LinkRounded from '@mui/icons-material/LinkRounded'
import { Alert, Box, Button, CircularProgress, Dialog, DialogActions, DialogContent, DialogTitle, Divider, Stack, TextField, Typography } from '@mui/material'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { FormEvent, useMemo, useState } from 'react'

import { apiRequest, ApiError } from '../api/client'
import { EmptyState, OperationalPanel, PageHeader, StatusSummary } from '../design/OperationalUI'

type Credential = { source_id: string; username: string; mtime: string }
type Registry = { id: string; name: string; endpoint: string; pull: boolean; push: boolean }

const verifiedAt = (value: string) => new Date(value).toLocaleString()

export function RegistryAccessView({ credentials, registries, credentialsAvailable, busy, error, onSave, onRemove }: {
  credentials: Credential[]
  registries: Registry[]
  credentialsAvailable: boolean
  busy?: boolean
  error?: string
  onSave: (source: string, username: string, secret: string) => void
  onRemove: (source: string) => void
}) {
  const [selected, setSelected] = useState<Registry | null>(null)
  const [removing, setRemoving] = useState<Registry | null>(null)
  const credentialBySource = useMemo(() => new Map(credentials.map((credential) => [credential.source_id, credential])), [credentials])
  const submit = (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault()
    if (!selected) return
    const data = new FormData(event.currentTarget)
    onSave(selected.id, String(data.get('username')), String(data.get('secret')))
  }

  return <Stack spacing={3}>
    <PageHeader title="Registry access" subtitle="Connect your accounts to registries configured in Regstair" />
    {error && <Alert severity="error">The credential could not be saved: {error}</Alert>}
    {!credentialsAvailable && <StatusSummary tone="warning" title="Credential storage unavailable" detail="An administrator must configure the credential encryption key before upstream credentials can be saved." />}
    <OperationalPanel title="Your registry connections">
      {registries.length === 0 ? <EmptyState title="No configured registries" detail="Add a registry before connecting an account." /> :
        <Stack divider={<Divider />} sx={{ px: 2.25 }}>
          {registries.map((registry) => {
            const credential = credentialBySource.get(registry.id)
            return <Stack key={registry.id} direction={{ xs: 'column', md: 'row' }} spacing={2} sx={{ alignItems: { md: 'center' }, py: 2.25 }}>
              <Box sx={{ flex: 1, minWidth: 0 }}>
                <Typography sx={{ fontWeight: 720 }}>{registry.name}</Typography>
                <Typography color="text.secondary" variant="body2">{registry.endpoint} · {registry.pull && registry.push ? 'Pull and push' : registry.push ? 'Push' : registry.pull ? 'Pull' : 'No active routes'}</Typography>
                <Typography color={credential ? 'success.main' : 'text.secondary'} variant="body2">
                  {credential ? `Connected as ${credential.username} · Verified ${verifiedAt(credential.mtime)}` : 'Not connected · Public pulls may still work without an account'}
                </Typography>
              </Box>
              <Button disabled={!credentialsAvailable || busy} startIcon={<LinkRounded />} variant={credential ? 'outlined' : 'contained'} onClick={() => setSelected(registry)}>{credential ? 'Replace connection' : 'Connect'}</Button>
              {credential && <Button color="error" disabled={busy} startIcon={<DeleteOutlineRounded />} onClick={() => setRemoving(registry)}>Remove</Button>}
            </Stack>
          })}
        </Stack>}
    </OperationalPanel>
    <Dialog open={Boolean(selected)} onClose={() => setSelected(null)} fullWidth maxWidth="sm">
      <Box component="form" onSubmit={submit}>
        <DialogTitle>Connect {selected?.name}</DialogTitle>
        <DialogContent><Stack spacing={2} sx={{ pt: 1 }}><Typography color="text.secondary">Enter credentials issued by {selected?.name}. Regstair verifies them before encrypted storage.</Typography><TextField required autoFocus name="username" label="Registry username" autoComplete="username" /><TextField required name="secret" type="password" label="Password, access token, or robot secret" autoComplete="new-password" /></Stack></DialogContent>
        <DialogActions><Button onClick={() => setSelected(null)}>Cancel</Button><Button disabled={busy} type="submit" variant="contained">Verify and connect</Button></DialogActions>
      </Box>
    </Dialog>
    <Dialog open={Boolean(removing)} onClose={() => setRemoving(null)} fullWidth maxWidth="xs">
      <DialogTitle>Remove {removing?.name} connection?</DialogTitle>
      <DialogContent><Alert severity="warning">Regstair will stop using your credential for this registry. Public pulls can continue where the registry permits them.</Alert></DialogContent>
      <DialogActions><Button onClick={() => setRemoving(null)}>Cancel</Button><Button color="error" variant="contained" onClick={() => { if (removing) onRemove(removing.id); setRemoving(null) }}>Remove connection</Button></DialogActions>
    </Dialog>
  </Stack>
}

export function RegistryAccessPage() {
  const queryClient = useQueryClient()
  const [error, setError] = useState('')
  const registries = useQuery({ queryKey: ['registries'], queryFn: () => apiRequest<{ registries: Registry[] }>('/admin/api/registries') })
  const credentials = useQuery({ queryKey: ['credentials'], queryFn: () => apiRequest<{ credentials: Credential[] }>('/admin/api/account/registry-credentials'), retry: false })
  const mutation = useMutation({
    mutationFn: ({ path, method, body }: { path: string; method: string; body?: unknown }) => apiRequest(path, { method, headers: body ? { 'Content-Type': 'application/json' } : undefined, body: body ? JSON.stringify(body) : undefined }),
    onSuccess: () => { setError(''); queryClient.invalidateQueries({ queryKey: ['credentials'] }) },
    onError: (failure) => setError(failure instanceof ApiError ? failure.classification : 'request_failed'),
  })
  if (registries.isLoading || credentials.isLoading) return <Stack role="status" sx={{ alignItems: 'center', py: 10 }}><CircularProgress /></Stack>
  if (!registries.data) return <Alert severity="error">Configured registries could not be loaded.</Alert>
  return <RegistryAccessView
    registries={registries.data.registries}
    credentials={credentials.data?.credentials ?? []}
    credentialsAvailable={!credentials.isError}
    busy={mutation.isPending}
    error={error}
    onSave={(source, username, secret) => mutation.mutate({ path: `/admin/api/account/registry-credentials/${source}/verify-and-save`, method: 'POST', body: { username, secret } })}
    onRemove={(source) => mutation.mutate({ path: `/admin/api/account/registry-credentials/${source}`, method: 'DELETE', body: { confirm: true } })}
  />
}
