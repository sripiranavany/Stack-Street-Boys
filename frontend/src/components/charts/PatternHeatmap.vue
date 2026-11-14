<script setup lang="ts">
import { computed } from 'vue'
import BaseChart from './BaseChart.vue'
import type { EChartsOption } from '@/composables/useEChart'
import type { Patterns } from '@/api/types'
import { busynessColor } from '@/lib/color'

const props = defineProps<{ patterns: Patterns; height?: string }>()

const option = computed<EChartsOption>(() => {
  const data: [number, number, number | null][] = []
  let max = 0
  props.patterns.matrix.forEach((row, d) =>
    row.forEach((v, h) => {
      data.push([h, d, v])
      if (v != null) max = Math.max(max, v)
    }),
  )
  return {
    animation: false,
    grid: { left: 48, right: 24, top: 12, bottom: 56 },
    tooltip: {
      formatter: (p: { value: [number, number, number | null] }) => {
        const [h, d, v] = p.value
        const n = props.patterns.counts[d]?.[h] ?? 0
        return `${props.patterns.days[d]} ${String(h).padStart(2, '0')}:00<br/>Average: <b>${v == null ? 'no data' : v.toFixed(1)}</b><br/><span style="color:#94a3b8">${n} readings</span>`
      },
    },
    xAxis: {
      type: 'category',
      data: Array.from({ length: 24 }, (_, h) => String(h).padStart(2, '0')),
      splitArea: { show: true },
    },
    yAxis: { type: 'category', data: props.patterns.days, inverse: true, splitArea: { show: true } },
    visualMap: {
      min: 0,
      max: max || 1,
      calculable: false,
      orient: 'horizontal',
      left: 'center',
      bottom: 0,
      inRange: { color: [0, 0.25, 0.5, 0.8, 1].map(busynessColor) },
      text: ['Busy', 'Quiet'],
    },
    series: [
      {
        type: 'heatmap',
        data: data.map(([h, d, v]) => [h, d, v ?? '-']),
        itemStyle: { borderColor: '#fff', borderWidth: 2, borderRadius: 3 },
        emphasis: { itemStyle: { shadowBlur: 8, shadowColor: 'rgba(0,0,0,0.3)' } },
      },
    ],
  }
})
</script>

<template>
  <BaseChart :option="option" :height="height ?? '420px'" aria-label="Average density by weekday and hour" />
</template>
