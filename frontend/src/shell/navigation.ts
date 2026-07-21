export type UserRole = 'admin' | 'user'

export type NavigationItem = {
  label: string
  path: string
  icon: 'overview' | 'requests' | 'routes' | 'registries' | 'cache' | 'users' | 'audit' | 'account' | 'credentials'
}

const administratorNavigation: NavigationItem[] = [
  { label: 'Overview', path: '/', icon: 'overview' },
  { label: 'Requests', path: '/requests', icon: 'requests' },
  { label: 'Routes', path: '/routes', icon: 'routes' },
  { label: 'Registries', path: '/sources', icon: 'registries' },
  { label: 'Registry access', path: '/registry-access', icon: 'credentials' },
  { label: 'Cache', path: '/cache', icon: 'cache' },
  { label: 'Users', path: '/admin/users', icon: 'users' },
  { label: 'Audit', path: '/admin/audit', icon: 'audit' },
]

export function navigationFor(role: UserRole): NavigationItem[] {
  return role === 'admin' ? administratorNavigation : administratorNavigation.filter((item) => item.path === '/registry-access')
}
