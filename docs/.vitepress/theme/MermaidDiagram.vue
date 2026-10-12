<script setup>
import mermaid from 'mermaid'
import { useData } from 'vitepress'
import { computed, nextTick, ref, watch } from 'vue'

defineProps({
  value: String,
})

const theme = computed(() => (useData().isDark.value ? 'dark' : 'default'))

const diagramRef = ref(null)
const fixedHeight = ref('auto')

watch(
  diagramRef,
  async () => {
    if (!diagramRef.value)
      return

    await nextTick()

    mermaid.initialize({ startOnLoad: false, theme: theme.value })
    diagramRef.value.removeAttribute('data-processed')

    try {
      await mermaid.run({ nodes: [diagramRef.value] })
      // 记住高度，主题切换重渲染时不跳版
      setTimeout(() => {
        if (diagramRef.value && diagramRef.value.offsetHeight > 0) {
          fixedHeight.value = `${diagramRef.value.offsetHeight}px`
        }
      }, 100)
    }
    catch (error) {
      console.error('Mermaid rendering error:', error)
    }
  },
  { immediate: true },
)
</script>

<template>
  <div :key="theme" ref="diagramRef" class="mermaid-diagram" :style="{ minHeight: fixedHeight }">
    {{ value }}
  </div>
</template>

<style scoped>
.mermaid-diagram {
  display: flex;
  justify-content: center;
  margin: 1rem 0;
  opacity: 0;
  transition: opacity 300ms ease;
}

.mermaid-diagram:has(> svg) {
  opacity: 1;
}
</style>
