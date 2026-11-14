<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import BaseCard from '@/components/common/BaseCard.vue'
import LoadState from '@/components/common/LoadState.vue'
import SegmentedControl from '@/components/common/SegmentedControl.vue'
import StoreDayChart from '@/components/charts/StoreDayChart.vue'
import StoreEventsCard from '@/components/stores/StoreEventsCard.vue'
import StoreBakePlanCard from '@/components/stores/StoreBakePlanCard.vue'
import StoreCalmHoursCard from '@/components/stores/StoreCalmHoursCard.vue'
import BaseChart from '@/components/charts/BaseChart.vue'
import type { EChartsOption } from '@/composables/useEChart'
import { storesApi, type StoreInfo } from '@/api/stores'
import { useAsyncData } from '@/composables/useAsyncData'
import { AREA, IN_STORE, LEVEL, SOURCE, effectPct, hh, int, opensText, pct, vsUsual, whyLines } from '@/lib/crowd'
import { fmtDate, fmtFull, fmtDayHour, hourOfDay } from '@/lib/time'

const props = defineProps<{ id?: string }>()
const router = useRouter()

const stores = ref<StoreInfo[]>([])
const storeId = computed(() => props.id ?? stores.value[0]?.id ?? null)
const day = ref<'today' | 'tomorrow'>('today')

onMounted(async () => {
  const o = await storesApi.overview().catch(() => null)
  stores.value = o?.stores.map((s) => s.store) ?? []
})
watch(stores, (list) => {
  if (!props.id && list[0]) router.replace({ name: 'store', params: { id: list[0].id } })
})

const dash = useAsyncData(
  computed(() => (storeId.value ? { id: storeId.value, day: day.value } : null)),
  (q, s) => storesApi.dashboard(q.id, q.day, s),
)
// The replay clock moves on its own: refresh every minute.
const timer = setInterval(() => dash.reload(), 60_000)
// After an event is added or removed the worker re-forecasts: refresh now and again once the new run is likely in.
const followUps: number[] = []
function onEventsChanged() {
  dash.reload()
  followUps.push(window.setTimeout(() => dash.reload(), 20_000), window.setTimeout(() => dash.reload(), 45_000))
}
onBeforeUnmount(() => {
  clearInterval(timer)
  followUps.forEach(clearTimeout)
})

const d = computed(() => dash.data.value)
const nowHour = computed(() => (d.value?.is_today ? hourOfDay(d.value.sim_now) : null))
const nowLevel = computed(() => LEVEL[d.value?.now.level ?? 'closed'])
const peakLevel = computed(() => (d.value?.peak ? LEVEL[d.value.peak.level] : null))

function changeStore(e: Event) {
  router.push({ name: 'store', params: { id: (e.target as HTMLSelectElement).value } })
}

// Chips above the chart: holidays, events and a weather summary for the open hours of the shown day.
const dayChips = computed(() => {
  const v = d.value
  if (!v) return []
  const chips: { text: string; tone: 'warn' | 'info' | 'weather' }[] = []
  if (v.context.public_holiday) chips.push({ text: `Public holiday: ${v.context.public_holiday} — closed`, tone: 'warn' })
  if (v.context.school_holiday) chips.push({ text: `School holidays: ${v.context.school_holiday}`, tone: 'info' })
  for (const e of v.context.events) {
    const sameDay = fmtDate(e.start) === fmtDate(e.end)
    const when = sameDay ? `${hh(hourOfDay(e.start))}–${hh(hourOfDay(e.end))}` : `${fmtDayHour(e.start)} – ${fmtDayHour(e.end)}`
    chips.push({ text: `${e.name} · ${when}`, tone: 'warn' })
  }
  const ws = v.hours.filter((h) => (h.open || h.closed_reason) && h.why?.weather).map((h) => h.why!)
  if (ws.length) {
    const temps = ws.map((w) => w.temp).filter((t): t is number => t != null)
    const rain = ws.reduce((t, w) => t + (w.precip ?? 0), 0)
    const effs = ws.map((w) => w.weather_effect).filter((e): e is number => e != null)
    const avg = effs.length ? effs.reduce((a, b) => a + b, 0) / effs.length : null
    let text = temps.length ? `${Math.round(Math.min(...temps))}–${Math.round(Math.max(...temps))}°C` : 'Weather'
    text += rain >= 0.5 ? `, ${rain.toFixed(1)} mm rain` : ', dry'
    if (avg != null && Math.abs(avg) >= 0.02) text += ` · weather ${effectPct(avg)} foot traffic`
    chips.push({ text, tone: 'weather' })
  }
  return chips
})
const chipClass = { warn: 'bg-orange-50 text-orange-900', info: 'bg-slate-100 text-slate-700', weather: 'bg-sky-50 text-sky-900' }

