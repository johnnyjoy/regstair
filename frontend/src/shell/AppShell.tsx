import AccountCircleOutlined from '@mui/icons-material/AccountCircleOutlined'
import AltRouteOutlined from '@mui/icons-material/AltRouteOutlined'
import ChevronRight from '@mui/icons-material/ChevronRight'
import DashboardOutlined from '@mui/icons-material/DashboardOutlined'
import DnsOutlined from '@mui/icons-material/DnsOutlined'
import HistoryOutlined from '@mui/icons-material/HistoryOutlined'
import Inventory2Outlined from '@mui/icons-material/Inventory2Outlined'
import KeyOutlined from '@mui/icons-material/KeyOutlined'
import MenuRounded from '@mui/icons-material/MenuRounded'
import PeopleAltOutlined from '@mui/icons-material/PeopleAltOutlined'
import ReceiptLongOutlined from '@mui/icons-material/ReceiptLongOutlined'
import {
  AppBar,
  Box,
  ButtonBase,
  Chip,
  Divider,
  Drawer,
  IconButton,
  List,
  ListItemButton,
  ListItemIcon,
  ListItemText,
  Menu,
  MenuItem,
  Toolbar,
  Tooltip,
  Typography,
} from '@mui/material'
import { useState, type PropsWithChildren } from 'react'
import { useLocation } from 'react-router-dom'

import { RegstairLogo } from '../brand/RegstairLogo'
import { ThemeModeControl } from '../theme/ThemeModeControl'
import { navigationFor, type NavigationItem, type UserRole } from './navigation'

const drawerWidth = 248
const icons = {
  overview: DashboardOutlined,
  requests: ReceiptLongOutlined,
  routes: AltRouteOutlined,
  registries: DnsOutlined,
  cache: Inventory2Outlined,
  users: PeopleAltOutlined,
  audit: HistoryOutlined,
  account: AccountCircleOutlined,
  credentials: KeyOutlined,
}

type AppShellProps = PropsWithChildren<{
  role: UserRole
  username: string
  health: 'healthy' | 'degraded' | 'unavailable'
  compact?: boolean
  onSignOut: () => void
}>

function Brand() {
  return (
    <Box
      component="a"
      href="/"
      aria-label="Regstair overview"
      sx={{ alignItems: 'center', color: 'text.primary', display: 'inline-flex', gap: 1.25, textDecoration: 'none' }}
    >
      <RegstairLogo compact decorative />
      <Typography component="span" sx={{ fontSize: 20, fontWeight: 760 }}>Regstair</Typography>
    </Box>
  )
}

function Navigation({ items, onNavigate }: { items: NavigationItem[]; onNavigate?: () => void }) {
  const location = useLocation()
  return (
    <Box component="nav" aria-label="Primary navigation" sx={{ px: 1.5, py: 2 }}>
      <List disablePadding>
        {items.map((item) => {
          const Icon = icons[item.icon]
          const selected = item.path === '/' ? location.pathname === '/' : location.pathname.startsWith(item.path)
          return (
            <ListItemButton
              component="a"
              href={item.path}
              key={item.path}
              selected={selected}
              onClick={onNavigate}
              sx={{ borderRadius: 1, mb: 0.5, minHeight: 42, '&.Mui-selected': { bgcolor: 'action.selected' } }}
            >
              <ListItemIcon sx={{ color: selected ? 'primary.main' : 'text.secondary', minWidth: 38 }}><Icon fontSize="small" /></ListItemIcon>
              <ListItemText primary={item.label} slotProps={{ primary: { sx: { fontWeight: selected ? 700 : 560 } } }} />
              {selected && <ChevronRight aria-hidden="true" sx={{ color: 'primary.main', fontSize: 18 }} />}
            </ListItemButton>
          )
        })}
      </List>
    </Box>
  )
}

