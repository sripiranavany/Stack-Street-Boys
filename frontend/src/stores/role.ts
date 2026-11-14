import { computed, ref, watch } from 'vue'
import { defineStore } from 'pinia'
import { HIDDEN_ROLES, ROLES, canAccess, type RoleId } from '@/lib/roles'

const KEY = 'city-pulse:role'

function load(): RoleId {
  try {
    const v = localStorage.getItem(KEY)
    if (v && v in ROLES && !HIDDEN_ROLES.includes(v as RoleId)) return v as RoleId
  } catch {
    /* storage unavailable */
  }
  return 'manager'
}

export const useRoleStore = defineStore('role', () => {
  const id = ref<RoleId>(load())
  const role = computed(() => ROLES[id.value])

  watch(id, (v) => {
    try {
      localStorage.setItem(KEY, v)
    } catch {
      /* ignore */
    }
  })

  /** Whether the current role may open the route with this name. */
  const can = (name: unknown) => canAccess(id.value, name)

  return { id, role, can }
})
