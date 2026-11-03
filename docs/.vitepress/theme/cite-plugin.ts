import type MarkdownIt from 'markdown-it'

// 引注标签视觉降权（方案 §5.9）：〔Wxx pN〕/〔TBxx pN〕/〔C:… pN〕 等页码标签
// 包进 <span class="cite-tag">，行内小灰字。标签语法本身不变——
// scripts/check-cite-pages.py 仍按原文匹配，任何批次不得把标签改写成脚注。
const CITE = /〔[^〕]*〕/g

export const citePlugin = (md: MarkdownIt) => {
  const inline = md.renderer.rules.inline!

  md.renderer.rules.inline = (...args) => {
    const [tokens, idx] = args
    const token = tokens[idx]
    if (token.type === 'inline' && CITE.test(token.content)) {
      CITE.lastIndex = 0
      // 只包标签本身，其余文本原样走默认渲染
      token.content = token.content.replace(CITE, (m) => `\uE000${m}\uE001`)
      const html = inline(...args)
      token.content = token.content.replace(/\uE000/g, '').replace(/\uE001/g, '')
      return html
        .replace(/\uE000/g, '<span class="cite-tag">')
        .replace(/\uE001/g, '</span>')
    }
    return inline(...args)
  }
}
