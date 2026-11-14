<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue'
import LoadState from '@/components/common/LoadState.vue'
import SegmentedControl from '@/components/common/SegmentedControl.vue'
import { storesApi, type StoreSummary } from '@/api/stores'
import { useAsyncData } from '@/composables/useAsyncData'
import { AREA, IN_STORE, IN_STORE_FORECAST, LEVEL, hh, int, opensText, pct, vsUsual } from '@/lib/crowd'
import { fmtClock } from '@/lib/time'

const data = useAsyncData(
  () => true,
  (_v, s) => storesApi.overview(s),
)
const timer = setInterval(() => data.reload(), 60_000)
const nowMs = ref(Date.now())
const clockTimer = setInterval(() => (nowMs.value = Date.now()), 1000)
onBeforeUnmount(() => {
  clearInterval(timer)
  clearInterval(clockTimer)
})

const o = computed(() => data.data.value)
const sort = ref<'now' | 'peak' | 'name'>('now')

const rows = computed<StoreSummary[]>(() => {
  const list = [...(o.value?.stores ?? [])]
  if (sort.value === 'now') list.sort((a, b) => b.now.pct - a.now.pct)
  else if (sort.value === 'peak') list.sort((a, b) => (b.peak?.pct ?? 0) - (a.peak?.pct ?? 0))
  else list.sort((a, b) => a.store.name.localeCompare(b.store.name))
  return list
})

const totals = computed(() => {
  const s = o.value?.stores ?? []
  const inside = s.reduce((t, x) => t + (x.now.open ? x.now.occupancy : 0), 0)
  const cap = s.reduce((t, x) => t + x.capacity, 0)
  const passers = s.reduce((t, x) => t + (x.area.passers_by ?? 0), 0)
  const vs = s.filter((x) => x.area.vs_normal != null)
  const avgVs = vs.length ? vs.reduce((t, x) => t + x.area.vs_normal!, 0) / vs.length : null
  const nearNow = s.filter((x) => x.now.level === 'near_capacity')
  const nearLater = s.filter((x) => x.peak && x.peak.level === 'near_capacity')
  return { inside, cap, passers, avgVs, nearNow, nearLater }
})

const attention = computed(() =>
  (o.value?.stores ?? []).flatMap((s) => s.alerts.filter((a) => a.severity !== 'ok').map((a) => ({ ...a, store: s.store }))).slice(0, 6),
)
</script>

