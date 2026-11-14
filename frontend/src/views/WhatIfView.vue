<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import PageHeader from '@/components/common/PageHeader.vue'
import BaseCard from '@/components/common/BaseCard.vue'
import SegmentedControl from '@/components/common/SegmentedControl.vue'
import BaseChart from '@/components/charts/BaseChart.vue'
import type { EChartsOption } from '@/composables/useEChart'
import { storesApi, type EventSize, type StoreSummary, type WeatherScenario, type WhatIfPoint, type WhatIfResult } from '@/api/stores'
import { AREA, IN_STORE, IN_STORE_FORECAST, effectPct, hh, int, sameEvent } from '@/lib/crowd'
import { berlinDayStart, fmtDate, fmtFull, hourOfDay, HOUR } from '@/lib/time'

const stores = ref<StoreSummary[]>([])
const simNow = ref<string | null>(null)
const storeId = ref('')
const learnedNames = ref<string[]>([])

const scenario = ref<WeatherScenario>('none')
const withEvent = ref(true)
const eventName = ref('Lichterfest')
const eventKind = ref('festival')
const eventSize = ref<EventSize>('M')
const eventDay = ref('')
const eventFrom = ref('17:00')
const eventTo = ref('22:00')

const running = ref(false)
const error = ref('')
const result = ref<WhatIfResult | null>(null)
const ranFor = ref<{ store: StoreSummary; withEvent: boolean; day: string; scenario: WeatherScenario } | null>(null)

const SCENARIOS: { value: WeatherScenario; label: string }[] = [
  { value: 'none', label: 'As forecast' },
  { value: 'sunny', label: 'Sunny' },
  { value: 'rain', label: 'Rain' },
  { value: 'cold', label: 'Cold (0°C)' },
  { value: 'hot', label: 'Hot (28°C)' },
]
const KINDS = ['festival', 'market', 'concert', 'sports', 'event']
const store = computed(() => stores.value.find((s) => s.store.id === storeId.value))
const recognised = computed(() => learnedNames.value.find((n) => eventName.value.trim().length >= 4 && sameEvent(eventName.value, n)))

/** "2026-11-08" in Berlin for an ISO time */
const berlinDate = (iso: string | number) => new Intl.DateTimeFormat('en-CA', { timeZone: 'Europe/Berlin' }).format(new Date(iso))

onMounted(async () => {
  try {
    const o = await storesApi.overview()
    stores.value = o.stores
    simNow.value = o.sim_now
    storeId.value = o.stores.find((s) => s.store.id.includes('neckarufer'))?.store.id ?? o.stores[0]?.store.id ?? ''
    eventDay.value = berlinDate(new Date(o.sim_now).getTime() + 24 * HOUR)
    if (storeId.value) {
      const d = await storesApi.dashboard(storeId.value, 'today')
      learnedNames.value = d.learned_events.map((l) => l.name)
    }
  } catch (e) {
    error.value = (e as Error).message
  }
})

function toIso(d: string, hm: string): string {
  const [h, m] = hm.split(':').map(Number)
  return new Date(berlinDayStart(d) + ((h ?? 0) + (m ?? 0) / 60) * HOUR).toISOString()
}

const dayOptions = computed(() => {
  if (!simNow.value) return []
  const t = new Date(simNow.value).getTime()
  return Array.from({ length: 6 }, (_, i) => {
    const iso = berlinDate(t + (i + 1) * 24 * HOUR)
    return { value: iso, label: new Intl.DateTimeFormat('en-GB', { timeZone: 'Europe/Berlin', weekday: 'short', day: 'numeric', month: 'short' }).format(new Date(t + (i + 1) * 24 * HOUR)) }
  })
})

