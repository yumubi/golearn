# CROSS-MACHINE:在另一台电脑上继续审读批次

更新:2026-10-18。本机(MBA,路径 `/Users/akka/Dev/learning/go`)完成阶段一后交接。

## 两个仓库

| 仓库 | 内容 | 克隆位置 |
|---|---|---|
| `yumubi/golearn`(公开) | 手册正文、工具链、脚本 | 任意路径,下称 `$GO` |
| `yumubi/golearn-state`(私有) | 审读状态数据:`work/`(digest/原文提取/OCR/run 输出)+ `audit/`(台账/场景表/落盘快照) | 建议与主仓同级,下称 `$STATE` |

状态仓不依赖路径;主仓 clone 后把状态仓的 `work/`、`audit/` 拷(或软链)进主仓根目录:

```
git clone https://github.com/yumubi/golearn.git && cd golearn
git clone https://github.com/yumubi/golearn-state.git
ln -s ../golearn-state/work work
ln -s ../golearn-state/audit audit
```

(也可直接拷贝。注意两者都在主仓 .gitignore 里,不会误提交。)

## 新机器第一次配置

1. `node -v` ≥ 20、`go`、`python3`、eslint(pnpm install)。
2. `.omp/handbook/setup.js` 会按主仓绝对路径生成 `~/.omp/workflows/projects/go-<hash>/`。
   **run 记录不跨机器迁移**:旧 run 的 apply 回执已在 `audit/handbook-applied/` 和台账里,新批次从零跑即可。
3. `downloads/`(付费 PDF)不在任何仓库。需要重建 `work/text/` 时才需要;已提取文本都在状态仓,通常不需要。
4. 网络若需代理:`git -c http.proxy=socks5://127.0.0.1:<port> push`。

## 单批审读的完整流程(已在 19 批验证)

1. 选批:从 `audit/entry-ledger.jsonl` 挑同一单元/场景的 5-8 条,场景表(`audit/handbook-scenarios.jsonl`)里"待独立复核"的优先。已知欠账:SC-ledger 的 W16-2#K15/K18 从未进过任何批次。
2. 跑 `xd://workflow` 用 `.omp/handbook/batch.workflow`,target 指向 `guide/handbook/NN-*.md`,items 为条目 ID。
3. 双审通过(status=reviewed)后:`node .omp/handbook/apply.js <runId> --check`,再去掉 `--check` 落盘。
4. 台账回填:`audit/entry-ledger.jsonl` 里对应条目改 `处置=已落盘(双审通过)`、`runId`、`回执`;场景表用 `python3 audit/mirror-scen.py <runId> <sceneId> <注记>`。
5. 验收:`python3 -I scripts/check-handbook-refs.py` + `npx eslint guide/handbook/`,然后单独 commit(格式:`SC-xxx: N 节主题 Y 条双审落盘(要点)`);**push 前先 push 状态仓**。

## 纪律(前车之鉴,来自阶段一复盘)

- **双审结论只能由审稿 agent 产生**。禁止手工改 run JSON 的 status/review(阶段一在 mvc4wh0y 上越线一次,该批"双审通过"信号已不可信,若复翻 19 节建议重跑一批)。
- 台账/场景更新**先查再写**:mirror-scen.py 出现过两次张冠李戴(热榜写成 SC-pay、24 节写成不存在的 SC-dnsproxi),都是先写后查导致。
- 每批 commit 后如果台账也改了,状态仓同步 commit,两边 commit 信息互相引用 runId。

## 当前欠账快照(2026-10-18)

- 台账 3713 条:已落盘 117、有理由省略 182、待清点 591、待审 2823。
- 场景表:已落盘 11,待独立复核 12(auth/post/comment/cache/mq/tolerance/pay/ledger/search/disc-lb/deploy/lang)。
- `audit/phase1-report.md` 有各批 runId 与提交对照表。
