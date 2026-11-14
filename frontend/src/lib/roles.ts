export type RoleId = 'manager' | 'planner' | 'citizen'
export type MapMode = 'now' | 'typical' | 'forecast'

export interface RoleConfig {
  id: RoleId
  label: string
  tagline: string
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
export const HIDDEN_ROLES: RoleId[] = ['planner', 'citizen']

export const ROLE_LIST = Object.values(ROLES).filter((r) => !HIDDEN_ROLES.includes(r.id))
