#!/bin/sh
# notes/p*.md → docs/p*.md 镜像（h1 提为 frontmatter title）。改完 notes 必跑。
cd "$(dirname "$0")/.." || exit 1
for f in notes/p*.md; do
  n=$(basename "$f" .md)
  title=$(head -1 "$f" | sed 's/^# //')
  { echo '---'; echo "title: $title"; echo '---'; echo ''; head -1 "$f"; echo ''; tail -n +2 "$f"; } > "docs/$n.md"
done
