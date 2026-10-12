import type MarkdownIt from 'markdown-it'

// ```mermaid fence → <vitepress-mermaid> 组件（渲染交给 MermaidDiagram.vue）。
// 思路来自 vitepress discussion #4999：vitepress-plugin-mermaid 会把 dayjs
// 塞进 optimizeDeps 触发 hydration 全站崩，这里只动 fence 渲染器，零依赖。
export const mermaidPlugin = (md: MarkdownIt) => {
  const fence = md.renderer.rules.fence!

  md.renderer.rules.fence = (...args) => {
    const [tokens, idx] = args
    const token = tokens[idx]
    const lang = token.info.trim()

    if (lang === 'mermaid') {
      const code = token.content.trim()
      return `<vitepress-mermaid value="${md.utils.escapeHtml(code)}"></vitepress-mermaid>\n`
    }

    return fence(...args)
  }
}
