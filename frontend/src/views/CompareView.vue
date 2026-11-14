<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { storeToRefs } from 'pinia'
import PageHeader from '@/components/common/PageHeader.vue'
import BaseCard from '@/components/common/BaseCard.vue'
import LoadState from '@/components/common/LoadState.vue'
import SensorSelect from '@/components/common/SensorSelect.vue'
import SegmentedControl from '@/components/common/SegmentedControl.vue'
import TimeSeriesChart from '@/components/charts/TimeSeriesChart.vue'
import { api } from '@/api/endpoints'
import type { Timeseries } from '@/api/types'
import { useAsyncData } from '@/composables/useAsyncData'
import { useSensorsStore } from '@/stores/sensors'
import { DAY, fmtDate, fmtFull, toIso } from '@/lib/time'

const sensors = useSensorsStore()
const { anchor } = storeToRefs(sensors)

const mode = ref<'sensors' | 'periods'>('sensors')
const modes = [
  { value: 'sensors' as const, label: 'Two sensors' },
  { value: 'periods' as const, label: 'Two periods' },
]
const days = ref<'1' | '7' | '14'>('7')
const spanOpts = [
  { value: '1' as const, label: '1 day' },
  { value: '7' as const, label: '7 days' },
  { value: '14' as const, label: '14 days' },
]

const a = ref<string>()
const b = ref<string>()
watch(
  () => sensors.active,
  (list) => {
    a.value ??= list[0]?.sensor_id
    b.value ??= list[1]?.sensor_id ?? list[0]?.sensor_id
  },
  { immediate: true },
)

interface Side {
  title: string
  sensorId: string
  from: number
  to: number
  /** shift applied so both sides can be shown on the same x-range */
  color: string
}

const sides = computed<[Side, Side] | null>(() => {
  if (!a.value || !b.value) return null
  const len = Number(days.value) * DAY
  const end = anchor.value + 1000
  const nameOf = (id: string) => sensors.byId.get(id)?.name ?? id
  if (mode.value === 'sensors') {
    return [
      { title: nameOf(a.value), sensorId: a.value, from: end - len, to: end, color: '#2f6fed' },
      { title: nameOf(b.value), sensorId: b.value, from: end - len, to: end, color: '#f97316' },
    ]
  }
  return [
    { title: `${nameOf(a.value)} · this period`, sensorId: a.value, from: end - len, to: end, color: '#2f6fed' },
    { title: `${nameOf(a.value)} · previous period`, sensorId: a.value, from: end - 2 * len, to: end - len, color: '#8b5cf6' },
  ]
})

const agg = computed(() => (Number(days.value) > 1 ? '1h' : '15m') as '1h' | '15m')

function load(i: 0 | 1) {
  return useAsyncData<Timeseries, { s: Side; agg: '1h' | '15m' }>(
    computed(() => (sides.value ? { s: sides.value[i], agg: agg.value } : null)),
    ({ s, agg }, signal) =>
      api.timeseries(s.sensorId, { from: toIso(s.from), to: toIso(s.to), agg, normalize: true }, signal),
  )
}
const left = load(0)
const right = load(1)

function summarize(t: Timeseries | null) {
  const v = (t?.points ?? []).flatMap((p) => (p.normalized == null ? [] : [{ t: p.time, v: p.normalized }]))
  if (!v.length) return null
  const peak = v.reduce((x, y) => (y.v > x.v ? y : x))
  return { avg: v.reduce((s, x) => s + x.v, 0) / v.length, peak: peak.v, peakAt: peak.t }
}
const stats = computed(() => [summarize(left.data.value), summarize(right.data.value)])

/** Both charts use the same y-range so shapes are visually comparable. */
const yMax = computed(() => {
  const all = [left.data.value, right.data.value].flatMap((t) => t?.points ?? [])
  const m = Math.max(0, ...all.map((p) => p.normalized ?? 0))
  return m ? Math.ceil(m * 10) / 10 : undefined
})
</script>

<template>
  <PageHeader title="Compare" subtitle="Values are scaled to each sensor's own busy level (p95), so sensors of different size compare fairly.">
    <SegmentedControl v-model="mode" :options="modes" label="Compare mode" />
    <SegmentedControl v-model="days" :options="spanOpts" label="Time span" />
  </PageHeader>

  <div class="mb-4 flex flex-wrap gap-4">
    <SensorSelect v-model="a" :label="mode === 'sensors' ? 'Sensor A' : 'Sensor'" />
    <SensorSelect v-if="mode === 'sensors'" v-model="b" label="Sensor B" />
  </div>

  <div v-if="sides" class="grid gap-4 xl:grid-cols-2">
    <BaseCard v-for="(side, i) in sides" :key="i" :title="side.title" :subtitle="`${fmtDate(side.from)} – ${fmtDate(side.to - 1)}`">
      <template v-for="r in [i === 0 ? left : right]" :key="i">
        <LoadState :loading="r.loading.value" :error="r.error.value" :empty="!r.data.value?.points.length">
          <TimeSeriesChart
            :points="r.data.value?.points ?? []"
            normalized
            series-name="vs. p95"
            :color="side.color"
            :y-max="yMax"
            height="300px"
          />
        </LoadState>
      </template>
      <dl v-if="stats[i]" class="mt-3 grid grid-cols-3 gap-3 text-sm">
        <div><dt class="text-slate-500">Average</dt><dd class="font-semibold tabular-nums">{{ (stats[i]!.avg * 100).toFixed(0) }}%</dd></div>
        <div><dt class="text-slate-500">Peak</dt><dd class="font-semibold tabular-nums">{{ (stats[i]!.peak * 100).toFixed(0) }}%</dd></div>
        <div><dt class="text-slate-500">Peak at</dt><dd class="font-semibold">{{ fmtFull(stats[i]!.peakAt) }}</dd></div>
      </dl>
    </BaseCard>
  </div>
</template>
