import { computed, type Ref } from 'vue'
import { api } from '@/api/endpoints'
import type { Sensor } from '@/api/types'
import type { MapMode } from '@/lib/roles'
import { floorHour, fmtDayHour, HOUR, hourOfDay, toIso, weekdayIndex } from '@/lib/time'
import { useAsyncData } from './useAsyncData'

export interface FrameValue {
  /** 0..1 relative to the sensor's own usual maximum */
  intensity: number
  /** raw density value */
  value: number
}

export interface Frame {
  key: number
  label: string
  values: Map<string, FrameValue>
}

interface Result {
  frames: Frame[]
  note?: string
}

const p95Of = (s: Sensor | undefined) =>
  s?.latest_value != null && s.busyness ? s.latest_value / s.busyness : null

/** Builds the animation frames for the three map modes. */
export function useMapFrames(
  mode: Ref<MapMode>,
  weekday: Ref<number>,
  anchor: Ref<number>,
  sensors: Ref<Sensor[]>,
) {
  const ready = computed(() => sensors.value.length > 0)
  const key = computed(() =>
    ready.value ? { mode: mode.value, weekday: weekday.value, anchor: anchor.value } : null,
  )

  const { data, loading, error } = useAsyncData<Result, NonNullable<typeof key.value>>(
    key,
    async ({ mode, weekday, anchor }, signal) => {
      const end = floorHour(anchor) + HOUR

      if (mode === 'now') {
        const res = await api.heatmap({ from: toIso(end - 24 * HOUR), to: toIso(end), agg: 'hourly' }, signal)
        return {
          frames: res.frames.map((f) => ({
            key: new Date(f.time).getTime(),
            label: fmtDayHour(f.time),
            values: new Map(f.points.map((p) => [p.sensor_id, { intensity: p.intensity, value: p.value }])),
          })),
        }
      }

      if (mode === 'typical') {
        const res = await api.heatmap({ from: toIso(end - 28 * 24 * HOUR), to: toIso(end), agg: 'hourly' }, signal)
        const acc = new Map<number, Map<string, { i: number; v: number; n: number }>>()
        for (const f of res.frames) {
          if (weekdayIndex(f.time) !== weekday) continue
          const h = hourOfDay(f.time)
          const slot = acc.get(h) ?? new Map()
          acc.set(h, slot)
          for (const p of f.points) {
            const c = slot.get(p.sensor_id) ?? { i: 0, v: 0, n: 0 }
            c.i += p.intensity
            c.v += p.value
            c.n++
            slot.set(p.sensor_id, c)
          }
        }
        const frames: Frame[] = [...acc.entries()]
          .sort((a, b) => a[0] - b[0])
          .map(([h, slot]) => ({
            key: h,
            label: `${String(h).padStart(2, '0')}:00`,
            values: new Map([...slot].map(([id, c]) => [id, { intensity: c.i / c.n, value: c.v / c.n }])),
          }))
        return { frames }
      }

      // forecast: one request per sensor, tolerate individual failures
      const list = sensors.value
      const results = await Promise.all(list.map((s) => api.forecast(s.sensor_id, '72h', signal).catch(() => null)))
      const byHour = new Map<number, Map<string, FrameValue>>()
      results.forEach((f, i) => {
        if (!f?.points.length) return
        const s = list[i]!
        const scale = p95Of(s) ?? Math.max(...f.points.map((p) => p.value), 1)
        for (const p of f.points) {
          const t = floorHour(new Date(p.time).getTime())
          const slot = byHour.get(t) ?? new Map()
          byHour.set(t, slot)
          slot.set(s.sensor_id, { intensity: Math.min(p.value / scale, 1), value: p.value })
        }
      })
      const frames = [...byHour.entries()]
        .sort((a, b) => a[0] - b[0])
        .map(([t, values]) => ({ key: t, label: fmtDayHour(t), values }))
      return {
        frames,
        note: frames.length ? undefined : 'No forecast has been generated yet. Try Now or Typical.',
      }
    },
  )

  return { frames: computed(() => data.value?.frames ?? []), note: computed(() => data.value?.note), loading, error }
}