<template>
  <div class="space-y-5">
    <header class="flex flex-wrap items-end justify-between gap-4">
      <div>
        <p class="text-xs font-medium uppercase tracking-wide text-slate-500">Store crowd dashboard</p>
        <h1 class="mt-1 text-2xl font-semibold text-slate-900">All stores</h1>
        <p v-if="o" class="mt-0.5 text-sm text-slate-500">{{ o.stores.length }} stores · {{ fmtClock(nowMs) }}</p>
      </div>
      <span v-if="o" class="inline-flex h-10 items-center gap-2 rounded-full bg-green-50 px-3 text-sm font-medium text-green-800">
        <span class="size-2 rounded-full bg-green-600" aria-hidden="true" />
        {{ o.mode === 'live' ? 'Live' : 'Replay' }} · refreshes every minute
      </span>
    </header>

    <LoadState :loading="data.loading.value && !o" :error="data.error.value" :empty="!!o && !o.stores.length" empty-text="No stores are set up yet.">
      <template v-if="o">
        <section aria-label="Totals" class="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
          <div class="rounded-xl border border-slate-200 bg-white p-4 shadow-sm">
            <div class="text-sm font-medium text-slate-500">In stores now</div>
            <div class="mt-2 flex items-baseline gap-2">
              <span class="text-4xl font-semibold tabular-nums">{{ int(totals.inside) }}</span>
              <span class="text-slate-500">/ {{ int(totals.cap) }} capacity</span>
            </div>
            <p class="mt-2 text-sm text-slate-500">{{ totals.cap ? pct(totals.inside / totals.cap) : '–' }} full across all stores</p>
          </div>
          <div class="rounded-xl border border-slate-200 bg-white p-4 shadow-sm">
            <div class="text-sm font-medium text-slate-500">Passers-by outside</div>
            <div class="mt-2 flex items-baseline gap-2">
              <span class="text-4xl font-semibold tabular-nums">{{ int(totals.passers) }}</span>
              <span class="text-slate-500">/ h</span>
            </div>
            <p class="mt-2 text-sm font-semibold" :style="{ color: (totals.avgVs ?? 0) >= 0 ? AREA : '#5b6170' }">{{ vsUsual(totals.avgVs) }}</p>
          </div>
          <div class="rounded-xl border border-slate-200 bg-white p-4 shadow-sm">
            <div class="text-sm font-medium text-slate-500">Near capacity now</div>
            <div class="mt-2 text-4xl font-semibold tabular-nums">{{ totals.nearNow.length }}</div>
            <p class="mt-2 text-sm text-slate-500">{{ totals.nearNow.map((s) => s.store.name).join(', ') || 'No store above 90%' }}</p>
          </div>
          <div class="rounded-xl border border-slate-200 bg-white p-4 shadow-sm">
            <div class="text-sm font-medium text-slate-500">Expected to hit 90%+ today</div>
            <div class="mt-2 text-4xl font-semibold tabular-nums">{{ totals.nearLater.length }}</div>
            <p class="mt-2 text-sm text-slate-500">
              {{ totals.nearLater.map((s) => `${s.store.name} ${hh(s.peak!.hour)}`).join(' · ') || 'None' }}
            </p>
          </div>
        </section>

        <section v-if="attention.length" aria-label="Needs attention" class="rounded-xl border border-slate-200 bg-white p-4 shadow-sm">
          <h2 class="mb-3 text-base font-semibold text-slate-900">Needs attention</h2>
          <ul class="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
            <li v-for="(a, i) in attention" :key="i" class="rounded-lg p-3" :class="a.severity === 'warn' ? 'bg-orange-50' : 'bg-slate-100'">
              <p class="text-xs font-semibold uppercase tracking-wide text-slate-500">{{ a.store.name }}</p>
              <p class="text-sm font-semibold text-slate-900">{{ a.title }}</p>
              <p class="text-sm text-slate-600">{{ a.detail }}</p>
            </li>
          </ul>
        </section>

        <section aria-label="Stores" class="rounded-xl border border-slate-200 bg-white p-4 shadow-sm">
          <div class="mb-3 flex flex-wrap items-center justify-between gap-3">
            <div>
              <h2 class="text-base font-semibold text-slate-900">Stores</h2>
              <p class="text-sm text-slate-500">Open a store to see its full dashboard</p>
            </div>
            <SegmentedControl
              v-model="sort"
              label="Sort stores"
              :options="[
                { value: 'now', label: 'Fullest now' },
                { value: 'peak', label: 'Highest peak' },
                { value: 'name', label: 'A–Z' },
              ]"
            />
          </div>
          <div class="overflow-x-auto">
            <table class="w-full min-w-[56rem] text-sm">
              <thead>
                <tr class="text-left text-xs text-slate-500">
                  <th scope="col" class="pb-2 pr-3 font-medium">Store</th>
                  <th scope="col" class="px-3 pb-2 font-medium">In store now</th>
                  <th scope="col" class="px-3 pb-2 font-medium">Outside</th>
                  <th scope="col" class="px-3 pb-2 font-medium">Peak today</th>
                  <th scope="col" class="px-3 pb-2 font-medium">Today</th>
                  <th scope="col" class="pb-2 pl-3"><span class="sr-only">Open</span></th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="s in rows" :key="s.store.id" class="border-t border-slate-100">
                  <td class="py-4 pr-3">
                    <div class="font-semibold text-slate-900">{{ s.store.name }}</div>
                    <div class="text-xs text-slate-500">Capacity {{ s.capacity }} · sensor {{ s.store.sensor_label }}</div>
                    <div v-if="s.context.public_holiday || s.context.events.length || s.context.school_holiday" class="mt-1 flex flex-wrap gap-1">
                      <span v-if="s.context.public_holiday" class="rounded bg-orange-50 px-1.5 text-[11px] font-semibold text-orange-900">{{ s.context.public_holiday }}</span>
                      <span v-for="e in s.context.events" :key="e.name" class="rounded bg-orange-50 px-1.5 text-[11px] font-semibold text-orange-900">{{ e.name }}</span>
                      <span v-if="s.context.school_holiday" class="rounded bg-slate-100 px-1.5 text-[11px] font-semibold text-slate-700">{{ s.context.school_holiday }}</span>
                    </div>
                  </td>
                  <td class="w-64 px-3 py-4">
                    <div class="flex items-center gap-2">
                      <span class="text-xl font-semibold tabular-nums">{{ s.now.open ? int(s.now.occupancy) : '–' }}</span>
                      <span v-if="s.now.source === 'live'" class="text-[11px] font-semibold uppercase tracking-wide text-green-800">live</span>
                      <span class="rounded-full px-2 py-0.5 text-xs font-semibold" :class="LEVEL[s.now.level].pill">
                        {{ LEVEL[s.now.level].label }}<template v-if="s.now.open"> · {{ pct(s.now.pct) }}</template><template v-else-if="s.now.next_open"> · {{ opensText(s.now.next_open, o.sim_now) }}</template>
                      </span>
                    </div>
                    <div class="mt-1.5 h-2 overflow-hidden rounded-full bg-slate-100">
                      <div class="h-full" :style="{ width: Math.min(100, s.now.pct * 100) + '%', background: LEVEL[s.now.level].bar }" />
                    </div>
                  </td>
                  <td class="px-3 py-4">
                    <div class="font-semibold tabular-nums">{{ int(s.area.passers_by) }} / h</div>
                    <div class="text-xs font-semibold" :style="{ color: (s.area.vs_normal ?? 0) >= 0 ? AREA : '#5b6170' }">{{ vsUsual(s.area.vs_normal) }}</div>
                  </td>
                  <td class="px-3 py-4">
                    <template v-if="s.peak">
                      <div class="font-semibold tabular-nums">{{ int(s.peak.occupancy) }} at {{ hh(s.peak.hour) }}</div>
                      <div class="text-xs" :class="s.peak.level === 'near_capacity' ? 'font-semibold text-orange-800' : 'text-slate-500'">{{ pct(s.peak.pct) }} of capacity</div>
                    </template>
                    <span v-else class="text-slate-500">Closed today{{ s.context.public_holiday ? ` (${s.context.public_holiday})` : '' }}</span>
                  </td>
                  <td class="w-52 px-3 py-4">
                    <div class="flex h-9 items-end gap-0.5 border-b border-slate-300" :title="`${s.store.name}: people inside by hour`">
                      <div
                        v-for="h in s.hours"
                        :key="h.hour"
                        class="flex-1 rounded-t-sm"
                        :style="{
                          height: Math.max(2, Math.min(36, (h.occupancy / s.capacity) * 36)) + 'px',
                          background: h.source === 'counted' ? IN_STORE : IN_STORE_FORECAST,
                        }"
                      />
                    </div>
                  </td>
                  <td class="py-4 pl-3 text-right">
                    <RouterLink
                      :to="{ name: 'store', params: { id: s.store.id } }"
                      class="inline-flex h-10 items-center gap-1.5 rounded-lg border border-slate-300 px-3 font-semibold text-slate-900 hover:bg-slate-50"
                      :aria-label="`Open ${s.store.name} dashboard`"
                    >
                      Open
                      <svg class="size-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true"><line x1="5" y1="12" x2="19" y2="12" /><polyline points="12 5 19 12 12 19" /></svg>
                    </RouterLink>
                  </td>
                </tr>
              </tbody>
            </table>
          </div>
          <div class="mt-3 flex flex-wrap gap-5 border-t border-slate-100 pt-3 text-xs text-slate-600">
            <span class="flex items-center gap-2"><span class="size-3 rounded-sm" :style="{ background: IN_STORE }" />Counted so far</span>
            <span class="flex items-center gap-2"><span class="size-3 rounded-sm" :style="{ background: IN_STORE_FORECAST }" />Forecast</span>
            <span>Comfortable under 70% · Busy 70–89% · Near capacity 90%+</span>
          </div>
        </section>
      </template>
    </LoadState>
  </div>
</template>
