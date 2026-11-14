<script setup lang="ts">
defineProps<{ loading?: boolean; error?: Error | null; empty?: boolean; emptyText?: string }>()
</script>

<template>
  <div v-if="error" role="alert" class="rounded-lg border border-red-200 bg-red-50 p-4 text-sm text-red-700">
    Could not load data: {{ error.message }}
  </div>
  <div v-else-if="loading" class="flex h-40 items-center justify-center text-sm text-slate-500" aria-busy="true">
    <span class="mr-2 inline-block size-4 animate-spin rounded-full border-2 border-slate-300 border-t-brand-500" />
    Loading…
  </div>
  <div v-else-if="empty" class="flex h-40 items-center justify-center rounded-lg bg-slate-50 px-4 text-center text-sm text-slate-500">
    {{ emptyText ?? 'No data for this selection.' }}
  </div>
  <slot v-else />
</template>
