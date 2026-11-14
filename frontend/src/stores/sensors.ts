import { computed, ref } from 'vue'
import { defineStore } from 'pinia'
import { api } from '@/api/endpoints'
import type { Sensor } from '@/api/types'

export const useSensorsStore = defineStore('sensors', () => {
  const list = ref<Sensor[]>([])
  const loading = ref(false)
  const error = ref<Error | null>(null)
  let inflight: Promise<void> | null = null

  const byId = computed(() => new Map(list.value.map((s) => [s.sensor_id, s])))

  /**
   * The data is historical, so "now" is the newest reading we have,
   * not the wall clock.
   */
  const anchor = computed<number>(() => {
    let max = 0
    for (const s of list.value) {
      if (s.latest_time) max = Math.max(max, new Date(s.latest_time).getTime())
    }
    return max || Date.now()
  })

  /** Sensors that reported within two weeks of the newest reading; stale ones are hidden. */
  const active = computed(() =>
    list.value.filter((s) => s.latest_time && new Date(s.latest_time).getTime() > anchor.value - 14 * 24 * 3_600_000),
  )

  function fetch(force = false) {
    if (inflight) return inflight
    if (list.value.length && !force) return Promise.resolve()
    loading.value = true
    error.value = null
    inflight = api
      .sensors()
      .then((r) => {
        list.value = r
      })
      .catch((e: Error) => {
        error.value = e
      })
      .finally(() => {
        loading.value = false
        inflight = null
      })
    return inflight
  }

  return { list, active, byId, anchor, loading, error, fetch }
})
