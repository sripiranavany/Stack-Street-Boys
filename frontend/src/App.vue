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
    <main class="mx-auto w-full max-w-7xl flex-1 px-4 py-6">
      <RouterView />
    </main>
  </div>
</template>
