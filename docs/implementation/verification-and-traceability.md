# 验证、追踪与完成定义

状态：Proposed。本文把需求和验收场景映射到实施工作包及可复现证据。测试名称是建议的稳定语义名；落地时按仓库语言惯例调整路径，但不得丢失场景。

## 1. 验证层次

| 层次 | 目的 | 何时执行 | 失败处理 |
| --- | --- | --- | --- |
| 文档/契约 | OpenAPI 生成同步、本地引用、需求 ID、生成客户端一致 | 每次契约变更 | 阻止合入；先修源文件，不手改生成物 |
| 单元/属性 | 值对象、时间、状态机、DAG、规则和指标边界 | 每次代码变更 | 阻止合入 |
| 存储/适配器契约 | 每个存储/Provider 实现满足同一端口语义 | 适配器变更 | 阻止适配器交付 |
| 集成 | 数据库、文件、HTTP、Worker、生成客户端共同工作 | 相关模块变更 | 阻止阶段退出 |
| 故障/恢复 | 崩溃点、租约、取消竞争、磁盘/网络异常、备份恢复 | M0 起持续 | 阻止对应持久化能力交付 |
| 数值 oracle | PIT、因子、订单、成交、账本和指标对照 | M1/M2 | 阻止研究正确性声明 |
| 浏览器 | 真实服务上的成功/失败/断线/键盘/窄视口 | 每个纵向切片 | 阻止页面交付 |
| 性能/容量 | 代表性规模下吞吐、延迟、内存、磁盘和取消 | 决策门禁/版本发布 | 未达目标则限制规模或重新决策，不隐藏 |

## 2. 需求追踪矩阵

| 需求 | 主工作包 | 必须有的自动化/证据 | 阶段 |
| --- | --- | --- | --- |
| DATA-01 | M0-06, M1-01 | `ProviderContract_CapabilitiesAndUnsupported`、真实能力探针 | M0/M1 |
| DATA-02 | M0-03/06, M1-01 | schema form、secret 不回显/不落日志、连接版本测试 | M0/M1 |
| DATA-03 | M1-03 | `Ingestion_ForceFetchesWhenCurrent`、分页/限流/取消/重试 | M1 |
| DATA-04 | M0-06, M1-04 | 坏数据 fixture、规则版本、Issue 定位、发布判定 | M0/M1 |
| DATA-05 | M0-04, M1-05 | `Snapshot_PublishAtomicImmutable`、checksum、PIT 修订 | M0/M1 |
| DATA-06 | M1-06 | `Universe_HistoricalMembershipNoCurrentBackfill` | M1 |
| FACTOR-01 | M1-07 | registry 冲突、schema、输入/单位/lookback/PIT 元数据检查 | M1 |
| FACTOR-02 | M1-07 | `Factor_DetectsCycleBeforeRun`、缓存键/失败不发布 | M1 |
| FACTOR-03 | M1-08 | 分布/覆盖/IC/Rank IC/分组收益 oracle 与 null reason | M1 |
| FACTOR-04 | M1-07 | AST 白名单、类型/单位、文件/网络/任意代码拒绝 | M1 |
| STRAT-01 | M2-01 | 不可变版本、精确因子引用、假设/失败标准进入 manifest | M2 |
| STRAT-02 | M2-01/04 | 权重/持仓/换手/调仓约束和不支持能力拒绝 | M2 |
| BT-01 | M2-03 | `BacktestPreflight_ReturnsAllIssues`、提交时重新预检 | M2 |
| BT-02 | M2-07 | 固定事件序、逻辑时钟、逐层因果链、重跑一致 | M2 |
| BT-03 | M2-05 | 部分成交/撤单/流动性/不支持订单、同 Bar 歧义 | M2 |
| BT-04 | M2-06 | 手算 ledger oracle、公司行为、复权/现金分红不重复 | M2 |
| REPORT-01 | M2-08 | MetricPolicy oracle、完整 series、records、null reason | M2 |
| EXP-01 | M0-04, M2-07/09 | manifest 完整性、build/config/snapshot/checksum 固定 | M2 |
| EXP-02 | M2-09 | 新 Run/source_run_id、兼容对比、不覆盖原实验 | M2 |
| VALID-01 | M1-07/08, M2-08 | `Validation_TrainingFitCannotReadFutureSplit`、测试集使用历史 | M2/M3 |
| VALID-02 | M3 后续工作包 | 参数/成本/阶段/滚动任务全量尝试 manifest | M3 |
| JOB-01 | M0-05 | 幂等、租约/fencing、取消竞争、SSE 重放、重启恢复 | M0 |
| UI-01 | M0-07/08, M1-09, M2-10 | 跨页面浏览器闭环与所有统一状态 | M0-M2 |
| INT-01 | M0-03/06, M1-07, M2-01/02/09 | Provider/Factor/Strategy/Model/Store/Exporter 契约套件 | M0-M2 |

## 3. 验收 ID 可执行化

