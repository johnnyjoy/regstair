import Brightness6Outlined from '@mui/icons-material/Brightness6Outlined'
import CheckRounded from '@mui/icons-material/CheckRounded'
import { IconButton, ListItemIcon, Menu, MenuItem, Tooltip } from '@mui/material'
import { useState } from 'react'
import { useThemeMode } from './ThemeModeProvider'
import type { ThemePreference } from './themeMode'

const choices: { value: ThemePreference; label: string }[] = [
  { value: 'system', label: 'System theme' },
  { value: 'light', label: 'Light theme' },
  { value: 'dark', label: 'Dark theme' },
]

export function ThemeModeControl() {
  const { preference, setPreference } = useThemeMode()
  const [anchor, setAnchor] = useState<HTMLElement | null>(null)

  return (
    <>
      <Tooltip title="Color theme">
        <IconButton aria-label={`Color theme: ${preference}`} onClick={(event) => setAnchor(event.currentTarget)}>
          <Brightness6Outlined />
        </IconButton>
      </Tooltip>
      <Menu anchorEl={anchor} open={Boolean(anchor)} onClose={() => setAnchor(null)}>
        {choices.map((choice) => (
          <MenuItem
            key={choice.value}
            role="menuitemradio"
            aria-checked={preference === choice.value}
            selected={preference === choice.value}
            onClick={() => { setPreference(choice.value); setAnchor(null) }}
          >
            <ListItemIcon>{preference === choice.value ? <CheckRounded fontSize="small" /> : null}</ListItemIcon>
            {choice.label}
          </MenuItem>
        ))}
      </Menu>
    </>
  )
}
