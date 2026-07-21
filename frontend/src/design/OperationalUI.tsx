import CheckCircleOutlineRounded from '@mui/icons-material/CheckCircleOutlineRounded'
import ErrorOutlineRounded from '@mui/icons-material/ErrorOutlineRounded'
import InfoOutlined from '@mui/icons-material/InfoOutlined'
import WarningAmberRounded from '@mui/icons-material/WarningAmberRounded'
import { Box, Paper, Stack, Typography } from '@mui/material'
import type { PropsWithChildren, ReactNode } from 'react'

export function PageHeader({ title, subtitle, actions }: { title: string; subtitle: string; actions?: ReactNode; eyebrow?: string }) {
  return <Stack direction={{ xs: 'column', sm: 'row' }} spacing={2} sx={{ alignItems: { sm: 'flex-start' }, justifyContent: 'space-between' }}>
    <Box><Typography component="h1" variant="h1">{title}</Typography><Typography color="text.secondary" sx={{ mt: .5 }}>{subtitle}</Typography></Box>
    {actions && <Stack direction="row" spacing={1} sx={{ flexWrap: 'wrap' }}>{actions}</Stack>}
  </Stack>
}

export function OperationalPanel({ children, title, action, label }: PropsWithChildren<{ title?: string; action?: ReactNode; label?: string }>) {
  return <Paper component="section" aria-label={label} variant="outlined" sx={{ overflow: 'hidden' }}>
    {(title || action) && <Stack direction="row" sx={{ alignItems: 'center', borderBottom: 1, borderColor: 'divider', minHeight: 52, px: 2.25, justifyContent: 'space-between' }}><Typography component="h2" variant="h2">{title}</Typography>{action}</Stack>}
    {children}
  </Paper>
}

export function MetricStrip({ children }: PropsWithChildren) {
  return <Paper variant="outlined" sx={{ display: 'grid', gridTemplateColumns: { xs: 'repeat(2, minmax(0, 1fr))', md: `repeat(${Array.isArray(children) ? children.length : 1}, minmax(0, 1fr))` }, overflow: 'hidden' }}>{children}</Paper>
}

export function Metric({ label, value, detail, tone = 'neutral' }: { label: string; value: string; detail: string; tone?: 'neutral' | 'good' | 'warning' | 'danger' }) {
  const color = tone === 'good' ? 'success.main' : tone === 'warning' ? 'warning.main' : tone === 'danger' ? 'error.main' : 'text.primary'
  return <Box sx={{ borderColor: 'divider', borderRight: 1, minHeight: 112, p: 2.25, '&:last-child': { borderRight: 0 } }}><Typography color="text.secondary" variant="caption">{label}</Typography><Typography component="p" sx={{ color, fontSize: 28, fontWeight: 760, lineHeight: 1.45 }}>{value}</Typography><Typography color="text.secondary" variant="body2">{detail}</Typography></Box>
}

const statusIcons = { good: CheckCircleOutlineRounded, warning: WarningAmberRounded, danger: ErrorOutlineRounded, neutral: InfoOutlined }
export function StatusSummary({ tone, title, detail, action }: { tone: 'good' | 'warning' | 'danger' | 'neutral'; title: string; detail: string; action?: ReactNode }) {
  const Icon = statusIcons[tone]
  const color = tone === 'good' ? 'success.main' : tone === 'warning' ? 'warning.main' : tone === 'danger' ? 'error.main' : 'primary.main'
  return <Paper role="status" variant="outlined" sx={{ alignItems: 'center', display: 'flex', gap: 1.5, p: 2 }}><Icon sx={{ color }} /><Box sx={{ flex: 1 }}><Typography sx={{ fontWeight: 720 }}>{title}</Typography><Typography color="text.secondary" variant="body2">{detail}</Typography></Box>{action}</Paper>
}

export function EmptyState({ icon, title, detail }: { icon?: ReactNode; title: string; detail: string }) {
  return <Stack sx={{ alignItems: 'center', px: 3, py: 6, textAlign: 'center' }} spacing={1}>{icon}<Typography sx={{ fontSize: 17, fontWeight: 720 }}>{title}</Typography><Typography color="text.secondary" sx={{ maxWidth: 520 }}>{detail}</Typography></Stack>
}
