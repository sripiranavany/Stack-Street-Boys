<script setup lang="ts">
import { computed, toRef } from 'vue'
import { storeToRefs } from 'pinia'
import PageHeader from '@/components/common/PageHeader.vue'
import BaseCard from '@/components/common/BaseCard.vue'
import LoadState from '@/components/common/LoadState.vue'
import SensorSelect from '@/components/common/SensorSelect.vue'
import ForecastChart from '@/components/charts/ForecastChart.vue'
import { api } from '@/api/endpoints'
import { useAsyncData } from '@/composables/useAsyncData'
import { useSensorParam } from '@/composables/useSensorParam'
import { useSensorsStore } from '@/stores/sensors'
import { useRoleStore } from '@/stores/role'
import { recommend } from '@/lib/recommendation'
import { fmtDayHour, HOUR, toIso } from '@/lib/time'

const props = defineProps<{ id?: string }>()
const { sensorId, sensor } = useSensorParam(toRef(props, 'id'), 'forecast')
const { anchor } = storeToRefs(useSensorsStore())
const { role } = storeToRefs(useRoleStore())

const forecast = useAsyncData(
  computed(() => sensorId.value ?? null),
  (id, s) => api.forecast(id, '72h', s),
)
const history = useAsyncData(
  computed(() => (sensorId.value ? { id: sensorId.value, to: anchor.value + 1000 } : null)),
  (q, s) => api.timeseries(q.id, { from: toIso(q.to - 48 * HOUR), to: toIso(q.to), agg: '1h' }, s),
)

const points = computed(() => forecast.data.value?.points ?? [])
const rec = computed(() => recommend(points.value, role.value.id))
</script>

<template>
  <PageHeader :title="`${role.words.forecastTitle}${sensor ? ' · ' + sensor.name : ''}`" subtitle="The next 72 hours, with the model's confidence band.">
    <SensorSelect v-if="sensorId" v-model="sensorId" />
  </PageHeader>

  <div class="grid gap-4 lg:grid-cols-[1fr_20rem]">
    <BaseCard title="72-hour forecast">
      <LoadState
        :loading="forecast.loading.value"
        :error="forecast.error.value"
        :empty="!points.length"
        :empty-text="forecast.data.value?.note ?? 'No forecast available for this sensor yet.'"
      >
        <ForecastChart :history="history.data.value?.points ?? []" :forecast="points" height="400px" />
      </LoadState>
    </BaseCard>

    <BaseCard title="Recommendation">
      <div v-if="rec" class="space-y-3">
        <div class="rounded-lg bg-brand-50 p-3">
          <div class="text-lg font-semibold text-brand-700">{{ rec.headline }}</div>
          <p class="mt-1 text-sm text-slate-700">{{ rec.body }}</p>
        </div>
        <dl class="grid grid-cols-2 gap-3 text-sm">
          <div>
            <dt class="text-slate-500">Peak</dt>
            <dd class="font-semibold">{{ fmtDayHour(rec.peak.time) }}</dd>
          </div>
          <div>
            <dt class="text-slate-500">Expected peak value</dt>
            <dd class="font-semibold tabular-nums">{{ rec.peak.value.toFixed(1) }}</dd>
          </div>
          <div>
            <dt class="text-slate-500">{{ role.words.busy }}</dt>
            <dd class="font-semibold">{{ fmtDayHour(rec.busyFrom) }} → {{ fmtDayHour(rec.busyTo) }}</dd>
          </div>
          <div>
            <dt class="text-slate-500">{{ role.words.quiet }}</dt>
            <dd class="font-semibold">{{ fmtDayHour(rec.quietFrom) }} → {{ fmtDayHour(rec.quietTo) }}</dd>
          </div>
        </dl>
      </div>
      <p v-else class="text-sm text-slate-500">Recommendations appear once a forecast is available.</p>
    </BaseCard>
  </div>
</template>
