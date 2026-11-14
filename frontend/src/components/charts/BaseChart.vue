<script setup lang="ts">
import { onBeforeUnmount, onMounted, shallowRef, useTemplateRef, watch } from 'vue'
import { echarts, type EChartsOption } from '@/composables/useEChart'

const props = withDefaults(defineProps<{ option: EChartsOption; height?: string; ariaLabel?: string }>(), {
  height: '320px',
})

const el = useTemplateRef<HTMLDivElement>('el')
const chart = shallowRef<echarts.ECharts>()
let ro: ResizeObserver | undefined

onMounted(() => {
  chart.value = echarts.init(el.value!)
  chart.value.setOption(props.option)
  ro = new ResizeObserver(() => chart.value?.resize())
  ro.observe(el.value!)
})

watch(() => props.option, (o) => chart.value?.setOption(o, true))

onBeforeUnmount(() => {
  ro?.disconnect()
  chart.value?.dispose()
})
</script>

<template>
  <div ref="el" role="img" :aria-label="ariaLabel" :style="{ height }" class="w-full" />
</template>
