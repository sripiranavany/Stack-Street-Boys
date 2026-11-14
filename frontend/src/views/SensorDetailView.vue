<script setup lang="ts">
import { computed, ref, toRef } from 'vue'
import { storeToRefs } from 'pinia'
import PageHeader from '@/components/common/PageHeader.vue'
import BaseCard from '@/components/common/BaseCard.vue'
import LoadState from '@/components/common/LoadState.vue'
import SensorSelect from '@/components/common/SensorSelect.vue'
import SegmentedControl from '@/components/common/SegmentedControl.vue'
import TimeSeriesChart from '@/components/charts/TimeSeriesChart.vue'
import { api } from '@/api/endpoints'
import { useAsyncData } from '@/composables/useAsyncData'
import { useSensorParam } from '@/composables/useSensorParam'
import { useSensorsStore } from '@/stores/sensors'
import { useRoleStore } from '@/stores/role'
import { toSpikeViews, spikeHeadline } from '@/lib/spikes'
import { DAY, fmtFull, toIso } from '@/lib/time'
import { busynessLabel } from '@/lib/color'
import type { Agg } from '@/api/types'

const props = defineProps<{ id?: string }>()
const { sensorId, sensor } = useSensorParam(toRef(props, 'id'), 'sensor')
const { anchor } = storeToRefs(useSensorsStore())
const { role } = storeToRefs(useRoleStore())

type Span = '3' | '7' | '30' | '90'
const span = ref<Span>('7')
const spans = [
  { value: '3' as Span, label: '3 days' },
  { value: '7' as Span, label: '7 days' },
  { value: '30' as Span, label: '30 days' },
  { value: '90' as Span, label: '90 days' },
]

const query = computed(() => {
  if (!sensorId.value) return null
  const to = anchor.value + 1000
  const days = Number(span.value)
  const agg: Agg = days > 30 ? '1d' : '1h'
  return { id: sensorId.value, from: toIso(to - days * DAY), to: toIso(to), agg }
})

const ts = useAsyncData(query, (q, signal) => api.timeseries(q.id, { from: q.from, to: q.to, agg: q.agg }, signal))
const ctx = useAsyncData(query, (q, signal) => api.context({ from: q.from, to: q.to }, signal))
const spikesReq = useAsyncData(query, (q, signal) =>
  api.spikes({ sensor_id: q.id, from: q.from, to: q.to, z: 2.5, limit: 50 }, signal),
)

const points = computed(() => ts.data.value?.points ?? [])
const stats = computed(() => {
  const vals = points.value.flatMap((p) => (p.value == null ? [] : [{ p, v: p.value }]))
  if (!vals.length) return null
  const peak = vals.reduce((a, b) => (b.v > a.v ? b : a))
  return { avg: vals.reduce((s, x) => s + x.v, 0) / vals.length, peak: peak.v, peakAt: peak.p.time }
})
const spikeViews = computed(() => toSpikeViews(spikesReq.data.value?.spikes ?? [], ctx.data.value?.weather ?? []))
const missing = computed(() => ctx.data.value?.missing_tables ?? [])
</script>

<template>
  <PageHeader :title="sensor?.name ?? 'Sensor detail'" subtitle="Density over time with weather, holidays and events.">
    <SensorSelect v-if="sensorId" v-model="sensorId" />
    <SegmentedControl v-model="span" :options="spans" label="Time span" />
  </PageHeader>

  <div class="grid gap-4 lg:grid-cols-[1fr_22rem]">
    <div class="flex flex-col gap-4">
      <div class="grid grid-cols-2 gap-3 sm:grid-cols-4">
        <div class="rounded-xl border border-slate-200 bg-white p-3">
          <div class="text-xs text-slate-500">Right now</div>
          <div class="text-xl font-semibold">{{ busynessLabel(sensor?.busyness) }}</div>
        </div>
        <div class="rounded-xl border border-slate-200 bg-white p-3">
          <div class="text-xs text-slate-500">Latest value</div>
          <div class="text-xl font-semibold tabular-nums">{{ sensor?.latest_value?.toFixed(1) ?? '—' }}</div>
        </div>
        <div class="rounded-xl border border-slate-200 bg-white p-3">
          <div class="text-xs text-slate-500">Average</div>
          <div class="text-xl font-semibold tabular-nums">{{ stats?.avg.toFixed(1) ?? '—' }}</div>
        </div>
        <div class="rounded-xl border border-slate-200 bg-white p-3">
          <div class="text-xs text-slate-500">Peak</div>
          <div class="text-xl font-semibold tabular-nums">{{ stats?.peak.toFixed(1) ?? '—' }}</div>
          <div v-if="stats" class="text-xs text-slate-500">{{ fmtFull(stats.peakAt) }}</div>
        </div>
      </div>

      <BaseCard title="Density" subtitle="Purple = public holiday, orange = event. Bars show rain, dashed line temperature.">
        <LoadState :loading="ts.loading.value" :error="ts.error.value" :empty="!points.length">
          <TimeSeriesChart
            :points="points"
            :weather="ctx.data.value?.weather"
            :holidays="ctx.data.value?.holidays"
            :events="ctx.data.value?.events"
            height="400px"
          />
        </LoadState>
        <p v-if="missing.length" class="mt-2 text-xs text-amber-700">
          Context not available yet: {{ missing.join(', ') }}.
        </p>
      </BaseCard>
    </div>

    <BaseCard :title="role.words.spikeTitle" subtitle="Hours far above this sensor's usual level">
      <LoadState :loading="spikesReq.loading.value" :error="spikesReq.error.value" :empty="!spikeViews.length" empty-text="No unusual spikes in this period.">
        <ul class="max-h-[520px] divide-y divide-slate-100 overflow-y-auto">
          <li v-for="s in spikeViews" :key="s.time" class="py-2.5">
            <div class="text-sm font-semibold text-slate-900">{{ spikeHeadline(s) }}</div>
            <div v-if="s.likely_reasons.length" class="mt-1 flex flex-wrap gap-1">
              <span v-for="r in s.likely_reasons" :key="r" class="rounded-full bg-slate-100 px-2 py-0.5 text-xs text-slate-600">{{ r }}</span>
            </div>
          </li>
        </ul>
      </LoadState>
    </BaseCard>
  </div>
</template>
