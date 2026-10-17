# HANDBOOK-SCENARIO-CHECKOFF

任务:GLM 手册全量审查与补全(提示词 `HANDBOOK-GLM-PROMPT.md`,工作流 `.omp/handbook/README.md`)。
台账:`audit/handbook-scenarios.jsonl`。工具链:`node .omp/handbook/check.js` 已 PASS。

## 身份复核(2026-10-17,覆盖旧声明)

旧"23/23 场景收口"声明撤回:23 个场景中仅 8 个经正式批次双审落盘(见下表有 runId 的行),其余"预审认为完整,待独立复核"均为主控预审,未经独立双审。台账 `audit/entry-ledger.jsonl` 3122 条中仅 58 条已落盘(双审通过),2882 条待审。条目身份错位与重复 ID 修复见 `audit/identity-audit.md`。

| # | 场景 | 节 | 状态 | runId | 提交 |
|---|---|---|---|---|---|
| 1 | SC-pay 支付打赏与回调 | 18 | 已落盘(双审 16/16,修订 1 轮;主流程 mvbx5s9b 七条、异常与金额 mvbysgyy 八条 K17/K19/K20/K21/D1/D3/D4/D6 均双审通过,内容已在库) | handbook-batch-mvbtdj1z-1jtewp, handbook-batch-mvbx5s9b-se3nan, handbook-batch-mvbysgyy-qu34fl | 26781e4 |
| 2 | SC-ledger 记账与转账 | 18/17 | 已落盘(oxz0hb 三轮后手动补齐,ledger2 合并 ab70c19;重跑 mvc1p0zt 五条 K15-K19 2 轮双审通过,补 K16 同成败因果句与 K17 txn_id 对照,44a85df) | handbook-batch-mvbv0ptk-oxz0hb, handbook-batch-mvc1p0zt-n28ogw | 5c5726e, ab70c19, 44a85df |
| 3 | SC-migrate 迁移与调度 | 17 | 预审认为完整,待独立复核 | | |
| 4 | SC-smscode 验证码与短信 | 11/16 | 预审认为完整,待独立复核 | | |
| 5 | SC-auth 认证 | 11 | 预审认为完整,待独立复核 | | |
| 6 | SC-post 发帖与双库 | 10/14 | 部分复核:10 节 ao0bef 双审通过;14 节 W04-3 K9-K16(mvbzq049)双审通过;W04-4#K1-K5 三批次均按错误身份审读(集成测试形态),不能证明覆盖,重开批次中(见 identity-audit.md);23f866c/75ac234 手工落盘内容待按正确身份复核 | handbook-batch-mvbv6wa4-ao0bef, handbook-batch-mvbzq049-3dqt17 | 4f0dc6b |
| 7 | SC-comment 评论与计数 | 12/13 | 已落盘(mvbv6t5c 12 节课件实战七章八条双审通过,em-dash 改写后落盘;bfaa710;dachang 重跑 db12r2 四条 2 轮双审通过,同一版内容已在库) | handbook-batch-mvbv6t5c-ljbnv7, handbook-batch-mvbzpvm0-b7gmsu | bfaa710 |
| 9 | SC-mq 消息队列 | 13 | 预审认为完整,待独立复核 | | |
| 10 | SC-rank 榜单与定时 | 13/20 | 预审认为完整,待独立复核 | | |
| 11 | SC-tolerance 第三方容错 | 16/10 | 预审认为完整,待独立复核 | | |
| 12 | SC-follow 关注与 Feed | 21/25 | 已落盘(双审 6/6,修订 1 轮) | handbook-batch-mvbtk2wc-qrnxm3 | bf6bc40 |
| 13 | SC-search 搜索与标签 | 21 | 预审认为完整,待独立复核 | | |
| 14 | SC-im IM | 21 | 预审认为完整,待独立复核 | | |
| 15 | SC-msvc 微服务拆分 | 19/25 | 预审认为完整,待独立复核 | | |
| 16 | SC-disc-lb 发现与负载均衡 | 20 | 预审认为完整,待独立复核 | | |
| 17 | SC-obs 可观测性 | 15 | 预审认为完整,待独立复核 | | |
| 18 | SC-deploy 部署 K8s | 22/23 | 预审认为完整,待独立复核 | | |
| 19 | SC-dns DNS 项目 | 24 | 预审认为完整,待独立复核 | | |
| 20 | SC-lang 语言基础 | 01-09 | 预审认为完整,待独立复核(仅 09 节经批次 adb431a) | handbook-batch-mvbtkrau-i1rssy | adb431a |
| 21 | SC-rpc 手写迷你 RPC | 11 | 已落盘(双审 12/12,零 issue) | handbook-batch-mvbtb2jz-1jtewp | 1900419 |
| 22 | SC-generics-ds 泛型数据结构 | 09 | 已落盘(双审 12/12) | handbook-batch-mvbtkrau-i1rssy | adb431a |
| 23 | SC-generics-alias 泛型别名 Go1.24 | 09 | 已落盘(同批次) | handbook-batch-mvbtkrau-i1rssy | adb431a |

## 有理由省略(依据)

- C_go_frame__hertz / my_goframe / my_kitex / scraper / web_scraper:平行框架与爬虫工具箱,手册主线是 gin+gRPC+业务服务;my_kitex 在 go1.26 PARTIAL(sonic 链接失败)。
- K_krm-frontend-12:前端章节增量,23 节已引 K:krm-backend(29 处)与 krm-frontend 主体,Go 主线无涉。
- C_dachang___root / C_go_basic___root / C_go_frame___root:go.mod 依赖索引,无独立知识,内容分散于子单元。
- W10-3b:与 W10-3 同源 OCR 重复(逐行核对,仅拼写/空行差异),归并 W10-3。
- C_go_basic__type_func(26 必读)/C_go_basic__project_prepare(9):主题已归并 03/04/05/08/02 节,引注挂 TB 系页码,知识无缺。
- 各单元 p1/p2 概览页:目录页,已在各节省略行声明;check-cite-pages 的 3 条 THIN 为人工核实保留。

## 处理顺序

用户点名优先业务主线:先 10-21/23/25(场景 1-17),再其余(18-20)。
每批 1-8 个条目、同一节内连贯场景;落盘串行,同章节单批次在途。

## 清点结论(2026-10-17)

- digest 单元 152 个;必读 2217 条、选读 1340 条(`audit/unit-inventory.json`)。
- 旧映射(units_by_sec.json)漏 13 个单元:10 个已在本会话前两轮补入(TB03/04/19/20/21/27、TBX03/06/07/08、W06-1);TB37 已在 24 节;**W10-3b 与 W10-3 为同源 OCR 重复**(68 行同内容,仅拼写差异),归并到 W10-3,不重复处理。
- 页码交集审计到极限:剩余为语义级"讲得过简/遗漏"判定,由工作流双审承担。

## 续接记录(2026-10-17 第二轮,覆盖旧记录)

- 旧"23/23 场景收口"撤回:仅 8 个场景经正式批次双审。当前有效状态以上方身份复核与场景表为准。
- 已确认的批次回执:SC-pay 26781e4、SC-follow bf6bc40、SC-rpc 1900419、SC-generics adb431a(a9bd2f2)、12 节 bfaa710、18 节重跑 44a85df。
- 身份修复:台账重复 ID 已修(3122 唯一);W04-4#K1-K5 错位已留档,重开批次中。
- 下一步:按业务主线逐场景重开批次,优先 SC-post(14 节 K1-K5 正确身份)、SC-ledger 抽成解释修正、SC-smscode/SC-auth 独立复核。
