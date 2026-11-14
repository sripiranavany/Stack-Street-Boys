import type { Spike, WeatherRow } from '@/api/types'
import { floorHour, fmtDayHour } from './time'

export interface SpikeView extends Spike {
  pct: number
  weather: string
}

/** "Sat 14:00, +85%, dry, 24 °C" building blocks for the spike list. */
export function toSpikeViews(spikes: Spike[], weather: WeatherRow[]): SpikeView[] {
  const byHour = new Map(weather.map((w) => [floorHour(new Date(w.time).getTime()), w]))
  return spikes.map((s) => {
    const w = byHour.get(floorHour(new Date(s.time).getTime()))
    const parts: string[] = []
    if (w) {
      parts.push((w.precipitation_mm ?? 0) >= 0.2 ? `rain ${w.precipitation_mm!.toFixed(1)} mm` : 'dry')
      if (w.temperature_c != null) parts.push(`${Math.round(w.temperature_c)} °C`)
    }
    return {
      ...s,
      pct: s.expected > 0 ? ((s.value - s.expected) / s.expected) * 100 : 0,
      weather: parts.join(', '),
    }
  })
}

export const spikeHeadline = (s: SpikeView) =>
  [fmtDayHour(s.time), `${s.pct >= 0 ? '+' : ''}${Math.round(s.pct)}%`, s.weather].filter(Boolean).join(', ')
