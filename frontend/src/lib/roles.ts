export type RoleId = 'manager' | 'planner' | 'citizen'
export type MapMode = 'now' | 'typical' | 'forecast'

export type RouteName = 'map' | 'sensor' | 'patterns' | 'compare' | 'forecast' | 'whatif' | 'stores' | 'store'
export type NavLink = { to: string; label: string }

const L = {
  map: { to: '/', label: 'Map' },
  sensor: { to: '/sensors', label: 'Sensor' },
  patterns: { to: '/patterns', label: 'Patterns' },
  compare: { to: '/compare', label: 'Compare' },
  forecast: { to: '/forecast', label: 'Forecast' },
  stores: { to: '/stores', label: 'Stores' },
  whatIf: { to: '/what-if', label: 'What if' },
} satisfies Record<string, NavLink>

export interface RoleConfig {
  id: RoleId
  label: string
  tagline: string
  /** Header links shown for this role (every page stays reachable by URL). */
  nav: NavLink[]
  /** Route names this role may open. Anything else redirects to `home`. UI-only: the API itself is not restricted. */
  access: RouteName[]
  /** Landing page (path) for this role. */
  home: string
  defaultMapMode: MapMode
  /** Wording that changes per role while the data stays the same. */
  words: {
    busy: string
    quiet: string
    rankingTitle: string
    rankingHint: string
    spikeTitle: string
    forecastTitle: string
  }
  /** Which sensors to feature first in the home side panel. */
  ranking: 'busiest' | 'quietest'
}

export const ROLES: Record<RoleId, RoleConfig> = {
  manager: {
    id: 'manager',
    label: 'Store manager',
    tagline: 'Plan staffing and opening hours around footfall.',
    nav: [L.stores, L.forecast, L.whatIf, L.map, L.sensor],
    access: ['stores', 'store', 'forecast', 'whatif', 'map', 'sensor'],
    home: '/stores',
    defaultMapMode: 'now',
    words: {
      busy: 'High footfall',
      quiet: 'Low footfall',
      rankingTitle: 'Where footfall is highest',
      rankingHint: 'Locations to staff up',
      spikeTitle: 'Unusual footfall surges',
      forecastTitle: 'Staffing outlook',
    },
    ranking: 'busiest',
  },
  planner: {
    id: 'planner',
    label: 'Waste planner',
    tagline: 'Schedule collection where litter pressure builds.',
    nav: [L.map, L.patterns, L.forecast, L.compare, L.sensor],
    access: ['map', 'patterns', 'forecast', 'compare', 'sensor'],
    home: '/',
    defaultMapMode: 'typical',
    words: {
      busy: 'High litter pressure',
      quiet: 'Low litter pressure',
      rankingTitle: 'Collection priorities',
      rankingHint: 'Crowded spots fill bins fastest',
      spikeTitle: 'Surges needing extra pickup',
      forecastTitle: 'Collection outlook',
    },
    ranking: 'busiest',
  },
  citizen: {
    id: 'citizen',
    label: 'Citizen',
    tagline: 'Find calm places and good times to visit.',
    nav: [L.map, L.forecast, L.patterns],
    access: ['map', 'forecast', 'patterns', 'sensor'],
    home: '/',
    defaultMapMode: 'now',
    words: {
      busy: 'Crowded',
      quiet: 'Calm',
      rankingTitle: 'Calmest places right now',
      rankingHint: 'Good spots to relax',
      spikeTitle: 'Unusually crowded moments',
      forecastTitle: 'Best time to visit',
    },
    ranking: 'quietest',
  },
}

/** Roles hidden from the UI. Their config stays above so types and copy keep working. */
export const HIDDEN_ROLES: RoleId[] = []

export const ROLE_LIST = Object.values(ROLES).filter((r) => !HIDDEN_ROLES.includes(r.id))

/** Whether a role may open a route (by route name). Unnamed routes are allowed. */
export const canAccess = (role: RoleId, name: unknown): boolean =>
  typeof name !== 'string' || ROLES[role].access.includes(name as RouteName)
