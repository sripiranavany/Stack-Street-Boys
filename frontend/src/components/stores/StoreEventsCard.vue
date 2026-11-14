<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import BaseCard from '@/components/common/BaseCard.vue'
import { storesApi, type EventSize, type LearnedEvent, type NearbyArea, type UpcomingEvent } from '@/api/stores'
import { effectPct, hh, sameEvent } from '@/lib/crowd'
import { berlinDayStart, fmtDayHour, HOUR } from '@/lib/time'

const props = defineProps<{
  sensorId: string
  sensorLabel: string
  day: string
  events: UpcomingEvent[]
  nearby: NearbyArea[]
  learned: LearnedEvent[]
}>()
const emit = defineEmits<{ changed: [] }>()

const SIZES: { value: EventSize; label: string }[] = [
  { value: 'S', label: 'Small (street stall, busker)' },
  { value: 'M', label: 'Medium (market, concert)' },
  { value: 'L', label: 'Large (festival, big match)' },
]

const name = ref('')
const date = ref(props.day)
const from = ref('15:00')
const to = ref('19:00')
const size = ref<EventSize>('M')
/** kind of event: a new event borrows the learned effect of past events of the same kind */
const KINDS = [
  { value: 'festival', label: 'Festival' },
  { value: 'market', label: 'Market' },
  { value: 'concert', label: 'Concert' },
  { value: 'sports', label: 'Sports' },
  { value: 'event', label: 'Other' },
]
const category = ref('event')
const includeNearby = ref(false)
const busy = ref(false)
const error = ref('')
const notice = ref('')
watch(() => props.day, (d) => (date.value = d))

/** the learned event series the typed name belongs to, if any */
const match = computed(() => (name.value.trim().length >= 4 ? props.learned.find((l) => sameEvent(name.value, l.name)) : undefined))
const dateLong = new Intl.DateTimeFormat('en-GB', { timeZone: 'Europe/Berlin', day: 'numeric', month: 'short', year: 'numeric' })
const fmtLong = (iso: string) => dateLong.format(new Date(iso))

/** street sensors within 400 m (not the store's own) */
const close = computed(() => props.nearby.filter((a) => !a.is_store_sensor && a.distance_m <= 400))

function toIso(d: string, hm: string): string {
  const [h, m] = hm.split(':').map(Number)
  return new Date(berlinDayStart(d) + ((h ?? 0) + (m ?? 0) / 60) * HOUR).toISOString()
}

async function add() {
  error.value = notice.value = ''
  const start = toIso(date.value, from.value)
  const end = toIso(date.value, to.value)
  if (!name.value.trim()) return void (error.value = 'Give the event a name.')
  if (end <= start) return void (error.value = 'The end time must be after the start time.')
  busy.value = true
  try {
    await storesApi.addEvent({
      name: name.value.trim(),
      category: match.value?.category ?? category.value,
      start,
      end,
      size: size.value,
      sensor_ids: [props.sensorId, ...(includeNearby.value ? close.value.map((a) => a.sensor_id) : [])],
    })
    name.value = ''
    notice.value = 'Event saved. The forecast is being recalculated — numbers update within a minute.'
    emit('changed')
  } catch (e) {
    error.value = (e as Error).message
  } finally {
    busy.value = false
  }
}

async function remove(e: UpcomingEvent) {
  if (!window.confirm(`Remove "${e.name}"? The forecast will be recalculated without it.`)) return
  error.value = notice.value = ''
  try {
    await storesApi.deleteEvent(e.id)
    notice.value = 'Event removed. The forecast is being recalculated.'
    emit('changed')
  } catch (err) {
    error.value = (err as Error).message
  }
}
</script>

