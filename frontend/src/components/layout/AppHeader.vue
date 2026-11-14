<script setup lang="ts">
import { useRoleStore } from '@/stores/role'
import { ROLE_LIST } from '@/lib/roles'

const role = useRoleStore()
const links = [
  { to: '/', label: 'Map' },
  { to: '/sensors', label: 'Sensor' },
  { to: '/patterns', label: 'Patterns' },
  { to: '/compare', label: 'Compare' },
  { to: '/forecast', label: 'Forecast' },
]
</script>

<template>
  <header class="sticky top-0 z-[1000] border-b border-slate-200 bg-white/90 backdrop-blur">
    <div class="mx-auto flex max-w-7xl flex-wrap items-center gap-x-6 gap-y-2 px-4 py-3">
      <RouterLink to="/" class="flex items-center gap-2 text-lg font-semibold text-slate-900">
        <span class="inline-block size-3 rounded-full bg-brand-500 ring-4 ring-brand-100" aria-hidden="true" />
        City Pulse
      </RouterLink>

      <nav class="flex gap-1" aria-label="Main">
        <RouterLink
          v-for="l in links"
          :key="l.to"
          :to="l.to"
          class="rounded-md px-3 py-1.5 text-sm font-medium text-slate-600 hover:bg-slate-100"
          active-class="!bg-brand-50 !text-brand-700"
          :exact-active-class="l.to === '/' ? '!bg-brand-50 !text-brand-700' : ''"
        >
          {{ l.label }}
        </RouterLink>
      </nav>

      <div class="ml-auto flex items-center gap-2"><span class="text-xs font-medium uppercase tracking-wide text-slate-500">View as</span>
        <div role="radiogroup" aria-label="Role" class="inline-flex rounded-lg bg-slate-100 p-0.5">
          <button
            v-for="r in ROLE_LIST"
            :key="r.id"
            type="button"
            role="radio"
            :aria-checked="role.id === r.id"
            class="rounded-md px-3 py-1 text-sm font-medium transition"
            :class="role.id === r.id ? 'bg-white text-brand-700 shadow-sm' : 'text-slate-600 hover:text-slate-900'"
            @click="role.id = r.id"
          >
            {{ r.label }}
          </button>
        </div>
      </div>
    </div>
  </header>
</template>
