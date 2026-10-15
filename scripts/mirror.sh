#!/bin/sh
# notes/p*.md → docs/p*.md，guide/handbook/*.md → docs/handbook/*.md
#（h1 提为 frontmatter title）。改完 notes 或 handbook 必跑。
cd "$(dirname "$0")/.." || exit 1
mirror() {
  src=$1
  dst=$2
  mkdir -p "$dst"
  for f in "$src"/*.md; do
    n=$(basename "$f" .md)
    title=$(head -1 "$f" | sed 's/^# //')
    { echo '---'; echo "title: $title"; echo '---'; echo ''; head -1 "$f"; echo ''; tail -n +2 "$f"; } > "$dst/$n.md"
  done
}
mirror notes docs
mirror guide/handbook docs/handbook
