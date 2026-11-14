<script setup lang="ts">
import { computed } from 'vue'
import BaseCard from '@/components/common/BaseCard.vue'
import LoadState from '@/components/common/LoadState.vue'
import { storesApi } from '@/api/stores'
import { useAsyncData } from '@/composables/useAsyncData'
import { hh, int } from '@/lib/crowd'

const props = defineProps<{ storeId: string; day: string; weekday: string; batchSize?: number }>()

const plan = useAsyncData(
  computed(() => ({ id: props.storeId, day: props.day })),
  (q, s) => storesApi.plan(q.id, q.day, s),
)
const p = computed(() => plan.data.value)
const open = computed(() => p.value?.hours.filter((h) => h.open) ?? [])
const batch = computed(() => props.batchSize ?? 40)
/** oven slots: hours with batches to bake */
const oven = computed(() => (p.value?.hours ?? []).filter((h) => h.batches > 0))
const maxDemand = computed(() => Math.max(1, ...open.value.map((h) => h.bake_demand)))
const saved = computed(() => {
  const k = p.value?.kpis
  if (!k) return null
  return {
    items: k.bake_items_fixed - k.bake_items_ebb,
    waste: k.waste_items_fixed - k.waste_items_ebb,
    staff: k.staff_hours_fixed - k.staff_hours_ebb,
  }
})
</script>

<template>
  <BaseCard title="Bakery and staffing plan" :subtitle="`${weekday}: oven times and cashiers from the shopper forecast, against a fixed every-day plan`">
    <LoadState :loading="plan.loading.value && !p" :error="plan.error.value" :empty="!!p && !open.length" :empty-text="`Closed on ${weekday} — nothing to bake.`">
      <template v-if="p && saved">
        <dl class="grid grid-cols-3 gap-3">
          <div class="rounded-lg bg-slate-50 p-3">
            <dt class="text-xs font-medium text-slate-500">Bake</dt>
            <dd class="text-xl font-semibold tabular-nums">{{ int(p.kpis.bake_items_ebb) }}</dd>
            <dd class="text-xs text-slate-500">fixed plan {{ int(p.kpis.bake_items_fixed) }}</dd>
          </div>
          <div class="rounded-lg bg-slate-50 p-3">
            <dt class="text-xs font-medium text-slate-500">Expected leftovers</dt>
            <dd class="text-xl font-semibold tabular-nums">{{ int(p.kpis.waste_items_ebb) }}</dd>
            <dd class="text-xs" :class="saved.waste > 0 ? 'font-semibold text-green-800' : 'text-slate-500'">
              {{ saved.waste > 0 ? `${int(saved.waste)} fewer than fixed` : `fixed plan ${int(p.kpis.waste_items_fixed)}` }}
            </dd>
          </div>
          <div class="rounded-lg bg-slate-50 p-3">
            <dt class="text-xs font-medium text-slate-500">Cashier hours</dt>
            <dd class="text-xl font-semibold tabular-nums">{{ p.kpis.staff_hours_ebb }}</dd>
            <dd class="text-xs" :class="saved.staff > 0 ? 'font-semibold text-green-800' : 'text-slate-500'">
              {{ saved.staff > 0 ? `${saved.staff} h fewer than fixed rota` : `fixed rota ${p.kpis.staff_hours_fixed} h` }}
            </dd>
          </div>
        </dl>
        <p v-if="p.kpis.missed_sales_items_fixed > 0" class="mt-2 text-xs text-orange-900">
          The fixed plan would run out: about {{ int(p.kpis.missed_sales_items_fixed) }} items short.
        </p>
        <p v-else-if="p.kpis.understaffed_hours_fixed > 0" class="mt-2 text-xs text-orange-900">
          The fixed rota is short-staffed for {{ p.kpis.understaffed_hours_fixed }} hour{{ p.kpis.understaffed_hours_fixed === 1 ? '' : 's' }}, around the {{ hh(p.kpis.peak_hour) }} peak.
        </p>

        <h3 class="mt-4 text-sm font-semibold text-slate-900">Oven schedule</h3>
        <p class="text-xs text-slate-500">Batches of {{ batch }}, baked about 30 min before they're needed.</p>
        <ul class="mt-2 flex flex-wrap gap-2" aria-label="Oven schedule">
          <li v-for="h in oven" :key="h.hour" class="rounded-lg border border-amber-200 bg-amber-50 px-2.5 py-1.5 text-sm">
            <span class="font-semibold">{{ hh(h.hour) }}</span>
            <span class="text-amber-900"> · {{ h.batches }} batch{{ h.batches === 1 ? '' : 'es' }}</span>
          </li>
        </ul>

        <h3 class="mt-4 text-sm font-semibold text-slate-900">Hour by hour</h3>
        <div class="mt-2 overflow-x-auto">
          <table class="w-full min-w-[30rem] text-xs">
            <thead>
              <tr class="text-left text-slate-500">
                <th scope="col" class="pb-1 font-medium">Hour</th>
                <th scope="col" class="pb-1 font-medium">Shoppers</th>
                <th scope="col" class="pb-1 font-medium">Bakery demand</th>
                <th scope="col" class="pb-1 text-right font-medium">Cashiers (fixed)</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="h in open" :key="h.hour" class="border-t border-slate-100">
                <td class="py-1 font-semibold">{{ hh(h.hour) }}</td>
                <td class="py-1 tabular-nums">{{ int(h.visitors) }}</td>
                <td class="py-1">
                  <div class="flex items-center gap-2">
                    <div class="h-2 w-24 overflow-hidden rounded-full bg-amber-50">
                      <div class="h-full rounded-full bg-amber-500" :style="{ width: (h.bake_demand / maxDemand) * 100 + '%' }" />
                    </div>
                    <span class="tabular-nums">{{ int(h.bake_demand) }}</span>
                  </div>
                </td>
                <td class="py-1 text-right tabular-nums">
                  <span :class="h.cashiers > h.fixed_cashiers ? 'font-semibold text-orange-900' : ''">{{ h.cashiers }}</span>
                  <span class="text-slate-400"> ({{ h.fixed_cashiers }})</span>
                </td>
              </tr>
            </tbody>
          </table>
        </div>
      </template>
    </LoadState>
  </BaseCard>
</template>
