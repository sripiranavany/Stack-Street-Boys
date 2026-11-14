<script setup lang="ts">
import { computed, ref, toRef } from 'vue'
import PageHeader from '@/components/common/PageHeader.vue'
import BaseCard from '@/components/common/BaseCard.vue'
import LoadState from '@/components/common/LoadState.vue'
import SensorSelect from '@/components/common/SensorSelect.vue'
import SegmentedControl from '@/components/common/SegmentedControl.vue'
import PatternHeatmap from '@/components/charts/PatternHeatmap.vue'
import { api } from '@/api/endpoints'
import { useAsyncData } from '@/composables/useAsyncData'
import { useSensorParam } from '@/composables/useSensorParam'
import { useSensorsStore } from '@/stores/sensors'
import { DAY, toIso } from '@/lib/time'
import { storeToRefs } from 'pinia'

const props = defineProps<{ id?: string }>()
const { sensorId, sensor } = useSensorParam(toRef(props, 'id'), 'patterns')
const { anchor } = storeToRefs(useSensorsStore())

const span = ref<'30' | '60' | '90'>('90')
const spans = [
  { value: '30' as const, label: '30 days' },
  { value: '60' as const, label: '60 days' },
  { value: '90' as const, label: '90 days' },
]

const query = computed(() =>
  sensorId.value
    ? { id: sensorId.value, from: toIso(anchor.value - Number(span.value) * DAY), to: toIso(anchor.value + 1000) }
    : null,
)
const { data, loading, error } = useAsyncData(query, (q, s) => api.patterns(q.id, { from: q.from, to: q.to }, s))

const hasData = computed(() => data.value?.matrix.some((r) => r.some((v) => v != null)) ?? false)

/** One-line takeaway so the grid is readable at a glance. */
const insight = computed(() => {
  const m = data.value
  if (!m || !hasData.value) return ''
  let best = { d: 0, h: 0, v: -Infinity }
  m.matrix.forEach((row, d) => row.forEach((v, h) => v != null && v > best.v && (best = { d, h, v })))
  return `Busiest slot: ${m.days[best.d]} ${String(best.h).padStart(2, '0')}:00–${String((best.h + 1) % 24).padStart(2, '0')}:00 (avg ${best.v.toFixed(1)}).`
})
</script>

<template>
  <PageHeader :title="`Weekly pattern${sensor ? ' · ' + sensor.name : ''}`" subtitle="Average density by weekday and hour (local time).">
    <SensorSelect v-if="sensorId" v-model="sensorId" />
    <SegmentedControl v-model="span" :options="spans" label="Time span" />
  </PageHeader>

  <BaseCard :subtitle="insight">
    <LoadState :loading="loading" :error="error" :empty="!hasData" empty-text="Not enough data for this sensor yet.">
      <PatternHeatmap v-if="data" :patterns="data" height="440px" />
    </LoadState>
  </BaseCard>
</template>
