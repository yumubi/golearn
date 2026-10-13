# vendored/：精读章的外部源码快照

P4/P5 精读章引用的两个外部仓库，快照进本仓库让读者 clone 即可跟读
（上游不在 GitHub 的 ejdb 若自建站不可达，教程就断了）。行号引用与
本快照对齐；上游演化后可能漂移，以快照为准。

| 目录 | 上游 | 快照 commit | 许可 |
|---|---|---|---|
| `ejdb/` | https://forgejo.gulugulu.moe/clark/ejdb （ClarkQAQ/ejdb） | `8be48bb` | 无 LICENSE 文件，已向作者确认意向后快照；仅作教程精读使用 |
| `deepsearch/` | https://github.com/ClarkQAQ/deepsearch | `b7157af` | MIT（见 deepsearch/LICENSE） |

快照时清理：`.git/`、测试二进制（*.test）、运行期 data/ 目录。
