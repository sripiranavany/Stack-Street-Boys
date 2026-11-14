<script setup lang="ts">
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import { useRoleStore } from '@/stores/role'
import { ROLE_LIST, type RoleId } from '@/lib/roles'

const role = useRoleStore()
const router = useRouter()
const links = computed(() => role.role.nav)

// Switching role lands on that role's home page.
function pick(id: RoleId) {
  role.id = id
  if (router.currentRoute.value.path !== role.role.home) router.push(role.role.home)
}
</script>

<template>
  <header class="sticky top-0 z-[1000] border-b border-slate-200 bg-white/90 backdrop-blur">
    <div class="mx-auto flex max-w-7xl flex-wrap items-center gap-x-6 gap-y-2 px-4 py-2 md:py-3">
      <RouterLink :to="role.role.home" class="flex items-center gap-2 text-lg font-semibold text-slate-900">
        <span class="inline-block size-3 rounded-full bg-brand-500 ring-4 ring-brand-100" aria-hidden="true" />
        City Pulse
      </RouterLink>

      <nav class="order-last flex w-full flex-wrap gap-1 md:order-none md:w-auto" aria-label="Main">
        <RouterLink
          v-for="l in links"
          :key="l.to"
          :to="l.to"
          class="whitespace-nowrap rounded-md px-2.5 py-1.5 sm:px-3 text-sm font-medium text-slate-600 hover:bg-slate-100"
          active-class="!bg-brand-50 !text-brand-700"
          :exact-active-class="l.to === '/' ? '!bg-brand-50 !text-brand-700' : ''"
        >
          {{ l.label }}
        </RouterLink>
      </nav>

      <div class="flex w-full items-center gap-2 md:ml-auto md:w-auto"><span class="text-xs font-medium uppercase tracking-wide text-slate-500">View as</span>
        <div role="radiogroup" aria-label="Role" class="inline-flex max-w-full flex-wrap rounded-lg bg-slate-100 p-0.5">
          <button
            v-for="r in ROLE_LIST"
            :key="r.id"
            type="button"
            role="radio"
            :aria-checked="role.id === r.id"
            class="whitespace-nowrap rounded-md px-2.5 py-1 text-sm font-medium transition sm:px-3"
            :class="role.id === r.id ? 'bg-white text-brand-700 shadow-sm' : 'text-slate-600 hover:text-slate-900'"
            :title="r.tagline"
            @click="pick(r.id)"
          >
            {{ r.label }}
          </button>
        </div>
      </div>
    </div>
  </header>
</template>