const nearbyMax = computed(() => Math.max(1, ...(d.value?.nearby ?? []).map((a) => a.density ?? 0)))

const weekOption = computed<EChartsOption>(() => {
  const w = d.value?.week
  if (!w) return {}
  const data: [number, number, number | string][] = []
  w.occupancy.forEach((row, di) => row.forEach((v, hi) => data.push([hi, di, v ?? '-'])))
  return {
    animation: false,
    grid: { left: 44, right: 16, top: 8, bottom: 48 },
    tooltip: {
      formatter: (p: { value: [number, number, number | string] }) =>
        `${w.days[p.value[1]]} ${hh(w.hours[p.value[0]] ?? 0)}<br/>Usually <b>${p.value[2] === '-' ? 'closed' : p.value[2]}</b> people inside`,
    },
    xAxis: { type: 'category', data: w.hours.map(hh), axisLabel: { color: '#5b6170' } },
    yAxis: { type: 'category', data: w.days, inverse: true, axisLabel: { color: '#5b6170' } },
    visualMap: {
      min: 0,
      max: d.value?.capacity ?? 100,
      orient: 'horizontal',
      left: 'center',
      bottom: 0,
      text: ['Full', 'Quiet'],
      inRange: { color: ['#eef2fc', '#c9d6f5', '#93aeeb', '#5480de', '#1f3e9e'] },
    },
    series: [{ type: 'heatmap', data, itemStyle: { borderColor: '#fff', borderWidth: 2, borderRadius: 4 } }],
  }
})

const alertStyle = {
  warn: { box: 'bg-orange-50', icon: 'text-orange-800' },
  info: { box: 'bg-slate-100', icon: 'text-slate-700' },
  ok: { box: 'bg-green-50', icon: 'text-green-800' },
} as const
</script>

