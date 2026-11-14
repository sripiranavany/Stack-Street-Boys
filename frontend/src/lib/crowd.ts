import type { CrowdLevel, HourWhy, VisitorSource } from '@/api/stores'

/** In-store numbers are blue, outside foot traffic is orange — everywhere on the store pages. */
export const IN_STORE = '#1f4fd1'
export const IN_STORE_FORECAST = '#c9d6f5'
export const AREA = '#b4500a'

export const LEVEL: Record<CrowdLevel, { label: string; pill: string; bar: string }> = {
  closed: { label: 'Closed', pill: 'bg-slate-100 text-slate-600', bar: '#94a3b8' },
  comfortable: { label: 'Comfortable', pill: 'bg-green-50 text-green-800', bar: IN_STORE },
  busy: { label: 'Busy', pill: 'bg-amber-50 text-amber-800', bar: '#c27a00' },
  near_capacity: { label: 'Near capacity', pill: 'bg-orange-800 text-white', bar: '#8a3a00' },
}

export const SOURCE: Record<VisitorSource, string> = {
  live: 'live door counter',
  counted: 'door counter',
  model: 'store forecast',
  'capture-rate': 'estimate from foot traffic',
  '': '',
}

export const hh = (h: number) => `${String(h).padStart(2, '0')}:00`
export const pct = (v: number) => `${Math.round(v * 100)}%`
export const int = (v: number | null | undefined) => (v == null ? '–' : Math.round(v).toLocaleString('en-GB'))

/** "▲ 22% vs usual" / "▼ 8% vs usual" */
export function vsUsual(v: number | null | undefined): string {
  if (v == null) return 'no usual level yet'
  const p = Math.round(v * 100)
  return `${p >= 0 ? '▲' : '▼'} ${Math.abs(p)}% vs usual`
}

/** "+12%" / "−8%" for an effect on foot traffic */
export const effectPct = (v: number) => `${v >= 0 ? '+' : '−'}${Math.abs(Math.round(v * 100))}%`

/** Plain-language reasons for one hour, e.g. ["8°C, 2.1 mm rain, 90% cloud (−12% foot traffic)", "Lichterfest (+40%)"]. */
export function whyLines(w?: HourWhy): string[] {
  if (!w) return []
  const out: string[] = []
  if (w.measured_vs_usual != null) out.push(`Measured foot traffic ${effectPct(w.measured_vs_usual)} vs usual`)
  const tag = w.effects_as_of ? ', as forecast' : ''
  if (w.weather) out.push(w.weather_effect != null ? `${w.weather} (${effectPct(w.weather_effect)} foot traffic${tag})` : w.weather)
  if (w.event) {
    const basis =
      w.event_basis === 'learned' ? `, learned from ${w.event_based_on ?? 'earlier occurrences'}`
      : w.event_basis === 'category' ? `, based on ${w.event_based_on}`
      : w.event_basis === 'size' ? ', size estimate'
      : ''
    out.push(w.event_mult != null ? `${w.event} (${effectPct(w.event_mult - 1)}${basis})` : w.event)
  }
  if (w.holiday_effect != null) out.push(`Holiday effect ${effectPct(w.holiday_effect)}${tag ? ' (as forecast)' : ''}`)
  if (w.school_effect != null) out.push(`School holidays ${effectPct(w.school_effect)}${tag ? ' (as forecast)' : ''}`)
  return out
}

const GENERIC = new Set(['event', 'events', 'fest', 'festival', 'market', 'markt', 'day', 'days', 'night', 'nacht', 'tag',
  'the', 'and', 'of', 'der', 'die', 'das', 'und', 'am', 'im', 'in', 'heilbronn', 'heilbronner', 'expected'])
const tokens = (name: string) =>
  new Set(name.toLowerCase().replace(/\b(19|20)\d{2}\b/g, ' ').split(/[^a-z0-9äöüß]+/).filter(Boolean))

/** Same rule as the forecaster (forecaster/events.py same_series): all words of the shorter name appear in the longer one,
 *  and at least one of them is distinctive. "Lichterfest" matches "Heilbronner Lichterfest 2026". */
export function sameEvent(a: string, b: string): boolean {
  const ta = tokens(a), tb = tokens(b)
  const [small, big] = ta.size <= tb.size ? [ta, tb] : [tb, ta]
  if (!small.size) return false
  let distinctive = false
  for (const t of small) {
    if (!big.has(t)) return false
    if (!GENERIC.has(t)) distinctive = true
  }
  return distinctive
}
