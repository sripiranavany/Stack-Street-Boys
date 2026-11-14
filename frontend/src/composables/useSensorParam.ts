import { computed, watch, type Ref } from 'vue'
import { useRouter } from 'vue-router'
import { useSensorsStore } from '@/stores/sensors'

/**
 * Keeps a sensor id in sync with the route param (`/page/:id?`) so views are
 * linkable. Falls back to the first active sensor once sensors have loaded.
 */
export function useSensorParam(routeId: Ref<string | undefined>, routeName: string) {
  const sensors = useSensorsStore()
  const router = useRouter()

  const sensorId = computed<string | undefined>({
    get: () => routeId.value || sensors.active[0]?.sensor_id,
    set: (id) => {
      if (id && id !== routeId.value) router.replace({ name: routeName, params: { id } })
    },
  })
  const sensor = computed(() => (sensorId.value ? sensors.byId.get(sensorId.value) : undefined))

  watch(
    () => sensors.active[0]?.sensor_id,
    (first) => {
      if (!routeId.value && first) sensorId.value = first
    },
    { immediate: true },
  )
  return { sensorId, sensor }
}