| 验收 ID | 测试安排 | 通过判据 |
| --- | --- | --- |
| AC-01 | 先完成 incremental 到最新，再提交 force；Provider spy/真实测试环境记录请求 | 确实调用上游，产生新 request/batch 证据；相同内容可去重但不丢请求事实 |
| AC-02 | 注入重复、错误单位、缺交易日；尝试严格发布 | Issue 定位到字段/范围，严格 policy 拒绝；无静默填零/删行 |
| AC-03 | 建立 as_of 前后两个财务/行情修订，修改未来版本后重跑过去决策 | 过去因子、决策、订单和账本不变；未来 as_of 可见新修订 |
| AC-04 | 历史成分含已退市/移除标的，当前名单不含 | 对应生效区间仍解析进入；区间外不进入 |
| AC-05 | 快照有价格无历史估值，分别运行价格与 PE 因子 | 价格成功；PE 预检返回精确字段/历史错误且不创建 Run |
| AC-06 | 配置收盘信号同价成交、同 Bar 双触发 fixture | 非法配置被拒；允许模型按固定顺序且 reason/版本可见 |
| AC-07 | 小样本逐笔买卖、费用、部分成交、公司行为、估值 | 每步现金/数量/权益与手算一致，journal 守恒且重复 Fill 不双记 |
| AC-08 | 固定所有引用/seed 执行两次 | Decision/Order/Fill/Ledger 逐项一致；统计在 policy 容差内 |
| AC-09 | 同键并发提交、取消/成功竞争、断线、Worker 崩溃/旧 token | 单一 Job/终态/结果；SSE 恢复；旧 Worker 不能发布 |
| AC-10 | 真实浏览器仅通过 UI 完成全流程，并分别注入失败与重试 | 无 CLI/手拼 JSON；上下文版本、错误和重试链完整 |
| AC-11 | 在测试段放置影响标准化/填充的极值并运行训练 | 训练拟合参数不变；测试使用记录不可删除或重命名重置 |
| AC-12 | 仅新增适配器与映射并运行契约套件 | 应用/领域无供应商私有字段依赖，注册后可被同一流程使用 |

## 4. CI 与本地验证入口

实际脚本在 M0-01 创建后固定；建议公开以下语义入口，Windows 与 CI 使用同一底层命令：

| 入口 | 内容 |
| --- | --- |
| `verify-contract` | 生成 OpenAPI、运行 `check_contract.py`、生成客户端、检查同步和需求/验收 ID |
| `verify-go` | gofmt 检查、go vet、单元/属性、race（支持平台）、build |
| `verify-web` | 格式、lint、TypeScript strict、unit/component、production build |
| `verify-integration` | 临时数据根、迁移、HTTP/Worker/文件、Provider/Store 契约 |
| `verify-e2e` | 启动真实服务与浏览器，运行当前阶段纵向路径 |
| `verify-recovery` | failpoint、租约/fencing、取消、备份/恢复、checksum |

脚本必须创建受控临时目录并在输出中显示实际配置，不能使用开发者生产数据目录。Race 只在目标 Go/OS/CGO/依赖组合确实可用时计为通过；不支持或失败标为 `Blocked`，不能被普通测试通过替代。

## 5. 测试数据与 oracle 管理

- `testdata/synthetic/<version>/`：市场无关分页、修订、坏数据和取消 fixture。
- `testdata/pit/<case>/`：记录事件/公告/可用/采集时间与预期可见版本。
- `testdata/ledger/<market-model-version>/`：经 DEC-06 批准的手算输入和逐步期望。
- `testdata/http/`：OpenAPI 成功/错误响应，不含 secret 或真实账号数据。

fixture 带 README、schema version、来源/生成方法、许可和 checksum。真实供应商响应必须脱敏并确认可进入仓库；否则保存生成的最小等价 fixture 和外部证据摘要。

## 6. 性能与容量证据

DEC-03 决定目标数值。基准至少分开测：拉取/标准化 rows/s、snapshot 发布、PIT 查询 p50/p95、因子吞吐与峰值内存、回测 events/s、数据库写等待、WAL/checkpoint（如用 SQLite）、artifact 吞吐、SSE 连接数和浏览器大表/图表交互。

报告记录硬件、OS、Go/Node/数据库版本、数据形态、并发、冷热缓存和文件系统。未达到团队多 Worker 目标时，不能用单机均值证明可部署；应限制支持范围或回到 DEC-03。

## 7. 阶段完成记录模板

每个阶段在 `docs/verification/` 保存一份记录：

```markdown
# <阶段> 验证记录

- 代码版本 / build hash：
- 契约与迁移版本：
- 环境：OS、toolchain、数据库、浏览器
- 已运行命令与结果：
- AC/需求覆盖：
- 恢复/备份演练：
- 性能基准：
- Not Run / Blocked：原因、风险、解除条件
- 已知限制与后续工作：
```

证据文件引用原始机器可读报告，不复制含敏感信息的日志。`docs/verification.md` 保留为当前仓库总体状态入口，随着代码实施更新，不把旧的文档验证误写为业务通过。

## 8. Definition of Done

一个工作包只有同时满足以下条件才为 Done：

- 行为与需求/契约一致，关键歧义有 ADR，不存在隐式市场默认。
- 正常、边界、失败、取消/恢复和权限场景有与风险匹配的测试。
- 公共 API、生成物、迁移、配置、运维/用户文档同步。
- 最终差异无 secret、调试代码、未解释 placeholder 或意外生成物。
- 对应验证命令已实际运行并记录；未验证内容明确标识。
- 产生长期有效的架构、边界、依赖或坑点时同步项目根 `MEMORY.md`。

