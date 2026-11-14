<script setup lang="ts">
import { onMounted } from 'vue'
import AppHeader from '@/components/layout/AppHeader.vue'
import { useSensorsStore } from '@/stores/sensors'

const sensors = useSensorsStore()
onMounted(() => sensors.fetch())
</script>

<template>
  <div class="flex min-h-full flex-col">
    <AppHeader />
    <main class="mx-auto w-full max-w-7xl flex-1 px-3 py-4 sm:px-4 sm:py-6">
      <RouterView />
    </main>
    <footer class="border-t border-slate-200 bg-white">
      <div class="mx-auto flex max-w-7xl flex-wrap items-center justify-between gap-x-4 gap-y-1 px-4 py-4 text-sm text-slate-500">
        <span>© {{ new Date().getFullYear() }} <strong class="font-semibold text-slate-700">Stack Street Boys</strong></span>
        <span>Powered by <strong class="font-semibold text-slate-700">STACKIT</strong></span>
      </div>
    </footer>
  </div>
</template>
