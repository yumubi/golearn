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
      // svg 的 width=100% 会把超宽图压进容器（390px 屏上 16px 字变 2px）。
      // 改成图自然宽（max-width 的值），容器 overflow-x 负责横向滚动。
      if (diagramRef.value.querySelector('svg')) {
        diagramRef.value.querySelector('svg').setAttribute('width', diagramRef.value.querySelector('svg').style.maxWidth || '100%')
      }
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

/* 超宽图（如 2456px 的弧线图）：不缩成 2px 文字，横向滚动读。
   mermaid 会给 svg 写 style="max-width: <图宽>px"，超过容器即溢出。 */
.mermaid-diagram:has(> svg[style*="max-width"]) {
  display: block;
  overflow-x: auto;
  padding-bottom: 0.5rem;
}

.mermaid-diagram :deep(svg) {
  margin: 0 auto;
}
</style>
