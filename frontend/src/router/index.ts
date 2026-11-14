import { createRouter, createWebHistory } from 'vue-router'

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
    { path: '/:pathMatch(.*)*', redirect: '/' },
  ],
  scrollBehavior: () => ({ top: 0 }),
})

router.afterEach((to) => {
  document.title = `${to.meta.title} · City Pulse`
})

export default router
