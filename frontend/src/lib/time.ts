export const TZ = 'Europe/Berlin'
export const HOUR = 3_600_000
export const DAY = 24 * HOUR

const dayHour = new Intl.DateTimeFormat('en-GB', {
  timeZone: TZ, weekday: 'short', hour: '2-digit', minute: '2-digit', hour12: false,
})
const dayMonth = new Intl.DateTimeFormat('en-GB', { timeZone: TZ, day: '2-digit', month: 'short' })
const full = new Intl.DateTimeFormat('en-GB', {
  timeZone: TZ, weekday: 'short', day: '2-digit', month: 'short', hour: '2-digit', minute: '2-digit', hour12: false,
})
const clock = new Intl.DateTimeFormat('en-GB', {
  timeZone: TZ, weekday: 'short', day: '2-digit', month: 'short', hour: '2-digit', minute: '2-digit', second: '2-digit', hour12: false,
})
const hourOnly = new Intl.DateTimeFormat('en-GB', { timeZone: TZ, hour: '2-digit', hour12: false })
const weekdayShort = new Intl.DateTimeFormat('en-US', { timeZone: TZ, weekday: 'short' })

const WD = ['Mon', 'Tue', 'Wed', 'Thu', 'Fri', 'Sat', 'Sun']

/** "Sat 14:00" */
export const fmtDayHour = (iso: string | number | Date) => dayHour.format(new Date(iso)).replace(',', '')
/** "Sat 14 Nov, 14:00" */
export const fmtFull = (iso: string | number | Date) => full.format(new Date(iso)).replace(',', '')
/** "Sat 14 Nov 14:00:05" */
export const fmtClock = (iso: string | number | Date) => clock.format(new Date(iso)).replace(',', '')
export const fmtDate = (iso: string | number | Date) => dayMonth.format(new Date(iso))
/** Hour of day (0-23) in Berlin local time. */
export const hourOfDay = (iso: string | number | Date) => Number(hourOnly.format(new Date(iso))) % 24
/** Weekday index, Mon = 0 … Sun = 6, in Berlin local time. */
export const weekdayIndex = (iso: string | number | Date) => WD.indexOf(weekdayShort.format(new Date(iso)))
export const weekdayName = (i: number) => WD[i] ?? ''

export const toIso = (ms: number) => new Date(ms).toISOString()
export const floorHour = (ms: number) => Math.floor(ms / HOUR) * HOUR

/** Start of a Berlin calendar day ("2026-11-14") as epoch ms. */
export function berlinDayStart(date: string): number {
  const guess = new Date(`${date}T00:00:00Z`).getTime()
  const h = hourOfDay(guess)
  return guess - (h > 12 ? h - 24 : h) * HOUR
}