export function AppShell({ children, role, username, health, compact = false, onSignOut }: AppShellProps) {
  const [drawerOpen, setDrawerOpen] = useState(false)
  const [accountAnchor, setAccountAnchor] = useState<HTMLElement | null>(null)
  const navigation = navigationFor(role)
  const healthLabel = health === 'healthy' ? 'Healthy' : health === 'degraded' ? 'Degraded' : 'Unavailable'
  const healthColor = health === 'healthy' ? 'success' : health === 'degraded' ? 'warning' : 'error'
  const controls = <>
    <Chip color={healthColor} label={healthLabel} size="small" variant="outlined" />
    <ThemeModeControl />
    <Tooltip title="Account and session">
      <IconButton aria-label={`${username} account menu`} onClick={(event) => setAccountAnchor(event.currentTarget)}>
        <AccountCircleOutlined />
      </IconButton>
    </Tooltip>
  </>
  const drawer = (
    <>
      <Box sx={{ alignItems: 'center', display: 'flex', minHeight: 64, px: 2.5 }}><Brand /></Box>
      <Divider />
      <Navigation items={navigation} onNavigate={compact ? () => setDrawerOpen(false) : undefined} />
      <Box sx={{ borderTop: 1, borderColor: 'divider', mt: 'auto', p: 2 }}>
        {!compact && <Box sx={{ alignItems: 'center', display: 'flex', gap: 1, justifyContent: 'space-between' }}>{controls}</Box>}
        {compact && <Typography color="text.secondary" variant="caption">Registry routing and cache</Typography>}
      </Box>
    </>
  )

  return (
    <Box sx={{ display: 'flex', minHeight: '100dvh' }}>
      <ButtonBase
        component="a"
        href="#main-content"
        sx={{ bgcolor: 'primary.main', color: 'primary.contrastText', left: 12, px: 2, py: 1, position: 'fixed', top: -80, zIndex: 2000, '&:focus': { top: 12 } }}
      >
        Skip to main content
      </ButtonBase>

      {compact && drawerOpen ? (
        <Drawer open={drawerOpen} onClose={() => setDrawerOpen(false)} slotProps={{ paper: { sx: { width: drawerWidth } } }}>{drawer}</Drawer>
      ) : !compact ? (
        <Drawer variant="permanent" slotProps={{ paper: { sx: { bgcolor: 'background.paper', borderRightColor: 'divider', width: drawerWidth } } }} sx={{ width: drawerWidth }}>{drawer}</Drawer>
      ) : null}

      <Box sx={{ display: 'flex', flex: 1, flexDirection: 'column', minWidth: 0 }}>
        {compact && <AppBar color="inherit" elevation={0} position="sticky" sx={{ borderBottom: 1, borderColor: 'divider' }}>
          <Toolbar sx={{ gap: 1.5, minHeight: '64px !important', px: { xs: 1.5, sm: 3 } }}>
            <Tooltip title="Navigation"><IconButton aria-label="Open navigation" onClick={() => setDrawerOpen(true)}><MenuRounded /></IconButton></Tooltip>
            <Brand />
            <Box sx={{ flex: 1 }} />
            {controls}
          </Toolbar>
        </AppBar>}
        <Box component="main" id="main-content" tabIndex={-1} sx={{ flex: 1, minWidth: 0, p: { xs: 2, sm: 3, lg: 4 } }}>
          <Box sx={{ maxWidth: 1600, mx: 'auto', width: '100%' }}>{children}</Box>
        </Box>
      </Box>
      <Menu anchorEl={accountAnchor} open={Boolean(accountAnchor)} onClose={() => setAccountAnchor(null)}>
        <MenuItem component="a" href="/account" onClick={() => setAccountAnchor(null)}>Account</MenuItem>
        <MenuItem component="a" href="/regstair-ca.crt" download onClick={() => setAccountAnchor(null)}>Download Regstair CA</MenuItem>
        <MenuItem onClick={() => { setAccountAnchor(null); onSignOut() }}>Sign out</MenuItem>
      </Menu>
    </Box>
  )
}
