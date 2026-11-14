<script setup lang="ts">
import { computed } from 'vue'
import BaseChart from './BaseChart.vue'
import type { EChartsOption } from '@/composables/useEChart'
import type { ForecastPoint, TimeseriesPoint } from '@/api/types'
import { fmtFull } from '@/lib/time'

const props = defineProps<{ history: TimeseriesPoint[]; forecast: ForecastPoint[]; height?: string }>()

const option = computed<EChartsOption>(() => {
  const t = (iso: string) => new Date(iso).getTime()
  const f = props.forecast
  const hasBand = f.every((p) => p.lower != null && p.upper != null)
  const boundary = f[0] ? t(f[0].time) : undefined

  return {
    animation: false,
    grid: { left: 52, right: 16, top: 36, bottom: 40 },
    legend: { top: 0, right: 0, data: ['Recent', 'Forecast', ...(hasBand ? ['Confidence band'] : [])] },
    tooltip: {
      trigger: 'axis',
      formatter: (ps: { axisValue: number }[]) => {
        const x = ps[0]?.axisValue ?? 0
        const hist = props.history.find((p) => t(p.time) === x)
        const fp = f.find((p) => t(p.time) === x)
        const lines: string[] = [fmtFull(x)]
        if (hist?.value != null) lines.push(`Recent: <b>${hist.value.toFixed(1)}</b>`)
        if (fp) {
          lines.push(`Forecast: <b>${fp.value.toFixed(1)}</b>`)
          if (fp.lower != null && fp.upper != null) lines.push(`Range: ${fp.lower.toFixed(1)} – ${fp.upper.toFixed(1)}`)
        }
        return lines.join('<br/>')
      },
    },
    xAxis: { type: 'time' },
    yAxis: { type: 'value', name: 'Density', splitLine: { lineStyle: { color: '#e2e8f0' } } },
    series: [
      {
        name: 'Recent',
        type: 'line',
        showSymbol: false,
        lineStyle: { color: '#64748b', width: 2 },
        itemStyle: { color: '#64748b' },
        data: props.history.map((p) => [t(p.time), p.value]),
      },
      ...(hasBand
        ? [
            {
              name: 'band-base',
              type: 'line',
              stack: 'band',
              symbol: 'none',
              lineStyle: { opacity: 0 },
              tooltip: { show: false },
              data: f.map((p) => [t(p.time), p.lower]),
            },
            {
              name: 'Confidence band',
              type: 'line',
              stack: 'band',
              symbol: 'none',
              lineStyle: { opacity: 0 },
              areaStyle: { color: 'rgba(47,111,237,0.18)' },
              itemStyle: { color: 'rgba(47,111,237,0.35)' },
              data: f.map((p) => [t(p.time), (p.upper ?? 0) - (p.lower ?? 0)]),
            },
          ]
        : []),
      {
        name: 'Forecast',
        type: 'line',
        showSymbol: false,
        lineStyle: { color: '#2f6fed', width: 2.5 },
        itemStyle: { color: '#2f6fed' },
        data: f.map((p) => [t(p.time), p.value]),
        markLine: boundary
          ? { symbol: 'none', silent: true, label: { formatter: 'now', position: 'insideStartTop' }, lineStyle: { color: '#94a3b8', type: 'dashed' }, data: [{ xAxis: boundary }] }
          : undefined,
      },
    ],
  }
})
</script>

<template>
  <BaseChart :option="option" :height="height ?? '360px'" aria-label="72 hour forecast with confidence band" />
</template>