<template>
  <div class="space-y-5">
    <header class="flex flex-wrap items-end justify-between gap-4">
      <div>
        <p class="text-xs font-medium uppercase tracking-wide text-slate-500">
          <RouterLink to="/stores" class="hover:text-brand-700">All stores</RouterLink> / Store dashboard
        </p>
        <h1 class="mt-1 text-2xl font-semibold text-slate-900">{{ d?.store.name ?? 'Store' }}</h1>
        <p v-if="d" class="mt-0.5 text-sm text-slate-500">
          {{ fmtFull(d.sim_now) }} · Open {{ hh(d.store.config.open_from) }}–{{ hh(d.store.config.open_to) }} · Capacity {{ d.capacity }}
        </p>
      </div>
      <div class="flex flex-wrap items-center gap-3">
        <span v-if="d" class="inline-flex h-10 items-center gap-2 rounded-full bg-green-50 px-3 text-sm font-medium text-green-800">
          <span class="size-2 rounded-full bg-green-600" aria-hidden="true" />
          {{ d.mode === 'live' ? 'Live' : 'Replay' }} · refreshes every minute
        </span>
        <label class="sr-only" for="store-select">Store</label>
        <select
          v-if="stores.length > 1"
          id="store-select"
          :value="storeId ?? ''"
          class="h-10 rounded-lg border border-slate-300 bg-white px-3 text-sm"
          @change="changeStore"
        >
          <option v-for="s in stores" :key="s.id" :value="s.id">{{ s.name }}</option>
        </select>
      </div>
    </header>

    <LoadState :loading="dash.loading.value && !d" :error="dash.error.value" :empty="!storeId" empty-text="No stores are set up yet.">
      <template v-if="d">
        <!-- Key numbers -->
        <section aria-label="Key numbers" class="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
          <div class="rounded-xl border border-slate-200 bg-white p-4 shadow-sm">
            <div class="text-sm font-medium text-slate-500">In store now</div>
            <div class="mt-2 flex items-baseline gap-2">
              <span class="text-4xl font-semibold tabular-nums">{{ d.now.open ? int(d.now.occupancy) : '–' }}</span>
              <span class="text-slate-500">/ {{ d.capacity }}</span>
            </div>
            <div class="mt-3 h-2 overflow-hidden rounded-full bg-slate-100">
              <div class="h-full rounded-full" :style="{ width: Math.min(100, d.now.pct * 100) + '%', background: nowLevel.bar }" />
            </div>
            <p class="mt-2 text-sm text-slate-600">
              <span class="rounded-full px-2 py-0.5 text-xs font-semibold" :class="nowLevel.pill">{{ nowLevel.label }}</span>
              <template v-if="d.now.open"> {{ pct(d.now.pct) }} full</template>
            </p>
            <p class="mt-1 text-xs text-slate-500">
              <template v-if="d.now.source === 'live' && d.now.live_at">
                <span class="font-semibold text-green-800">Live</span> door counter · {{ fmtDayHour(d.now.live_at) }} · {{ int(d.now.entries_this_hour) }} in this hour
              </template>
              <template v-else-if="d.now.source === 'counted' && d.now.basis_from">
                Door counter {{ hh(hourOfDay(d.now.basis_from)) }}–{{ hh(hourOfDay(d.now.basis_to!)) }}: {{ int(d.now.entries_last_hour) }} entries
              </template>
              <template v-else-if="d.now.open">Estimated ({{ SOURCE[d.now.source] }})</template>
              <template v-else>Store is closed{{ d.context.public_holiday && d.is_today ? ` — ${d.context.public_holiday}` : '' }}{{ d.now.next_open ? ` · ${opensText(d.now.next_open, d.sim_now)}` : '' }}</template>
            </p>
          </div>

          <div class="rounded-xl border border-slate-200 bg-white p-4 shadow-sm">
            <div class="text-sm font-medium text-slate-500">Outside the store · {{ d.area.label }}</div>
            <div class="mt-2 flex items-baseline gap-2">
              <span class="text-4xl font-semibold tabular-nums">{{ int(d.area.passers_by) }}</span>
              <span class="text-slate-500">passers-by / h</span>
            </div>
            <p class="mt-3 text-sm font-semibold" :style="{ color: (d.area.vs_normal ?? 0) >= 0 ? AREA : '#5b6170' }">{{ vsUsual(d.area.vs_normal) }}</p>
            <p class="mt-1 text-xs text-slate-500">
              Street sensor: {{ d.area.density?.toFixed(1) ?? 'no reading' }} people nearby (usual {{ d.area.typical.toFixed(1) }})
              <template v-if="d.area.capture_today != null"> · {{ (d.area.capture_today * 100).toFixed(1) }}% came in today</template>
            </p>
          </div>

          <div class="rounded-xl border border-slate-200 bg-white p-4 shadow-sm">
            <div class="text-sm font-medium text-slate-500">Peak {{ d.is_today ? 'today' : d.weekday }}</div>
            <template v-if="d.peak">
              <div class="mt-2 flex items-baseline gap-2">
                <span class="text-4xl font-semibold tabular-nums">{{ int(d.peak.occupancy) }}</span>
                <span class="text-slate-500">at {{ hh(d.peak.hour) }}</span>
              </div>
              <p class="mt-3 text-sm font-semibold" :class="d.peak.level === 'near_capacity' ? 'text-orange-800' : 'text-slate-800'">
                {{ pct(d.peak.pct) }} of capacity · {{ peakLevel?.label }}
              </p>
              <p class="mt-1 text-xs text-slate-500">
                {{ SOURCE[d.peak.source] }}<template v-if="d.peak.occupancy_low != null"> · likely {{ d.peak.occupancy_low }}–{{ d.peak.occupancy_high }}</template>
              </p>
            </template>
            <p v-else class="mt-2 text-lg font-semibold text-slate-600">Closed all day</p>
          </div>

          <div class="rounded-xl border border-slate-200 bg-white p-4 shadow-sm">
            <div class="text-sm font-medium text-slate-500">Next hour</div>
            <template v-if="d.now.next_hour">
              <div class="mt-2 flex items-baseline gap-2">
                <span class="text-4xl font-semibold tabular-nums">{{ int(d.now.next_hour.occupancy) }}</span>
                <span class="text-slate-500">at {{ hh(d.now.next_hour.hour) }}</span>
              </div>
              <p class="mt-3 text-sm font-semibold text-slate-800">{{ LEVEL[d.now.next_hour.level].label }}</p>
              <p class="mt-1 text-xs text-slate-500">Suggested: {{ d.now.next_hour.staff }} cashier{{ d.now.next_hour.staff === 1 ? '' : 's' }}</p>
            </template>
            <p v-else class="mt-2 text-lg font-semibold text-slate-600">Closed next hour</p>
          </div>
        </section>

        <div class="grid gap-4 lg:grid-cols-[minmax(0,1fr)_22rem]">
          <BaseCard :title="d.is_today ? 'Today' : `${d.weekday} (forecast)`" subtitle="People in the store by hour, with passers-by outside">
            <template #actions>
              <SegmentedControl
                v-model="day"
                label="Day"
                :options="[
                  { value: 'today', label: 'Today' },
                  { value: 'tomorrow', label: 'Tomorrow' },
                ]"
              />
            </template>
            <ul v-if="dayChips.length" class="mb-3 flex flex-wrap gap-2" aria-label="What affects this day">
              <li v-for="(c, i) in dayChips" :key="i" class="rounded-full px-3 py-1 text-xs font-medium" :class="chipClass[c.tone]">{{ c.text }}</li>
            </ul>
            <StoreDayChart v-if="d.hours.some((h) => h.open)" :hours="d.hours" :capacity="d.capacity" :now-hour="nowHour" />
            <div v-else class="flex h-60 items-center justify-center rounded-lg bg-slate-50 text-sm text-slate-500">{{ d.context.public_holiday ? `Closed for ${d.context.public_holiday} (public holiday).` : `The store is closed on ${d.weekday}.` }}</div>
          </BaseCard>

          <div class="space-y-4">
            <BaseCard title="Heads-up">
              <ul class="space-y-3">
                <li v-for="(a, i) in d.alerts" :key="i" class="flex gap-3 rounded-lg p-3" :class="alertStyle[a.severity].box">
                  <svg class="mt-0.5 size-4 flex-none" :class="alertStyle[a.severity].icon" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">
                    <polyline v-if="a.severity === 'ok'" points="20 6 9 17 4 12" />
                    <template v-else><circle cx="12" cy="12" r="10" /><line x1="12" y1="8" x2="12" y2="12" /><line x1="12" y1="16" x2="12.01" y2="16" /></template>
                  </svg>
                  <div>
                    <p class="text-sm font-semibold text-slate-900">{{ a.title }}</p>
                    <p class="text-sm text-slate-600">{{ a.detail }}</p>
                  </div>
                </li>
                <li v-if="!d.alerts.length" class="text-sm text-slate-500">Nothing needs attention.</li>
              </ul>
            </BaseCard>

            <BaseCard v-if="d.is_today" title="Next hours">
              <table v-if="d.next_hours.length" class="w-full text-sm">
                <thead>
                  <tr class="text-left text-xs text-slate-500">
                    <th scope="col" class="pb-2 font-medium">Time</th>
                    <th scope="col" class="pb-2 font-medium">Expected</th>
                    <th scope="col" class="pb-2 font-medium">Level</th>
                    <th scope="col" class="pb-2 text-right font-medium">Cashiers</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="h in d.next_hours" :key="h.hour" class="border-t border-slate-100" :title="whyLines(h.why).join(' · ')">
                    <td class="py-2 font-semibold">
                      {{ hh(h.hour) }}
                      <span v-if="h.why?.event" class="ml-1 rounded bg-orange-50 px-1 text-[11px] font-semibold text-orange-900">{{ h.why.event }}</span>
                      <span v-else-if="(h.why?.precip ?? 0) >= 0.5" class="ml-1 rounded bg-sky-50 px-1 text-[11px] font-semibold text-sky-900">rain</span>
                    </td>
                    <td class="py-2 tabular-nums">{{ h.occupancy }}</td>
                    <td class="py-2"><span class="rounded-full px-2 py-0.5 text-xs font-semibold" :class="LEVEL[h.level].pill">{{ LEVEL[h.level].label }}</span></td>
                    <td class="py-2 text-right tabular-nums">{{ h.staff }}</td>
                  </tr>
                </tbody>
              </table>
              <p v-else class="text-sm text-slate-500">The store closes soon — no more open hours today.</p>
            </BaseCard>
          </div>
        </div>

        <div class="grid gap-4 xl:grid-cols-2">
          <StoreBakePlanCard :store-id="d.store.id" :day="d.day" :weekday="d.weekday" :batch-size="d.store.config.batch_size" />
          <StoreCalmHoursCard :store-id="d.store.id" :day="d.day" :weekday="d.weekday" />
        </div>

        <div class="grid gap-4 lg:grid-cols-[22rem_minmax(0,1fr)]">
          <BaseCard title="Around the store" subtitle="People near each street sensor, last hour">
            <ul class="space-y-3">
              <li v-for="a in d.nearby" :key="a.sensor_id">
                <div class="flex justify-between gap-3 text-sm">
                  <span class="font-medium">
                    {{ a.label }}
                    <span class="font-normal text-slate-500">· {{ a.is_store_sensor ? 'at the store' : a.distance_m < 1000 ? `${a.distance_m} m` : `${(a.distance_m / 1000).toFixed(1)} km` }}</span>
                  </span>
                  <span class="tabular-nums">
                    <strong>{{ a.density?.toFixed(1) ?? '–' }}</strong>
                    <span class="ml-1 text-xs font-semibold" :style="{ color: (a.vs_normal ?? 0) >= 0 ? AREA : '#5b6170' }">{{ a.online ? vsUsual(a.vs_normal) : 'offline' }}</span>
                  </span>
                </div>
                <div class="mt-1.5 h-2 overflow-hidden rounded-full bg-orange-50">
                  <div class="h-full rounded-full" :style="{ width: ((a.density ?? 0) / nearbyMax) * 100 + '%', background: AREA }" />
                </div>
              </li>
            </ul>
          </BaseCard>

          <BaseCard title="Typical week" :subtitle="`Usual people inside by weekday and hour (${d.week.source === 'door-counter' ? 'door counter, last 5 weeks' : 'estimated from foot traffic'})`">
            <BaseChart :option="weekOption" height="300px" aria-label="Usual number of people in the store by weekday and hour" />
          </BaseCard>
        </div>

        <StoreEventsCard
          :sensor-id="d.store.sensor_id"
          :sensor-label="d.area.label"
          :day="d.day"
          :events="d.upcoming_events"
          :nearby="d.nearby"
          :learned="d.learned_events ?? []"
          @changed="onEventsChanged"
        />

        <p class="text-xs text-slate-500">
          People inside = door-counter entries per hour × {{ d.visit_minutes }} min average visit. Passers-by = street-sensor density × 3600 ÷ dwell time.
          <template v-if="d.forecast_run"> Forecast run from {{ fmtDayHour(d.forecast_run.as_of) }} ({{ d.forecast_run.model_version }}).</template>
          <span :style="{ color: IN_STORE }"> Blue = in store,</span> <span :style="{ color: AREA }">orange = outside.</span>
        </p>
      </template>
    </LoadState>
  </div>
</template>