async function run() {
  const s = store.value
  if (!s) return
  error.value = ''
  if (withEvent.value && toIso(eventDay.value, eventTo.value) <= toIso(eventDay.value, eventFrom.value)) {
    error.value = 'The event must end after it starts.'
    return
  }
  running.value = true
  try {
    result.value = await storesApi.whatIf({
      sensor_id: s.store.sensor_id,
      scenario: scenario.value,
      hours: 168,
      ...(withEvent.value
        ? {
            event_name: eventName.value.trim() || 'What-if event',
            event_category: eventKind.value,
            event_size: eventSize.value,
            event_start: toIso(eventDay.value, eventFrom.value),
            event_end: toIso(eventDay.value, eventTo.value),
          }
        : {}),
    })
    ranFor.value = { store: s, withEvent: withEvent.value, day: eventDay.value, scenario: scenario.value }
  } catch (e) {
    error.value = (e as Error).message
  } finally {
    running.value = false
  }
}
watch(storeId, () => (result.value = null))

/** the hours the summary looks at: the event day, or the whole week for a weather-only scenario */
const inWindow = (p: WhatIfPoint) => !ranFor.value?.withEvent || berlinDate(p.ts) === ranFor.value.day
const street = computed(() => (result.value?.points ?? []).filter(inWindow))
const shoppers = computed(() => (result.value?.stores.find((x) => x.store_id === ranFor.value?.store.store.id)?.points ?? []).filter(inWindow))

const summary = computed(() => {
  const s = ranFor.value?.store
  if (!s || !street.value.length) return null
  const cfg = s.store.config
  const cph = Math.max(cfg.customers_per_cashier_h ?? 40, 1)
  const minStaff = cfg.min_staff ?? 1
  const bakePer = (cfg.bakery_buy_rate ?? 0.3) * (cfg.items_per_buyer ?? 2)
  const sum = (xs: number[]) => xs.reduce((a, b) => a + b, 0)
  const peak = (xs: number[]) => Math.max(0, ...xs)
  const cashiers = (v: number) => Math.max(minStaff, Math.ceil(v / cph))
  const sb = shoppers.value.map((p) => p.baseline), ss = shoppers.value.map((p) => p.scenario)
  const fb = street.value.map((p) => p.baseline), fs = street.value.map((p) => p.scenario)
  const rel = (a: number, b: number) => (a > 0 ? b / a - 1 : 0)
  return {
    streetPeak: { base: peak(fb), scen: peak(fs), change: rel(peak(fb), peak(fs)) },
    shoppers: { base: sum(sb), scen: sum(ss), change: rel(sum(sb), sum(ss)) },
    cashiers: { base: shoppers.value.length ? peak(sb.map(cashiers)) : 0, scen: shoppers.value.length ? peak(ss.map(cashiers)) : 0 },
    bake: { base: sum(sb) * bakePer, scen: sum(ss) * bakePer },
    hasStore: shoppers.value.length > 0,
  }
})

function chartOption(points: WhatIfPoint[], color: string, light: string, name: string, bars: boolean): EChartsOption {
  const labels = points.map((p) => (ranFor.value?.withEvent ? hh(hourOfDay(p.ts)) : `${fmtDate(p.ts)} ${hh(hourOfDay(p.ts))}`))
  return {
    animation: false,
    grid: { left: 44, right: 16, top: 28, bottom: 40 },
    legend: { top: 0, data: [`${name} — usual`, `${name} — scenario`] },
    tooltip: { trigger: 'axis' },
    xAxis: { type: 'category', data: labels, axisLabel: { color: '#5b6170' } },
    yAxis: { type: 'value', axisLabel: { color: '#5b6170' } },
    series: [
      bars
        ? { name: `${name} — usual`, type: 'bar', data: points.map((p) => Math.round(p.baseline)), itemStyle: { color: light }, barGap: '-100%', barMaxWidth: 28 }
        : { name: `${name} — usual`, type: 'line', data: points.map((p) => Math.round(p.baseline)), symbol: 'none', lineStyle: { color, type: 'dashed', width: 2 }, itemStyle: { color } },
      bars
        ? { name: `${name} — scenario`, type: 'bar', data: points.map((p) => Math.round(p.scenario)), itemStyle: { color, opacity: 0.85 }, barMaxWidth: 14 }
        : { name: `${name} — scenario`, type: 'line', data: points.map((p) => Math.round(p.scenario)), symbol: 'none', lineStyle: { color, width: 3 }, itemStyle: { color }, areaStyle: { color: light, opacity: 0.35 } },
    ],
  }
}
const streetOption = computed(() => chartOption(street.value, AREA, '#f6d3b8', 'Passers-by density', false))
const shopperOption = computed(() => chartOption(shoppers.value.filter((p) => p.baseline > 0 || p.scenario > 0), IN_STORE, IN_STORE_FORECAST, 'Shoppers / h', true))
const basisText = computed(() => {
  const e = result.value?.event
  if (!e) return ''
  if (e.basis === 'learned') return `learned from ${e.based_on}`
  if (e.basis === 'category') return `based on ${e.based_on}`
  if (e.basis === 'size') return 'estimated from the event size (no similar event on record)'
  return 'from its labelled profile'
})
</script>

