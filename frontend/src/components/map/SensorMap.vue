<script setup lang="ts">
import { onBeforeUnmount, onMounted, useTemplateRef, watch } from 'vue'
import L from 'leaflet'
import { busynessColor, busynessRadius } from '@/lib/color'

export interface MapMarker {
  id: string
  name: string
  lat: number
  lon: number
  /** 0..1, null = no data in this frame */
  intensity: number | null
  tooltip: string
}

const props = defineProps<{ markers: MapMarker[]; selectedId?: string }>()
const emit = defineEmits<{ select: [id: string] }>()

const el = useTemplateRef<HTMLDivElement>('el')
let map: L.Map | undefined
const layers = new Map<string, L.CircleMarker>()
let fitted = false

function sync() {
  if (!map) return
  const seen = new Set<string>()
  for (const m of props.markers) {
    seen.add(m.id)
    const style: L.CircleMarkerOptions = {
      radius: m.intensity == null ? 6 : busynessRadius(m.intensity),
      fillColor: busynessColor(m.intensity),
      color: m.id === props.selectedId ? '#0f172a' : '#ffffff',
      weight: m.id === props.selectedId ? 3 : 1.5,
      fillOpacity: m.intensity == null ? 0.4 : 0.8,
    }
    let c = layers.get(m.id)
    if (!c) {
      c = L.circleMarker([m.lat, m.lon], style).addTo(map)
      c.on('click', () => emit('select', m.id))
      c.bindTooltip(m.tooltip)
      layers.set(m.id, c)
    } else {
      c.setStyle(style)
      c.setRadius(style.radius!)
      c.setTooltipContent(m.tooltip)
    }
    if (m.id === props.selectedId) c.bringToFront()
  }
  for (const [id, c] of layers) {
    if (!seen.has(id)) {
      c.remove()
      layers.delete(id)
    }
  }
  if (!fitted && props.markers.length) {
    map.fitBounds(L.latLngBounds(props.markers.map((m) => [m.lat, m.lon] as [number, number])).pad(0.25), { maxZoom: 16 })
    fitted = true
  }
}

onMounted(() => {
  map = L.map(el.value!, { zoomControl: true, attributionControl: true }).setView([49.14, 9.22], 13)
  L.tileLayer('https://tile.openstreetmap.org/{z}/{x}/{y}.png', {
    maxZoom: 19,
    attribution: '© OpenStreetMap contributors',
  }).addTo(map)
  sync()
})
watch(() => [props.markers, props.selectedId], sync)
onBeforeUnmount(() => {
  map?.remove()
  map = undefined
  layers.clear()
})
</script>

<template>
  <div ref="el" class="z-0 h-full min-h-[420px] w-full rounded-xl" role="application" aria-label="Map of sensors" />
</template>
