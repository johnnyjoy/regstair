import { Box } from '@mui/material'

type RegstairLogoProps = {
  compact?: boolean
  decorative?: boolean
}

export function RegstairLogo({ compact = false, decorative = false }: RegstairLogoProps) {
  const viewport = compact ? 36 : 88
  const imageSize = compact ? 60 : 146

  return (
    <Box
      sx={{ flex: '0 0 auto', height: viewport, overflow: 'hidden', position: 'relative', width: viewport }}
    >
      <Box
        component="img"
        src="/regstair-logo.png"
        alt={decorative ? '' : 'Regstair logo'}
        aria-hidden={decorative || undefined}
        sx={{
          height: imageSize,
          left: '50%',
          maxWidth: 'none',
          position: 'absolute',
          top: '50%',
          transform: 'translate(-50%, -50%)',
          width: imageSize,
        }}
      />
    </Box>
  )
}