<template>
  <PageHeader title="What if…?" :subtitle="simNow ? `Change the weather or add an event and see the next 7 days change · forecast from ${fmtFull(simNow)}` : 'Change the weather or add an event and see the forecast change'" />

  <div class="grid gap-4 lg:grid-cols-[22rem_minmax(0,1fr)]">
    <BaseCard title="Scenario">
      <form class="space-y-4" @submit.prevent="run">
        <label class="flex flex-col gap-1 text-sm">
          <span class="font-medium text-slate-700">Store</span>
          <select v-model="storeId" class="h-10 rounded-lg border border-slate-300 bg-white px-3">
            <option v-for="s in stores" :key="s.store.id" :value="s.store.id">{{ s.store.name }} · sensor {{ s.store.sensor_label }}</option>
          </select>
        </label>

        <div class="space-y-1">
          <span class="text-sm font-medium text-slate-700">Weather all week</span>
          <SegmentedControl v-model="scenario" label="Weather scenario" :options="SCENARIOS" class="flex-wrap" />
        </div>

        <label class="flex items-center gap-2 text-sm font-medium text-slate-700">
          <input v-model="withEvent" type="checkbox" class="size-4" /> Add an event near the store
        </label>
        <fieldset v-if="withEvent" class="space-y-3 rounded-lg border border-slate-200 p-3">
          <legend class="sr-only">Event</legend>
          <label class="flex flex-col gap-1 text-sm">
            <span class="font-medium text-slate-700">Name</span>
            <input v-model="eventName" type="text" maxlength="80" class="h-10 rounded-lg border border-slate-300 px-3" />
            <span v-if="recognised" class="text-xs text-green-800">Recognised: uses what was learned from {{ recognised }}.</span>
            <span v-else class="text-xs text-slate-500">New event: uses past events of the same kind, or the size.</span>
          </label>
          <div class="grid grid-cols-2 gap-3">
            <label class="flex flex-col gap-1 text-sm">
              <span class="font-medium text-slate-700">Kind</span>
              <select v-model="eventKind" class="h-10 rounded-lg border border-slate-300 bg-white px-3 capitalize">
                <option v-for="k in KINDS" :key="k" :value="k">{{ k === 'event' ? 'other' : k }}</option>
              </select>
            </label>
            <label class="flex flex-col gap-1 text-sm">
              <span class="font-medium text-slate-700">Size</span>
              <select v-model="eventSize" class="h-10 rounded-lg border border-slate-300 bg-white px-3">
                <option value="S">Small</option>
                <option value="M">Medium</option>
                <option value="L">Large</option>
              </select>
            </label>
          </div>
          <label class="flex flex-col gap-1 text-sm">
            <span class="font-medium text-slate-700">Day</span>
            <select v-model="eventDay" class="h-10 rounded-lg border border-slate-300 bg-white px-3">
              <option v-for="o in dayOptions" :key="o.value" :value="o.value">{{ o.label }}</option>
            </select>
          </label>
          <div class="grid grid-cols-2 gap-3">
            <label class="flex flex-col gap-1 text-sm">
              <span class="font-medium text-slate-700">From</span>
              <input v-model="eventFrom" type="time" step="3600" class="h-10 rounded-lg border border-slate-300 px-3" />
            </label>
            <label class="flex flex-col gap-1 text-sm">
              <span class="font-medium text-slate-700">To</span>
              <input v-model="eventTo" type="time" step="3600" class="h-10 rounded-lg border border-slate-300 px-3" />
            </label>
          </div>
        </fieldset>

        <button type="submit" :disabled="running || !storeId" class="h-11 w-full rounded-lg bg-brand-600 text-sm font-semibold text-white hover:bg-brand-700 disabled:opacity-60">
          {{ running ? 'Running the forecast…' : 'Run scenario' }}
        </button>
        <p v-if="error" role="alert" class="text-sm text-red-700">{{ error }}</p>
      </form>
    </BaseCard>

    <div class="space-y-4">
      <div v-if="!result" class="flex h-full min-h-60 items-center justify-center rounded-xl border border-dashed border-slate-300 bg-white p-6 text-center text-sm text-slate-500">
        Pick a scenario and run it. The same forecasting model behind the dashboard runs twice — as forecast and with your changes.
      </div>

      <template v-if="result && summary && ranFor">
        <section aria-label="Scenario impact" class="grid gap-3 sm:grid-cols-2 xl:grid-cols-4">
          <div class="rounded-xl border border-slate-200 bg-white p-4 shadow-sm">
            <div class="text-xs font-medium text-slate-500">Peak foot traffic outside</div>
            <div class="mt-1 text-2xl font-semibold" :style="{ color: AREA }">{{ effectPct(summary.streetPeak.change) }}</div>
            <div class="text-xs text-slate-500">{{ summary.streetPeak.base.toFixed(0) }} → {{ summary.streetPeak.scen.toFixed(0) }} people near the sensor</div>
          </div>
          <div class="rounded-xl border border-slate-200 bg-white p-4 shadow-sm">
            <div class="text-xs font-medium text-slate-500">Shoppers {{ ranFor.withEvent ? 'that day' : 'this week' }}</div>
            <div class="mt-1 text-2xl font-semibold" :style="{ color: IN_STORE }">{{ summary.hasStore ? effectPct(summary.shoppers.change) : '–' }}</div>
            <div class="text-xs text-slate-500">{{ int(summary.shoppers.base) }} → {{ int(summary.shoppers.scen) }} door entries</div>
          </div>
          <div class="rounded-xl border border-slate-200 bg-white p-4 shadow-sm">
            <div class="text-xs font-medium text-slate-500">Cashiers at the busiest hour</div>
            <div class="mt-1 text-2xl font-semibold">{{ summary.cashiers.base }} → {{ summary.cashiers.scen }}</div>
            <div class="text-xs text-slate-500">{{ summary.cashiers.scen > summary.cashiers.base ? 'call in extra staff' : summary.cashiers.scen < summary.cashiers.base ? 'staff can be freed up' : 'no change needed' }}</div>
          </div>
          <div class="rounded-xl border border-slate-200 bg-white p-4 shadow-sm">
            <div class="text-xs font-medium text-slate-500">Bakery items to bake</div>
            <div class="mt-1 text-2xl font-semibold">{{ int(summary.bake.base) }} → {{ int(summary.bake.scen) }}</div>
            <div class="text-xs text-slate-500">from expected shoppers</div>
          </div>
        </section>

        <p v-if="result.event" class="rounded-lg bg-orange-50 px-3 py-2 text-sm text-orange-900">
          <strong>{{ result.event.name }}</strong>: foot traffic {{ effectPct(result.event.mean_mult - 1) }} on average during the event, {{ basisText }}.
          <template v-if="summary.hasStore && summary.shoppers.change < summary.streetPeak.change / 2 && result.event.basis === 'learned'">
            Shoppers rise much less: past occurrences showed the crowds don't come in to shop.
          </template>
        </p>

        <BaseCard :title="ranFor.withEvent ? `Outside the store · ${fmtDate(berlinDayStart(ranFor.day))}` : 'Outside the store · next 7 days'" :subtitle="`Street sensor ${ranFor.store.store.sensor_label}: usual forecast vs your scenario`">
          <BaseChart :option="streetOption" height="260px" aria-label="Foot traffic outside the store, usual vs scenario" />
        </BaseCard>
        <BaseCard v-if="summary.hasStore" :title="`Shoppers at ${ranFor.store.store.name}`" subtitle="Door entries per open hour: usual forecast vs your scenario">
          <BaseChart :option="shopperOption" height="260px" aria-label="Shoppers per hour, usual vs scenario" />
        </BaseCard>
      </template>
    </div>
  </div>
</template>
