import { computed, ref, watch } from 'vue'
import { defineStore } from 'pinia'
import { ROLES, type RoleId } from '@/lib/roles'

const KEY = 'city-pulse:role'

function load(): RoleId {
  try {
    const v = localStorage.getItem(KEY)
    if (v && v in ROLES) return v as RoleId
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

  return { id, role }
})
