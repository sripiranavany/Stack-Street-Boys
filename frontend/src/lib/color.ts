// Quiet → busy scale (colour-blind friendly enough: green → amber → red, also encoded by size).
const STOPS: [number, [number, number, number]][] = [
  [0, [34, 163, 159]],
  [0.5, [250, 204, 21]],
  [0.8, [249, 115, 22]],
  [1, [220, 38, 38]],
]

export function busynessColor(v: number | null | undefined): string {
  if (v == null || Number.isNaN(v)) return '#94a3b8'
  const x = Math.min(Math.max(v, 0), 1)
  for (let i = 1; i < STOPS.length; i++) {
    const [b, cb] = STOPS[i]!
    const [a, ca] = STOPS[i - 1]!
    if (x <= b) {
      const t = (x - a) / (b - a)
      const c = ca.map((c0, k) => Math.round(c0 + (cb[k]! - c0) * t))
      return `rgb(${c[0]},${c[1]},${c[2]})`
    }
  }
  return 'rgb(220,38,38)'
}

export const busynessRadius = (v: number | null | undefined) =>
  7 + 17 * Math.min(Math.max(v ?? 0, 0), 1.2)

export const GRADIENT_CSS = `linear-gradient(90deg, ${[0, 0.25, 0.5, 0.8, 1].map(busynessColor).join(', ')})`

export function busynessLabel(v: number | null | undefined): string {
  if (v == null) return 'No data'
  if (v < 0.25) return 'Very quiet'
  if (v < 0.5) return 'Quiet'
  if (v < 0.75) return 'Moderate'
  if (v < 0.9) return 'Busy'
  return 'Very busy'
}
