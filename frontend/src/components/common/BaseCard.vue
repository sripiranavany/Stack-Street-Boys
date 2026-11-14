<script setup lang="ts">
defineProps<{ title?: string; subtitle?: string }>()
</script>

<template>
  <section class="rounded-xl border border-slate-200 bg-white p-4 shadow-sm">
    <header v-if="title || $slots.actions" class="mb-3 flex flex-wrap items-start justify-between gap-2">
      <div>
        <h2 class="text-base font-semibold text-slate-900">{{ title }}</h2>
        <p v-if="subtitle" class="text-sm text-slate-500">{{ subtitle }}</p>
      </div>
      <slot name="actions" />
    </header>
    <slot />
  </section>
</template>
