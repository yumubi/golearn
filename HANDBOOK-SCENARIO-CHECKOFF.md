# HANDBOOK-SCENARIO-CHECKOFF

任务:GLM 手册全量审查与补全(提示词 `HANDBOOK-GLM-PROMPT.md`,工作流 `.omp/handbook/README.md`)。
台账:`audit/handbook-scenarios.jsonl`。工具链:`node .omp/handbook/check.js` 已 PASS。

## 进度

| # | 场景 | 节 | 状态 | runId | 提交 |
|---|---|---|---|---|---|
| 1 | SC-pay 支付打赏与回调 | 18 | 已落盘(双审 16/16,修订 1 轮;主流程批次 mvbx5s9b 补 K2/K3/K4/K6/K13/D2/D5 七条,3 轮双审通过,内容已在库) | handbook-batch-mvbtdj1z-1jtewp, handbook-batch-mvbx5s9b-se3nan | 26781e4 |
| 2 | SC-ledger 记账与转账 | 18/17 | 已落盘(oxz0hb needs_rework 三轮后手动补齐 K15 语义/K16 拆表/K19 幂等遗留,ledger2 双审增量合并;ab70c19) | handbook-batch-mvbv0ptk-oxz0hb, handbook-batch-mvc1p0zt-n28ogw | 5c5726e, ab70c19 |
| 3 | SC-migrate 迁移与调度 | 17 | 预审认为完整,待独立复核 | | |
| 4 | SC-smscode 验证码与短信 | 11/16 | 预审认为完整,待独立复核 | | |
| 5 | SC-auth 认证 | 11 | 预审认为完整,待独立复核 | | |
| 6 | SC-post 发帖与双库 | 10/14 | 已完整(ao0bef 双审 reviewed,after 与当前文件一致,无新增量;14 节 8 条已落盘 4f0dc6b) | handbook-batch-mvbv6wa4-ao0bef | 4f0dc6b |
| 7 | SC-comment 评论与计数 | 12/13 | 预审认为完整,待独立复核 | | |
| 8 | SC-cache 缓存 | 13 | 预审认为完整,待独立复核 | | |
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

## 续接记录

- 已完成:主控预审 23 个场景;4 个工作流批次(SC-pay/SC-follow/SC-rpc/SC-generics-ds+alias)全部双审通过并落盘,23/23 场景收口。
- 批次回执:SC-pay 26781e4(18 节)、SC-follow bf6bc40(21 节)、SC-rpc 1900419(11 节)、SC-generics adb431a(09 节,含二分推演修正 a9bd2f2)。
- 审稿闭环实绩:fidelity 各揪出 1 处(骨架归属、推演自相矛盾、口径外推),writer 修订轮修复;learner 全部 6/6 或 8/8。
- 阻塞:无。
