<script setup lang="ts">
import { computed } from 'vue'
import BaseChart from './BaseChart.vue'
import type { EChartsOption } from '@/composables/useEChart'
import type { CityEvent, Holiday, TimeseriesPoint, WeatherRow } from '@/api/types'
import { berlinDayStart, DAY, fmtFull } from '@/lib/time'

const props = withDefaults(
  defineProps<{
    points: TimeseriesPoint[]
    weather?: WeatherRow[]
    holidays?: Holiday[]
    events?: CityEvent[]
    seriesName?: string
    height?: string
    /** plot the normalized value (value / p95) instead of the raw one */
    normalized?: boolean
    color?: string
    yMax?: number
  }>(),
  { weather: () => [], holidays: () => [], events: () => [], seriesName: 'Density', height: '360px', color: '#2f6fed' },
)

const option = computed<EChartsOption>(() => {
  const showWeather = props.weather.length > 0
  const val = (p: TimeseriesPoint) => (props.normalized ? (p.normalized ?? null) : p.value)

  const areas = [
    ...props.holidays.map((h) => [
      { name: h.name, xAxis: berlinDayStart(h.date), itemStyle: { color: 'rgba(168,85,247,0.12)' } },
      { xAxis: berlinDayStart(h.date) + DAY },
    ]),
    ...props.events.map((e) => [
      { name: e.name, xAxis: new Date(e.starts_at).getTime(), itemStyle: { color: 'rgba(249,115,22,0.16)' } },
      { xAxis: new Date(e.ends_at ?? e.starts_at).getTime() + (e.ends_at ? 0 : 3_600_000) },
    ]),
  ]

  return {
    animation: false,
    grid: { left: 52, right: showWeather ? 52 : 16, top: 36, bottom: 56 },
    legend: { top: 0, right: 0, textStyle: { color: '#475569' } },
    tooltip: {
      trigger: 'axis',
      axisPointer: { type: 'cross' },
      formatter: (params: { axisValue: number; marker: string; seriesName: string; value: [number, number | null] }[]) => {
        const rows = params
          .filter((p) => p.value?.[1] != null)
          .map((p) => `${p.marker} ${p.seriesName}: <b>${Number(p.value[1]).toFixed(p.seriesName === 'Rain' ? 1 : 2)}</b>`)
        return `${fmtFull(params[0]?.axisValue ?? 0)}<br/>${rows.join('<br/>')}`
      },
    },
    dataZoom: [{ type: 'inside' }, { type: 'slider', height: 18, bottom: 8 }],
    xAxis: { type: 'time' },
    yAxis: [
      { type: 'value', name: props.normalized ? 'vs. p95' : props.seriesName, max: props.yMax, splitLine: { lineStyle: { color: '#e2e8f0' } } },
      { type: 'value', name: '°C', show: showWeather, splitLine: { show: false } },
      { type: 'value', name: 'mm', show: false, inverse: true, max: 20, min: 0 },
    ],
    series: [
      {
        name: props.seriesName,
        type: 'line',
        showSymbol: false,
        connectNulls: false,
        sampling: 'lttb',
        lineStyle: { width: 2, color: props.color },
        itemStyle: { color: props.color },
        areaStyle: { color: props.color, opacity: 0.08 },
        data: props.points.map((p) => [new Date(p.time).getTime(), val(p)]),
        markArea: areas.length ? { silent: false, label: { show: false }, data: areas } : undefined,
        z: 3,
      },
      ...(showWeather
        ? [
            {
              name: 'Temperature',
              type: 'line',
              yAxisIndex: 1,
              showSymbol: false,
              smooth: true,
              lineStyle: { width: 1.5, color: '#f97316', type: 'dashed' },
              itemStyle: { color: '#f97316' },
              data: props.weather.map((w) => [new Date(w.time).getTime(), w.temperature_c]),
            },
            {
              name: 'Rain',
              type: 'bar',
              yAxisIndex: 2,
              itemStyle: { color: 'rgba(14,165,233,0.55)' },
              barMaxWidth: 6,
              data: props.weather.map((w) => [new Date(w.time).getTime(), w.precipitation_mm ?? 0]),
            },
          ]
        : []),
    ],
  }
})
</script>

<template>
  <BaseChart :option="option" :height="height" :aria-label="`${seriesName} over time`" />
</template>
