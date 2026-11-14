import { createRouter, createWebHistory } from 'vue-router'
import { useRoleStore } from '@/stores/role'

declare module 'vue-router' {
  interface RouteMeta {
    title: string
  }
}

const router = createRouter({
  history: createWebHistory(),
  routes: [
    { path: '/', name: 'map', component: () => import('@/views/MapView.vue'), meta: { title: 'City map' } },
    {
      path: '/sensors/:id?',
      name: 'sensor',
      component: () => import('@/views/SensorDetailView.vue'),
      props: true,
      meta: { title: 'Sensor detail' },
    },
    { path: '/patterns/:id?', name: 'patterns', component: () => import('@/views/PatternsView.vue'), props: true, meta: { title: 'Patterns' } },
    { path: '/compare', name: 'compare', component: () => import('@/views/CompareView.vue'), meta: { title: 'Compare' } },
    { path: '/forecast/:id?', name: 'forecast', component: () => import('@/views/ForecastView.vue'), props: true, meta: { title: 'Forecast' } },
    { path: '/what-if', name: 'whatif', component: () => import('@/views/WhatIfView.vue'), meta: { title: 'What if' } },
    { path: '/stores', name: 'stores', component: () => import('@/views/StoresOverviewView.vue'), meta: { title: 'All stores' } },
    {
      path: '/stores/:id',
      name: 'store',
      component: () => import('@/views/StoreDashboardView.vue'),
      props: true,
      meta: { title: 'Store dashboard' },
    },
    { path: '/:pathMatch(.*)*', redirect: '/' },
  ],
  scrollBehavior: () => ({ top: 0 }),
})

// UI-level role access: a role can only open the pages in its `access` list; anything else goes to its home page.
router.beforeEach((to) => {
  const role = useRoleStore()
  if (!role.can(to.name)) return role.role.home
})

router.afterEach((to) => {
  document.title = `${to.meta.title} · City Pulse`
})

export default router
