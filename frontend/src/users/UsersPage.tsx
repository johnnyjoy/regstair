import AddRounded from '@mui/icons-material/AddRounded'
import KeyRounded from '@mui/icons-material/KeyRounded'
import SaveRounded from '@mui/icons-material/SaveRounded'
import { Alert, Box, Button, Checkbox, CircularProgress, Dialog, DialogActions, DialogContent, DialogTitle, FormControl, FormControlLabel, InputLabel, MenuItem, Select, Stack, TextField, Typography } from '@mui/material'
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query'
import { FormEvent, useState } from 'react'
import { apiRequest, ApiError } from '../api/client'
import { EmptyState, OperationalPanel, PageHeader, StatusSummary } from '../design/OperationalUI'

type User = { id: string; username: string; display_name: string; email: string; access: 'admin' | 'user'; enabled: boolean; ctime: string; mtime: string }
type CreateUser = { username: string; password: string; display_name: string; email: string; access: 'admin' | 'user'; enabled: boolean }

export function UsersView({ users, onCreate, onSave, onReset, error }: { users: User[]; onCreate: (user: CreateUser) => void; onSave: (user: User) => void; onReset: (id: string, password: string) => void; error?: string }) {
  const [createOpen, setCreateOpen] = useState(false)
  const [resetUser, setResetUser] = useState<User | null>(null)
  const [drafts, setDrafts] = useState<Record<string, User>>({})
  const draft = (user: User) => drafts[user.id] ?? user
  const update = (user: User, values: Partial<User>) => setDrafts((current) => ({ ...current, [user.id]: { ...draft(user), ...values } }))
  const create = (event: FormEvent<HTMLFormElement>) => { event.preventDefault(); const data = new FormData(event.currentTarget); onCreate({ username: String(data.get('username')), password: String(data.get('password')), display_name: String(data.get('display_name')), email: String(data.get('email')), access: String(data.get('access')) as 'admin' | 'user', enabled: true }); setCreateOpen(false) }
  const reset = (event: FormEvent<HTMLFormElement>) => { event.preventDefault(); if (!resetUser) return; onReset(resetUser.id, String(new FormData(event.currentTarget).get('password'))); setResetUser(null) }

  return <Stack spacing={3}>
    <PageHeader title="Users" subtitle="Local identities authorized to use and administer Regstair" actions={<Button startIcon={<AddRounded />} variant="contained" onClick={() => setCreateOpen(true)}>Add user</Button>} />
    <StatusSummary tone="neutral" title="Immediate access enforcement" detail="Role, access, and password changes end that user's active sessions immediately. Regstair prevents changes that would leave no enabled administrator." />
    {error && <Alert severity="error">{error}</Alert>}
    <OperationalPanel title={`Local users · ${users.length}`}>{users.length === 0 ? <EmptyState title="No local users" detail="Create a local identity to grant access to Regstair." /> : <Stack divider={<Box sx={{ borderTop: 1, borderColor: 'divider' }} />} sx={{ px: 2.25 }}>
      {users.map((user) => { const value = draft(user); const changed = value.access !== user.access || value.enabled !== user.enabled; return <Box key={user.id} sx={{ alignItems: { md: 'center' }, display: 'grid', gap: 2, gridTemplateColumns: { xs: '1fr auto', md: 'minmax(220px, 1.5fr) 170px 130px 220px' }, py: 2 }}>
        <Box><Typography sx={{ fontWeight: 700 }}>{user.display_name || user.username}</Typography><Typography color="text.secondary" variant="body2">{user.username}{user.email ? ` · ${user.email}` : ''}</Typography></Box>
        <FormControl size="small"><InputLabel>Access for {user.username}</InputLabel><Select aria-label={`Access for ${user.username}`} label={`Access for ${user.username}`} value={value.access} onChange={(event) => update(user, { access: event.target.value as 'admin' | 'user' })}><MenuItem value="user">User</MenuItem><MenuItem value="admin">Administrator</MenuItem></Select></FormControl>
        <FormControlLabel control={<Checkbox checked={value.enabled} onChange={(event) => update(user, { enabled: event.target.checked })} />} label={`Enabled for ${user.username}`} />
        <Stack direction="row" spacing={1} sx={{ gridColumn: { xs: '1 / -1', md: 'auto' } }}><Button aria-label={`Save changes for ${user.username}`} disabled={!changed} startIcon={<SaveRounded />} onClick={() => onSave(value)}>Save</Button><Button aria-label={`Reset password for ${user.username}`} startIcon={<KeyRounded />} onClick={() => setResetUser(user)}>Reset password</Button></Stack>
      </Box> })}
    </Stack>}</OperationalPanel>
    <Dialog open={createOpen} onClose={() => setCreateOpen(false)} fullWidth maxWidth="sm"><Box component="form" onSubmit={create}><DialogTitle>Add user</DialogTitle><DialogContent><Stack spacing={2} sx={{ pt: 1 }}><TextField required label="Username" name="username" /><TextField label="Display name" name="display_name" /><TextField label="Email" name="email" type="email" /><TextField required label="Temporary password" name="password" type="password" helperText="15 to 128 characters" slotProps={{ htmlInput: { minLength: 15, maxLength: 128 } }} /><FormControl><InputLabel>Access</InputLabel><Select label="Access" name="access" defaultValue="user"><MenuItem value="user">User</MenuItem><MenuItem value="admin">Administrator</MenuItem></Select></FormControl></Stack></DialogContent><DialogActions><Button onClick={() => setCreateOpen(false)}>Cancel</Button><Button type="submit" variant="contained">Create user</Button></DialogActions></Box></Dialog>
    <Dialog open={Boolean(resetUser)} onClose={() => setResetUser(null)} fullWidth maxWidth="sm"><Box component="form" onSubmit={reset}><DialogTitle>Reset password for {resetUser?.username}</DialogTitle><DialogContent><Alert severity="warning" sx={{ mb: 2 }}>This immediately ends all active sessions for this user.</Alert><TextField required fullWidth label="New password" name="password" type="password" slotProps={{ htmlInput: { minLength: 15, maxLength: 128 } }} /></DialogContent><DialogActions><Button onClick={() => setResetUser(null)}>Cancel</Button><Button color="warning" type="submit" variant="contained">Reset password</Button></DialogActions></Box></Dialog>
  </Stack>
}

export function UsersPage() {
  const client = useQueryClient()
  const users = useQuery({ queryKey: ['users'], queryFn: () => apiRequest<{ users: User[] }>('/admin/api/users') })
  const [error, setError] = useState('')
  const mutation = useMutation({ mutationFn: ({ path, method, body }: { path: string; method: string; body: unknown }) => apiRequest(path, { method, headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) }), onSuccess: () => { setError(''); client.invalidateQueries({ queryKey: ['users'] }) }, onError: (failure) => setError(failure instanceof ApiError ? failure.classification : 'The user change could not be completed.') })
  if (users.isLoading) return <Stack role="status" sx={{ alignItems: 'center', py: 10 }}><CircularProgress /></Stack>
  if (users.isError || !users.data) return <Alert severity="error">Users could not be loaded.</Alert>
  return <UsersView users={users.data.users} error={error} onCreate={(body) => mutation.mutate({ path: '/admin/api/users', method: 'POST', body })} onSave={(user) => mutation.mutate({ path: `/admin/api/users/${user.id}`, method: 'PATCH', body: { access: user.access, enabled: user.enabled, mtime: user.mtime } })} onReset={(id, password) => mutation.mutate({ path: `/admin/api/users/${id}/password`, method: 'POST', body: { password } })} />
}
