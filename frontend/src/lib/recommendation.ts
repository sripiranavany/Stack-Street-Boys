import type { ForecastPoint } from '@/api/types'
import type { RoleId } from './roles'
import { fmtDayHour } from './time'

export interface Recommendation {
  peak: ForecastPoint
  busyFrom: string
  busyTo: string
  quietFrom: string
  quietTo: string
  headline: string
  body: string
}

const hh = (iso: string) => fmtDayHour(iso)

/** Turns a forecast into a role-specific recommendation. */
export function recommend(points: ForecastPoint[], role: RoleId): Recommendation | null {
  if (points.length < 3) return null

  let peakIdx = 0
  points.forEach((p, i) => {
    if (p.value > points[peakIdx]!.value) peakIdx = i
  })
  const peak = points[peakIdx]!
  const threshold = peak.value * 0.8

  let a = peakIdx
  let b = peakIdx
  while (a > 0 && points[a - 1]!.value >= threshold) a--
  while (b < points.length - 1 && points[b + 1]!.value >= threshold) b++

  // quietest 3-hour stretch
  let qi = 0
  let qBest = Infinity
  for (let i = 0; i + 2 < points.length; i++) {
    const s = points[i]!.value + points[i + 1]!.value + points[i + 2]!.value
    if (s < qBest) {
      qBest = s
      qi = i
    }
  }

  const busyFrom = points[a]!.time
  const busyTo = points[b]!.time
  const quietFrom = points[qi]!.time
  const quietTo = points[qi + 2]!.time
  const busyRange = `${hh(busyFrom)}–${hh(busyTo).split(' ')[1]}`
  const quietRange = `${hh(quietFrom)}–${hh(quietTo).split(' ')[1]}`

  const text: Record<RoleId, [string, string]> = {
    manager: [
      `Staff up around ${hh(peak.time)}`,
      `Footfall is expected to stay high ${busyRange}. The quietest stretch is ${quietRange} – a good window for restocking and breaks.`,
    ],
    planner: [
      `Empty bins before ${hh(busyFrom)}`,
      `Crowds peak around ${hh(peak.time)} (high until ${hh(busyTo).split(' ')[1]}). Schedule extra pickup right after the peak; ${quietRange} is the calmest slot for routine rounds.`,
    ],
    citizen: [
      `Best time to visit: ${quietRange}`,
      `It will be most crowded ${busyRange}, peaking at ${hh(peak.time)}. Come in the calm window if you prefer space.`,
    ],
  }
  const [headline, body] = text[role]
  return { peak, busyFrom, busyTo, quietFrom, quietTo, headline, body }
}
