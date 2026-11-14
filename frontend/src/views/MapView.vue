<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { storeToRefs } from 'pinia'
import PageHeader from '@/components/common/PageHeader.vue'
import BaseCard from '@/components/common/BaseCard.vue'
import SegmentedControl from '@/components/common/SegmentedControl.vue'
import SensorMap, { type MapMarker } from '@/components/map/SensorMap.vue'
import TimeSlider from '@/components/map/TimeSlider.vue'
import { useSensorsStore } from '@/stores/sensors'
import { useRoleStore } from '@/stores/role'
import { useMapFrames } from '@/composables/useMapFrames'
import { busynessColor, busynessLabel, GRADIENT_CSS } from '@/lib/color'
import type { MapMode } from '@/lib/roles'
import { weekdayIndex, weekdayName } from '@/lib/time'

const sensors = useSensorsStore()
const roleStore = useRoleStore()
const { active, anchor } = storeToRefs(sensors)
const { role } = storeToRefs(roleStore)

const mode = ref<MapMode>(role.value.defaultMapMode)
watch(role, (r) => (mode.value = r.defaultMapMode))

const weekday = ref(weekdayIndex(anchor.value))
watch(anchor, (a) => (weekday.value = weekdayIndex(a)), { once: true })

const modes: { value: MapMode; label: string }[] = [
  { value: 'now', label: 'Now' },
  { value: 'typical', label: 'Typical' },
  { value: 'forecast', label: 'Forecast' },
]

const { frames, note, loading, error } = useMapFrames(mode, weekday, anchor, active)

const index = ref(0)
watch(
  [frames, mode],
  () => {
    // "Now" starts at the latest frame; the other modes start at the beginning
    index.value = mode.value === 'now' ? Math.max(frames.value.length - 1, 0) : 0
  },
  { flush: 'sync' },
)
const frame = computed(() => frames.value[Math.min(index.value, frames.value.length - 1)])

const selectedId = ref<string>()
const selected = computed(() => (selectedId.value ? sensors.byId.get(selectedId.value) : undefined))

const markers = computed<MapMarker[]>(() =>
  active.value.map((s) => {
    const v = frame.value?.values.get(s.sensor_id)
    const intensity = v?.intensity ?? null
    return {
      id: s.sensor_id,
      name: s.name,
      lat: s.latitude,
      lon: s.longitude,
      intensity,
      tooltip: `${s.name}: ${busynessLabel(intensity)}`,
    }
  }),
)

const ranking = computed(() =>
  markers.value
    .filter((m) => m.intensity != null)
    .sort((a, b) => (role.value.ranking === 'busiest' ? b.intensity! - a.intensity! : a.intensity! - b.intensity!))
    .slice(0, 5),
)

const modeHint = computed(
  () =>
    ({
      now: `The last 24 hours up to the newest reading.`,
      typical: `Average ${weekdayName(weekday.value)} over the last 4 weeks.`,
      forecast: 'Predicted for the next 72 hours.',
    })[mode.value],
)
const weekdays = Array.from({ length: 7 }, (_, i) => ({ value: String(i), label: weekdayName(i) }))
const weekdayModel = computed({ get: () => String(weekday.value), set: (v) => (weekday.value = Number(v)) })
</script>

<template>
  <PageHeader title="City map" :subtitle="`${role.tagline} ${modeHint}`">
    <SegmentedControl v-model="mode" :options="modes" label="Map mode" />
  </PageHeader>

  <div class="grid gap-4 lg:grid-cols-[1fr_20rem]">
    <div class="flex flex-col gap-3">
      <div class="relative h-[55vh] min-h-[420px] overflow-hidden rounded-xl border border-slate-200 shadow-sm">
        <SensorMap :markers="markers" :selected-id="selectedId" @select="selectedId = $event" />
        <div v-if="loading" class="absolute inset-x-0 top-3 z-[500] mx-auto w-fit rounded-full bg-white px-3 py-1 text-sm shadow">Loading…</div>
        <div v-if="error" role="alert" class="absolute inset-x-0 top-3 z-[500] mx-auto w-fit rounded-lg bg-red-50 px-3 py-1 text-sm text-red-700 shadow">
          {{ error.message }}
        </div>
        <div v-else-if="note && !loading" class="absolute inset-x-0 top-3 z-[500] mx-auto w-fit rounded-lg bg-amber-50 px-3 py-1 text-sm text-amber-800 shadow">{{ note }}</div>
        <div class="absolute bottom-3 left-3 z-[500] rounded-lg bg-white/95 p-2 text-xs shadow">
          <div class="mb-1 font-medium text-slate-600">{{ role.words.quiet }} → {{ role.words.busy }}</div>
          <div class="h-2 w-40 rounded-full" :style="{ background: GRADIENT_CSS }" />
        </div>
      </div>

      <BaseCard>
        <div class="flex flex-wrap items-center gap-3">
          <div class="min-w-0 flex-1"><TimeSlider v-model="index" :length="frames.length" :label="frame?.label ?? '—'" /></div>
          <SegmentedControl v-if="mode === 'typical'" v-model="weekdayModel" :options="weekdays" label="Weekday" />
        </div>
      </BaseCard>
    </div>

    <aside class="flex flex-col gap-4">
      <BaseCard :title="role.words.rankingTitle" :subtitle="role.words.rankingHint">
        <ol v-if="ranking.length" class="space-y-1">
          <li v-for="m in ranking" :key="m.id">
            <button
              type="button"
              class="flex w-full items-center gap-3 rounded-lg px-2 py-1.5 text-left hover:bg-slate-50"
              :class="m.id === selectedId && 'bg-brand-50'"
              @click="selectedId = m.id"
            >
              <span class="size-3 shrink-0 rounded-full" :style="{ background: busynessColor(m.intensity) }" />
              <span class="flex-1 truncate text-sm font-medium">{{ m.name }}</span>
              <span class="text-xs tabular-nums text-slate-500">{{ Math.round(m.intensity! * 100) }}%</span>
            </button>
          </li>
        </ol>
        <p v-else class="text-sm text-slate-500">No data for this moment.</p>
      </BaseCard>

      <BaseCard v-if="selected" :title="selected.name" :subtitle="busynessLabel(frame?.values.get(selected.sensor_id)?.intensity)">
        <div class="flex flex-wrap gap-2 text-sm">
          <RouterLink class="rounded-md bg-brand-50 px-3 py-1.5 font-medium text-brand-700 hover:bg-brand-100" :to="{ name: 'sensor', params: { id: selected.sensor_id } }">Details</RouterLink>
          <RouterLink class="rounded-md bg-slate-100 px-3 py-1.5 font-medium hover:bg-slate-200" :to="{ name: 'patterns', params: { id: selected.sensor_id } }">Patterns</RouterLink>
          <RouterLink class="rounded-md bg-slate-100 px-3 py-1.5 font-medium hover:bg-slate-200" :to="{ name: 'forecast', params: { id: selected.sensor_id } }">Forecast</RouterLink>
        </div>
      </BaseCard>
      <p v-else class="px-1 text-sm text-slate-500">Select a circle on the map to dive in.</p>
    </aside>
  </div>
</template>