<template>
  <BaseCard title="Events near the store" :subtitle="`Next 7 days at ${sensorLabel}. Events change the foot-traffic and store forecast.`">
    <section v-if="learned.length" class="mb-4 rounded-lg bg-slate-50 p-3" aria-label="Learned from past events">
      <h3 class="text-sm font-semibold text-slate-900">Learned from past events</h3>
      <ul class="mt-2 space-y-2">
        <li v-for="l in learned" :key="l.series" class="text-sm text-slate-700">
          <span class="font-semibold text-slate-900">{{ l.name }}</span>
          <span class="text-slate-500"> · {{ l.occurrences }} occurrence{{ l.occurrences === 1 ? '' : 's' }}</span>
          <br />
          <template v-if="l.at_store_street != null">
            Foot traffic at {{ sensorLabel }} {{ effectPct(l.at_store_street - 1) }}<template v-if="l.at_store_peak_hour != null">, strongest around {{ hh(l.at_store_peak_hour) }}</template>.
          </template>
          <template v-if="l.at_store_shoppers != null">
            Shoppers {{ effectPct(l.at_store_shoppers - 1) }}{{ l.at_store_shoppers < 1.05 && (l.at_store_street ?? 1) > 1.2 ? ' — the crowds did not come in to shop' : '' }}.
          </template>
          <span v-if="l.next_start" class="mt-0.5 block text-xs font-medium text-brand-700">
            Next: {{ l.next_name }} · {{ fmtLong(l.next_start) }} — already in the forecast when it comes into range
          </span>
        </li>
      </ul>
    </section>

    <ul v-if="events.length" class="mb-4 divide-y divide-slate-100">
      <li v-for="e in events" :key="e.id" class="flex items-center justify-between gap-3 py-2">
        <div>
          <p class="text-sm font-semibold text-slate-900">{{ e.name }}</p>
          <p class="text-xs text-slate-500">
            {{ fmtDayHour(e.start) }} – {{ fmtDayHour(e.end) }}<template v-if="e.size"> · size {{ e.size }}</template>
            <template v-if="e.source === 'projected'"> · expected (repeats every year)</template>
          </p>
        </div>
        <button
          type="button"
          class="h-9 rounded-lg border border-slate-300 px-3 text-sm font-medium text-slate-700 hover:bg-slate-50"
          :aria-label="`Remove ${e.name}`"
          @click="remove(e)"
        >
          Remove
        </button>
      </li>
    </ul>
    <p v-else class="mb-4 text-sm text-slate-500">No events planned. Add one so the forecast knows about it.</p>

    <form class="grid gap-3 sm:grid-cols-2" @submit.prevent="add">
      <label class="flex flex-col gap-1 text-sm sm:col-span-2">
        <span class="font-medium text-slate-700">Event name</span>
        <input v-model="name" type="text" maxlength="80" placeholder="e.g. Farmers' market" class="h-10 rounded-lg border border-slate-300 px-3" />
        <span v-if="match" class="text-xs text-green-800">Recognised: the forecast will use what was learned from {{ match.name }} — size is ignored.</span>
        <span v-else-if="name.trim().length >= 4" class="text-xs text-slate-500">New event: the forecast uses past events of the same kind, or the size below.</span>
      </label>
      <label class="flex flex-col gap-1 text-sm">
        <span class="font-medium text-slate-700">Date</span>
        <input v-model="date" type="date" required class="h-10 rounded-lg border border-slate-300 px-3" />
      </label>
      <label class="flex flex-col gap-1 text-sm">
        <span class="font-medium text-slate-700">Kind</span>
        <select v-model="category" :disabled="!!match" class="h-10 rounded-lg border border-slate-300 bg-white px-3 disabled:opacity-60">
          <option v-for="k in KINDS" :key="k.value" :value="k.value">{{ k.label }}</option>
        </select>
      </label>
      <label class="flex flex-col gap-1 text-sm">
        <span class="font-medium text-slate-700">Size</span>
        <select v-model="size" class="h-10 rounded-lg border border-slate-300 bg-white px-3">
          <option v-for="o in SIZES" :key="o.value" :value="o.value">{{ o.label }}</option>
        </select>
      </label>
      <label class="flex flex-col gap-1 text-sm">
        <span class="font-medium text-slate-700">From</span>
        <input v-model="from" type="time" step="3600" required class="h-10 rounded-lg border border-slate-300 px-3" />
      </label>
      <label class="flex flex-col gap-1 text-sm">
        <span class="font-medium text-slate-700">To</span>
        <input v-model="to" type="time" step="3600" required class="h-10 rounded-lg border border-slate-300 px-3" />
      </label>
      <label v-if="close.length" class="flex items-center gap-2 text-sm text-slate-700 sm:col-span-2">
        <input v-model="includeNearby" type="checkbox" class="size-4" />
        Also affects {{ close.map((a) => a.label).join(', ') }}
      </label>
      <div class="flex flex-wrap items-center gap-3 sm:col-span-2">
        <button type="submit" :disabled="busy" class="h-10 rounded-lg bg-brand-600 px-4 text-sm font-semibold text-white hover:bg-brand-700 disabled:opacity-60">
          {{ busy ? 'Saving…' : 'Add event' }}
        </button>
        <p v-if="error" role="alert" class="text-sm text-red-700">{{ error }}</p>
        <p v-else-if="notice" role="status" class="text-sm text-green-800">{{ notice }}</p>
      </div>
    </form>
  </BaseCard>
</template>
