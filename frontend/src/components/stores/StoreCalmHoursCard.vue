<script setup lang="ts">
import { computed, ref } from 'vue'
import BaseCard from '@/components/common/BaseCard.vue'
import LoadState from '@/components/common/LoadState.vue'
import { storesApi, type OfferResult } from '@/api/stores'
import { useAsyncData } from '@/composables/useAsyncData'
import { IN_STORE, IN_STORE_FORECAST, hh, int } from '@/lib/crowd'
import { berlinDayStart, fmtDayHour, hourOfDay, weekdayIndex } from '@/lib/time'

const props = defineProps<{ storeId: string; day: string; weekday: string }>()

const campaign = useAsyncData(computed(() => props.storeId), (id, s) => storesApi.campaign(id, s))
const sim = useAsyncData(
  computed(() => (campaign.data.value ? { id: props.storeId, day: props.day } : null)),
  (q) => storesApi.simulateCalm(q.id, q.day),
)
const stats = useAsyncData(computed(() => props.storeId), (id, s) => storesApi.offers(id, s))

const r = computed(() => sim.data.value?.result)
const bars = computed(() => {
  const x = r.value
  if (!x) return []
  const max = Math.max(1, ...x.none.load)
  return x.none.load
    .map((v, h) => ({ h, open: x.open[h], before: v, after: x.ebb.load[h] ?? v, max }))
    .filter((b) => b.open)
})

const sending = ref(false)
const offer = ref<OfferResult | null>(null)
const offerError = ref('')
async function sendTest() {
  sending.value = true
  offerError.value = ''
  try {
    offer.value = await storesApi.sendOffer(props.storeId, `demo-${Date.now().toString(36)}`, weekdayIndex(berlinDayStart(props.day)))
    stats.reload()
  } catch (e) {
    offerError.value = (e as Error).message
  } finally {
    sending.value = false
  }
}
</script>

<template>
  <BaseCard title="App discounts for quieter hours" :subtitle="`Calm Hours: loyalty customers who usually shop at the ${weekday} peak get a coupon for a calmer hour`">
    <template #actions>
      <span v-if="campaign.data.value" class="rounded-full px-2 py-0.5 text-xs font-semibold" :class="campaign.data.value.active ? 'bg-green-50 text-green-800' : 'bg-slate-100 text-slate-600'">
        {{ campaign.data.value.active ? 'Campaign on' : 'Campaign off' }}
      </span>
    </template>
    <LoadState :loading="(campaign.loading.value || sim.loading.value) && !r" :error="campaign.error.value ?? sim.error.value" :empty="!!r && !bars.length" :empty-text="`Closed on ${weekday}.`">
      <template v-if="r">
        <dl class="grid grid-cols-3 gap-3">
          <div class="rounded-lg bg-slate-50 p-3">
            <dt class="text-xs font-medium text-slate-500">Peak at {{ hh(r.none.peak_hour) }}</dt>
            <dd class="text-xl font-semibold tabular-nums">{{ int(r.ebb.peak) }}<span class="text-sm font-normal text-slate-500">/h</span></dd>
            <dd class="text-xs font-semibold text-green-800">{{ r.peak_reduction_pct > 0 ? `−${r.peak_reduction_pct.toFixed(0)}% from ${int(r.none.peak)}` : 'no change' }}</dd>
          </div>
          <div class="rounded-lg bg-slate-50 p-3">
            <dt class="text-xs font-medium text-slate-500">Shoppers moved</dt>
            <dd class="text-xl font-semibold tabular-nums">{{ int(r.ebb.shifted_customers) }}</dd>
            <dd class="text-xs text-slate-500">of {{ int(r.ebb.offers) }} offers</dd>
          </div>
          <div class="rounded-lg bg-slate-50 p-3">
            <dt class="text-xs font-medium text-slate-500">Coupon cost</dt>
            <dd class="text-xl font-semibold tabular-nums">€{{ int(r.ebb.coupon_cost_eur) }}</dd>
            <dd class="text-xs text-slate-500">{{ r.staff_hours_saved > 0 ? `${r.staff_hours_saved} cashier h saved` : 'for the day' }}</dd>
          </div>
        </dl>

        <div class="mt-4">
          <div class="flex h-24 items-end gap-1" role="img" :aria-label="`Shoppers per hour on ${weekday}, without and with the campaign`">
            <div v-for="b in bars" :key="b.h" class="relative flex h-full flex-1 items-end" :title="`${hh(b.h)}: ${int(b.before)} → ${int(b.after)} shoppers`">
              <div class="absolute bottom-0 w-full rounded-t-sm" :style="{ height: (b.before / b.max) * 100 + '%', background: IN_STORE_FORECAST }" />
              <div class="absolute bottom-0 left-1/4 w-1/2 rounded-t-sm" :style="{ height: (b.after / b.max) * 100 + '%', background: IN_STORE }" />
            </div>
          </div>
          <div class="mt-1 flex gap-1 text-[10px] text-slate-500">
            <span v-for="b in bars" :key="b.h" class="flex-1 text-center">{{ b.h }}</span>
          </div>
          <div class="mt-2 flex flex-wrap gap-4 text-xs text-slate-600">
            <span class="flex items-center gap-1.5"><span class="size-3 rounded-sm" :style="{ background: IN_STORE_FORECAST }" />Without campaign</span>
            <span class="flex items-center gap-1.5"><span class="size-3 rounded-sm" :style="{ background: IN_STORE }" />With Calm Hours</span>
            <span>Target: no hour above {{ int(r.cap) }} shoppers</span>
          </div>
        </div>

        <div class="mt-4 flex flex-wrap items-center gap-3 border-t border-slate-100 pt-3">
          <button
            type="button"
            :disabled="sending || !campaign.data.value?.active"
            class="h-10 rounded-lg bg-brand-600 px-4 text-sm font-semibold text-white hover:bg-brand-700 disabled:opacity-60"
            @click="sendTest"
          >
            {{ sending ? 'Sending…' : 'Send a test offer' }}
          </button>
          <span v-if="stats.data.value" class="text-xs text-slate-500">
            {{ stats.data.value.issued }} offers issued · {{ stats.data.value.redeemed }} redeemed
          </span>
        </div>
        <p v-if="offerError" role="alert" class="mt-2 text-sm text-red-700">{{ offerError }}</p>
        <div v-else-if="offer" role="status" class="mt-3 rounded-lg border border-dashed border-brand-500 bg-brand-50 p-3 text-sm">
          <template v-if="offer.offer">
            <p class="font-semibold text-brand-700">
              {{ offer.offer.discount_pct }}% off {{ offer.offer.applies_to }} · {{ fmtDayHour(offer.offer.slot_start) }}–{{ hh(hourOfDay(offer.offer.slot_end)) }}
            </p>
            <p class="text-slate-700">{{ offer.offer.reason }}</p>
            <p class="mt-1 font-mono text-xs text-slate-600">Coupon {{ offer.offer.coupon_code }}</p>
          </template>
          <p v-else class="text-slate-700">{{ offer.reason }}</p>
        </div>
      </template>
    </LoadState>
  </BaseCard>
</template>
