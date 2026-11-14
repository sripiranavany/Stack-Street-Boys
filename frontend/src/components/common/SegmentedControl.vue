<script setup lang="ts" generic="T extends string">
defineProps<{ options: { value: T; label: string }[]; label: string }>()
const model = defineModel<T>({ required: true })
</script>

<template>
  <div role="radiogroup" :aria-label="label" class="inline-flex rounded-lg bg-slate-100 p-0.5">
    <button
      v-for="o in options"
      :key="o.value"
      type="button"
      role="radio"
      :aria-checked="model === o.value"
      class="rounded-md px-3 py-1 text-sm font-medium transition"
      :class="model === o.value ? 'bg-white text-brand-700 shadow-sm' : 'text-slate-600 hover:text-slate-900'"
      @click="model = o.value"
    >
      {{ o.label }}
    </button>
  </div>
</template>
