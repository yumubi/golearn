# 手册审读工作流

在仓库根目录启动 omp，使用已注册的 `/handbook-batch`。扩展安装在用户插件目录；本目录与 `.omp/agents/` 保存项目工作流和角色，`.omp/config.yml` 关闭自动摘要并限制原生任务并发。

本项目只自动加载 workflow-authoring 与 workflow-patterns 两个技能，减少无关技能描述进入精读上下文。其他项目的技能配置不受影响。完整提示词中的手册写作规范仍然适用。

四个角色均使用当前配置的 GLM，分别拥有独立上下文：原文审读员、候选稿作者、忠实性审稿员、手册可用性审稿员。前三者只能读取文件；可用性审稿员没有文件工具，只得到候选稿和验收问题。共享 store 已禁用，两个审稿人并行运行，不互看报告。独立角色不等于跨模型验证。

作者只返回 before/after 提案。工作流逐项检查完整性并要求两个审稿人全部通过，失败最多返工两次。运行正常结束也可能得到 blocked 或 needs_rework，这些结果不能落盘。结构检查不证明语义一定正确，仍要检查原文证据和实际阅读记录。

## 启动和使用

首次安装或修改 batch.workflow 后执行 `node .omp/handbook/setup.js`，然后重新启动 omp。生成的 `.omp/workflows/saved/handbook-batch.json` 已忽略，不手动修改。新机器还需安装 omp-dynamic-workflows，并指定可用模型；模型与过程记录保存在机器本地，不入库。

本机安装的是扩展 0.1.0，来源提交 421f4bb。它的懒加载桥接在 omp 18.11.1 独立二进制中会加载第二套 SDK，触发 pi-natives 解析失败。setup 会应用本目录的兼容桥接，改用 Bun YAML 与宿主注入的 schema 校验器，保留原文件备份。补丁只接受已核对的版本和原文件哈希；插件升级后若检查失败，需要重新评估，不能强行覆盖。没有修改 omp 核心或模型凭据。

先检查 `work/text/` 和 `work/digest/` 可读，再选 1-8 个相关条目。生成工具调用参数：

```sh
node .omp/handbook/prepare.js guide/handbook/18-consistency.md W15-2#K8,W15-2#K10 支付消息发送失败与重复消费
```

让主 agent 把输出对象原样交给 `workflow` 工具。也可从输出取得 revision 后执行 `/handbook-batch target=guide/handbook/18-consistency.md items=W15-2#K8,W15-2#K10 revision=<SHA256> 支付消息发送失败与重复消费`。

通过 `/workflows` 查看状态，`/workflows web` 打开本地控制台。完整运行结果和子会话保存在 `~/.omp/workflows/` 与 omp 会话目录。不同批次必须串行落盘；同一文件变化后，旧 revision 的提案不能应用。

只有结果 status=reviewed 才可执行：

```sh
node .omp/handbook/apply.js --check RUN_ID
node .omp/handbook/apply.js RUN_ID
```

apply 直接读取扩展保存的运行记录，重新核对工作流定义、任务条目、双审证据、目标文件 SHA-256 和唯一替换区块。它只写 guide/handbook 下的指定章节，并把修改前备份和应用回执放在忽略目录 audit/handbook-applied/。这是正常工作流的防误用检查，不是抵抗能修改本机文件的恶意 agent 的安全沙箱。

应用后由主 agent 执行示例验证、引用守卫、时长更新和镜像同步，最后单独运行文档构建。全部结果通过才更新台账并提交。提示词入口见仓库根目录 HANDBOOK-GLM-PROMPT.md。

## 校验与边界

运行 `node .omp/handbook/check.js` 检查缺项拒绝、双审失败、返工上限、目标范围与旧版本拒绝。这是确定性门禁测试，不是 GLM 内容质量评测。`node .omp/handbook/smoke.js` 会实际调用 GLM 精读 W16-1 K2 并完成候选稿和双审，核对手册未被修改；它有十分钟的诊断等待上限，不是正式任务的执行时限。

加 `--fixture` 会在临时目录建立明确标为测试样本的小材料，执行同一工作流和四个真实 GLM 会话，并验证结果可通过落盘前检查。本次隔离样本全链路通过；真实长章节试跑曾超出诊断等待上限，没有算作通过。全书内容质量和长任务稳定性仍需分批台账及回归案例验收。

新 CLI 文件的 unused-variable 检查采用 ESLint 核心规则。当前 unused-imports 规则在 ESLint 10 下会把 `const x = 1; console.error(x)` 也误报成 type-only usage；替代规则已经用已使用与未使用两个样本核对，其余目录的检查规则不变。

目前不使用 worktree：原材料未入库，而且候选作者无写权限，直接共享原文只读访问即可。每轮审稿都是新会话。发现原文图表或代码缺失时，主 agent 提取 PDF 到 audit，再明确补充读取位置并重新运行，不能让作者猜测。

任务参数、源码与候选文本会保存在本机审计记录中；按已有仓库规则处理，不把原始 PDF、凭据和会话日志提交到 Git。
