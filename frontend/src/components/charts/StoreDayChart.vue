<script setup lang="ts">
import { computed } from 'vue'
import BaseChart from './BaseChart.vue'
import type { EChartsOption } from '@/composables/useEChart'
import type { DashHour } from '@/api/stores'
import { AREA, IN_STORE, IN_STORE_FORECAST, SOURCE, hh, whyLines } from '@/lib/crowd'

const props = defineProps<{ hours: DashHour[]; capacity: number; nowHour?: number | null; height?: string }>()

const option = computed<EChartsOption>(() => {
  const open = props.hours.filter((h) => h.open)
  const shown = open.length ? open : props.hours.filter((h) => h.hour >= 7 && h.hour < 22)
  const labels = shown.map((h) => hh(h.hour))
  const counted = shown.map((h) => (h.source === 'counted' ? h.occupancy : null))
  const forecast = shown.map((h) => (h.open && h.source !== 'counted' ? h.occupancy : null))
  const area = shown.map((h) => Math.round(h.passers_by))
  const areaActual = shown.map((h) => (h.area_source === 'actual' ? Math.round(h.passers_by) : null))
  const nowIdx = props.nowHour == null ? -1 : shown.findIndex((h) => h.hour === props.nowHour)
  // shaded bands for hours with an event at the store
  const eventBands: { name: string; from: string; to: string }[] = []
  shown.forEach((h, i) => {
    const ev = h.why?.event
    const last = eventBands[eventBands.length - 1]
    if (!ev) return
    if (last && last.name === ev && labels[i - 1] === last.to) last.to = labels[i]!
    else eventBands.push({ name: ev, from: labels[i]!, to: labels[i]! })
  })
  const yMax = Math.max(props.capacity * 1.1, ...shown.map((h) => h.occupancy_high ?? h.occupancy))
  return {
    animation: false,
    grid: { left: 44, right: 56, top: 28, bottom: 52 },
    legend: {
      bottom: 0,
      data: ['In store (door counter)', 'In store (forecast)', 'Passers-by / h'],
      textStyle: { color: '#3d4250' },
    },
    tooltip: {
      trigger: 'axis',
      formatter: (ps: { dataIndex: number }[]) => {
        const h = shown[ps[0]?.dataIndex ?? 0]
        if (!h) return ''
        const band = h.occupancy_low != null ? ` <span style="color:#64748b">(${h.occupancy_low}–${h.occupancy_high})</span>` : ''
        return [
          `<b>${hh(h.hour)}</b>`,
          h.open ? `In store: <b>${h.occupancy}</b>${band} · ${SOURCE[h.source]}` : 'Closed',
          h.open ? `Entries: ${h.entries} · suggested cashiers: ${h.staff}` : '',
          `Passers-by: ${Math.round(h.passers_by)}/h <span style="color:#64748b">(${h.area_source})</span>`,
          ...whyLines(h.why).map((l) => `<span style="color:#475569">${l}</span>`),
        ]
          .filter(Boolean)
          .join('<br/>')
      },
    },
    xAxis: { type: 'category', data: labels, axisTick: { alignWithLabel: true }, axisLabel: { color: '#5b6170' } },
    yAxis: [
      { type: 'value', name: 'People inside', max: Math.ceil(yMax / 10) * 10, nameTextStyle: { color: '#5b6170', align: 'left' }, axisLabel: { color: '#5b6170' } },
      { type: 'value', name: 'Passers-by / h', splitLine: { show: false }, nameTextStyle: { color: AREA, align: 'right' }, axisLabel: { color: AREA } },
    ],
    series: [
      {
        name: 'In store (door counter)',
        type: 'bar',
        stack: 'store',
        data: counted,
        itemStyle: { color: IN_STORE, borderRadius: [5, 5, 0, 0] },
        barMaxWidth: 36,
        markLine: {
          symbol: 'none',
          silent: true,
          data: [
            { yAxis: props.capacity, label: { formatter: `Capacity ${props.capacity}`, position: 'insideEndTop', color: '#8a3a00', fontWeight: 600 }, lineStyle: { color: '#8a3a00', type: 'solid', width: 1.5 } },
            { yAxis: Math.round(props.capacity * 0.7), label: { formatter: 'Busy · 70%', position: 'insideStartTop', color: '#5b6170' }, lineStyle: { color: '#a8aeba', type: 'dashed' } },
          ],
        },
        markArea:
          nowIdx >= 0
            ? { silent: true, itemStyle: { color: 'rgba(20,22,26,0.05)' }, label: { show: true, position: 'insideTop', formatter: 'Now', color: '#14161a', fontWeight: 600 }, data: [[{ xAxis: labels[nowIdx] }, { xAxis: labels[nowIdx] }]] }
            : undefined,
      },
      {
        name: 'In store (forecast)',
        type: 'bar',
        stack: 'store',
        data: forecast,
        itemStyle: { color: IN_STORE_FORECAST, borderColor: IN_STORE, borderType: 'dashed', borderWidth: 1, borderRadius: [5, 5, 0, 0] },
        barMaxWidth: 36,
      },
      {
        name: 'Passers-by / h',
        type: 'line',
        yAxisIndex: 1,
        data: area,
        symbol: 'none',
        smooth: true,
        lineStyle: { color: AREA, width: 2, type: 'dashed' },
        itemStyle: { color: AREA },
        z: 3,
        markArea: eventBands.length
          ? {
              silent: true,
              itemStyle: { color: 'rgba(180,80,10,0.08)' },
              label: { show: true, position: 'insideTop', color: '#8a3a00', fontWeight: 600, distance: 18 },
              data: eventBands.map((b) => [{ name: b.name, xAxis: b.from }, { xAxis: b.to }]),
            }
          : undefined,
      },
      {
        name: 'Passers-by / h',
        type: 'line',
        yAxisIndex: 1,
        data: areaActual,
        symbol: 'none',
        smooth: true,
        lineStyle: { color: AREA, width: 3 },
        itemStyle: { color: AREA },
        tooltip: { show: false },
        z: 4,
      },
    ],
  }
})
</script>

<template>
  <BaseChart :option="option" :height="height ?? '340px'" aria-label="People in the store and passers-by outside, by hour" />
</template>
