<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'

const props = defineProps<{ length: number; label: string }>()
const index = defineModel<number>({ required: true })
const playing = ref(false)
let timer: ReturnType<typeof setInterval> | undefined

function stop() {
  playing.value = false
  clearInterval(timer)
}
function toggle() {
  if (playing.value) return stop()
  if (index.value >= props.length - 1) index.value = 0
  playing.value = true
  timer = setInterval(() => {
    if (index.value >= props.length - 1) return stop()
    index.value++
  }, 500)
}
watch(() => props.length, stop)
onBeforeUnmount(stop)
</script>

<template>
  <div class="flex items-center gap-2 sm:gap-3">
    <button
      type="button"
      class="grid size-9 shrink-0 place-items-center rounded-full bg-brand-500 text-white hover:bg-brand-600 disabled:opacity-40"
      :aria-label="playing ? 'Pause' : 'Play the day'"
      :disabled="length < 2"
      @click="toggle"
    >
      <svg v-if="!playing" viewBox="0 0 20 20" class="size-4 fill-current"><path d="M6 4l10 6-10 6z" /></svg>
      <svg v-else viewBox="0 0 20 20" class="size-4 fill-current"><path d="M5 4h3v12H5zM12 4h3v12h-3z" /></svg>
    </button>
    <input
      :key="length"
      v-model.number="index"
      type="range"
      min="0"
      :max="Math.max(length - 1, 0)"
      step="1"
      class="h-2 flex-1 accent-brand-500"
      aria-label="Time"
      :disabled="length < 2"
      @input="stop"
    />
    <output class="w-24 shrink-0 text-right sm:w-28 text-sm font-medium tabular-nums text-slate-700">{{ label }}</output>
  </div>
</template>
