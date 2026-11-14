<script setup lang="ts">
import { useSensorsStore } from '@/stores/sensors'

defineProps<{ label?: string }>()
const model = defineModel<string | undefined>({ required: true })
const sensors = useSensorsStore()
const id = `sensor-${Math.random().toString(36).slice(2, 8)}`
</script>

<template>
  <div class="flex items-center gap-2">
    <label :for="id" class="text-sm font-medium text-slate-600">{{ label ?? 'Sensor' }}</label>
    <select
      :id="id"
      v-model="model"
      class="rounded-md border border-slate-300 bg-white px-2 py-1.5 text-sm focus:border-brand-500 focus:outline-none focus:ring-2 focus:ring-brand-100"
    >
      <option v-for="s in sensors.active" :key="s.sensor_id" :value="s.sensor_id">{{ s.name }} ({{ s.sensor_id.slice(0, 4) }})</option>
    </select>
  </div>
</template>
