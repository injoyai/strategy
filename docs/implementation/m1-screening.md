# M1S 选股功能实施

版本：0.6，2026-09-14。状态：S1-01..S1-04 契约与纯规则已落地；S2 已分五次落地——(1) ScreenerVersion 的 wire 模型与持久化：`internal/screening/wire.go` 按契约 oneOf 逐分支编解码（range 恒发 null 边界、factor binding 恒发 params、缺 factor_ref 拒绝解码），`screener_versions` 以 (workspace, id, version) 为主键、`id` 是方案身份而 `version` 是不可变修订，`parent_id` 追加 `v<N+1>`（修订号在写事务内推导），`definition_hash` 只覆盖规则契约（不含 name/description/parent_id，与 universe version 同规则），`GET /screeners/{id}` 补齐 required `version` query（与 `/factors/:id` 一致），端点 `/screeners`（列表/创建）与 `/screeners/{id}` 已实现；(2) ScreenRun preflight：新增 `internal/screenrun` 编排层（不放进 `internal/screening`，因为 `ports` 已导入 `screening`，反向导入会成环），`POST /screen-runs/preflight` 解析冻结的 screener/universe 版本、要求 universe 的 `definition_hash` 作为不可变 pin、校验快照绑定与 `strict_pit`（要求快照本身严格时才算满足）、在绑定快照的视图上于 as_of 解析母池、做结构性规则校验，并按绑定给出 coverage——factor 绑定校验注册、参数规范化、**按数据推导窗口**并跑因子引擎 preflight（图/PIT/逐输入可用性/陈旧），field 绑定在无法解析时显式报告 `screenrun.catalog_unavailable`（warning 级）；`estimated_scan_rows`/`estimated_rows` 由「成员数 × 推导窗口内日期数」与「成员数」得出；(3) 窗口推导原语 `DataView.RecentEventTimes`：取 `as_of` 之前最近的 N 个不重复事件时间，把「声明的回看期数」用快照自身的历史折算成窗口起点，而不是把期数换成未经证实的时间跨度；历史不足即报 `insufficient_history`（error 级），不会静默缩短窗口。**尚未实现**：Run/rows/explanations 存储与查询、静态池与导出、S3/S4。另有一处引擎 API 空缺：SC-AC-01 的“单位可比性”目前无钩子（`screening.InputTypes` 只到 `ValueKind`，`checkLiteral` 比对 kind 而非 unit），数据集目录已提供单位，要落地需给引擎补 unit 维度。(4) 数据集目录：`batches.declaration_json`（migration 00008）持久化摄取声明的字段单位与 availability policy，`GET /datasets`（分页）与 `GET /datasets/{id}` 由持久状态聚合出声明字段、观测类型、schema_version、coverage 与质量发现（未声明字段 unit 为空、声明未观测字段 type 为新增枚举 `unknown`），同时修掉契约与前端长期调用但服务端从未挂载的 `/datasets` 404；test 描述：`internal/data/dataset_test.go`、`internal/server/datasets_test.go`。(5) field 绑定校验：preflight 用该目录构建 `screening.InputTypes` 并传入 `Validate`，条件的 operator/字面量 kind 被真正比对（布尔阈值比小数字段 → `screening.literal_kind_mismatch`），dataset/字段未知或声明未观测分别报 `screenrun.dataset_unknown`/`field_unknown`/`field_unobserved`（error 级）；factor 绑定在 catalog 中登记为空 kind（因子 spec 只声明 unit 不声明 value kind），使其 kind 检查被显式跳过而非假定。设计来源为 [选股功能设计](../stock-screening-design.md)。SCREEN-01..08 与 SC-AC-01..12 已进入主需求与 OpenAPI；其余端点、Go/TS handler 与页面尚未实现，本文件定义接入步骤，不表示功能已经存在。

## 1. 目标与非目标

交付独立选股工作台：冻结 `ScreenerVersion + Snapshot + UniverseVersion + as_of`，执行有类型条件、排序或评分，保存不可变 ScreenRun、名单与逐条件解释，并可导出或生成带来源证据的静态 UniverseVersion。

明确不做：

- 选股结果不生成订单、仓位、收益或交易建议。
- 不另建 PE、动量、波动等公式；复用 DataView 与 FactorEngine。
- 不接受 SQL、Go、JavaScript 或任意表达式执行。
- 不把当前分页、搜索结果当作完整入选名单。
- 不把 t 时点保存的静态名单用于 t 之前的策略决策。
- 首期不做定时扫描、实时订阅、提醒、行业配额或动态策略 Universe。

## 2. 依赖、门禁与可并行范围

| 切片 | 可开始条件 | 阻塞条件 |
| --- | --- | --- |
| S1 契约与纯规则 | M0 领域基元、错误、契约生成链已稳定 | 共享分页/幂等语义尚未统一时，不加入新 handler |
| S2 数据与任务 | Snapshot/DataView、UniverseResolver、FactorEngine、Job/Artifact 可用 | M1-05..07 未完成 |
| S3 页面闭环 | 业务 API 和生成 TS 类型通过契约测试 | 不允许用 mock 数据宣称闭环完成 |
| S4 静态池衔接 | Universe 持久化与来源元信息已实现 | 回测时点校验未完成时不得宣称成果可安全回测 |
| 真实市场验收 | DEC-01/02/03 与首批字段/规模批准 | 不得用示例市场、今日估值或任意供应商默认值替代 |

`GATE-SCREEN-CONTRACT` 先统一 [决策与门禁](decisions-and-gates.md) 中的共享契约差异，并将 SCREEN-01..08 纳入主需求。选股首期不新增生产依赖；若全局排序需要外部排序库或新文件格式，先用代表性规模验证并走 ADR。

## 3. 领域模型与包边界

建议包：

```text
internal/screening/
  definition.go       ScreenerVersion、InputBinding、ConditionTree、Ranking、Selection
  validate.go         类型、单位、操作符、复杂度与依赖预检
  truth.go            true/false/unknown 三值逻辑
  ranking.go          稳定多字段排序与百分位评分
  engine.go           母池、字段/因子、解释、汇总的编排
  explain.go          节点证据与互斥汇总阶段
internal/ports/
  screening.go        Registry、RunStore、ResultReader、OutputService 端口
internal/store/
  screening_*.go      SQLite 版本/Run/结果索引适配器
internal/server/
  screeners.go        生成契约对应 handler 与映射
```

传输 DTO、领域定义和存储记录分别建模。`ScreenCondition` 是选股专用 schema，不直接扩大既有 Strategy Expression 权限。所有 ID/ref 经过工作区与精确版本校验。

主要聚合：

| 聚合 | 不可变内容 | 可变内容 |
| --- | --- | --- |
| ScreenerVersion | parent、schema version、bindings、condition tree、ranking、selection、display columns | 无；修改创建新版本 |
| ScreenRun | 冻结 request、job、engine/build/policy、config/snapshot hash、source_run_id | 仅执行状态通过关联 Job 演进；终态结果不覆盖 |
| ScreenResult | summary、正式 rank/score、row/explanation artifact、quality limits | 无 |
| Derived UniverseVersion | 完整入选成员、source_screen_run_id、selection as_of、snapshot hash、quality limits | 无 |

## 4. S1 契约接入

### S1-01 主需求与 OpenAPI

先将 SCREEN-01..08 和 SC-AC-01..12 纳入正式需求/追踪，再修改 `docs/api/build_openapi.py`。一次性定义并生成：

- `ScreenerCreate`、`Screener`、`ScreenInput`、`ScreenCondition`、`ScreenRanking`、`ScreenSelection`。
- `ScreenRunCreate`、`ScreenPreflight`、`ScreenRun`、`ScreenSummary`、`ScreenRowPage`、`ScreenExplanation`。
- `/screeners`、`/screen-runs`、preflight、rows、explanation、universe 与 exports 端点。
- 每个 202 响应的 Location、写操作 Idempotency-Key、标准 Error/Issue、分页与权限。

`Job.result_refs` 若新增 `screen_run`，同步生成器、JSON、Go 映射、数据库约束、TypeScript 和旧消费者未知枚举测试。首期不扩展 Experiment.kind；ScreenRun 单独保存。选股导出使用独立入口，底层复用 Job/Artifact，不静默扩大既有 `/exports` 枚举。

### S1-02 输入目录

页面可用输入由服务端目录返回，每项包含 field/factor binding ID、类型、单位、频率、资产适用、操作符、历史覆盖、staleness 与参数 schema。参数化同一因子的两个窗口拥有不同 binding ID。

目录只声明注册能力；具体 Snapshot 缺字段、历史不足或 PIT 不合格由 ScreenRun preflight 返回。未知输入/操作符/schema version fail closed。

## 5. S1 纯规则引擎

### S1-03 条件树与三值逻辑

条件树节点 ID 在版本内唯一。保存时验证：逻辑组非空、NOT 恰有一个子节点、类型与操作符匹配、区间端点合法、集合元素同型、input binding 存在且单位可比较。

规则固定为：

```text
NOT unknown = unknown
AND: 任一 false => false；全 true => true；其他 => unknown
OR:  任一 true => true；全 false => false；其他 => unknown
```

只有根节点 `true` 进入排名候选。`is_missing/is_present` 对值状态产生确定布尔结果，但不能绕过整个数据集缺失、频率不支持或严格 PIT 失败。

`required_value_policy` 必填且仅为：

- `exclude_instrument`：无法判定或排名的标的排除并计数。
- `fail_run`：必要输入未知且影响成员/排名时整次失败。

展示列缺失只影响显示。每个节点输出 input、typed threshold、truth、missing reason、数据时间与修订引用，解释器不能回查当前最新数据。

复杂度限制由配置与容量证据决定，至少限制树深、节点数、集合长度、bindings、结果列和扫描预算；文档不预设未经验证的数字。

### S1-04 排序、评分与选择

两种模式互斥：

- 多字段排序：按声明优先级和方向比较，最终追加 `instrument_id ASC`。
- 综合评分：对完整评分母体计算平均秩百分位，按方向与明确权重求和，再按 score 降序与稳定键排序。

百分位政策：`m > 1` 时 `p=(r-1)/(m-1)`；`m=1` 时 `p=0.5`；全相等均为 0.5。权重非负、总和为 1，零权重总和拒绝；缺分量不重分权。比较精度、并列、累加和舍入属于版本化 scoring policy。

`selection` 必填为 `all` 或正整数 `top_n`。不足 N 返回全部合格项，不补不合格项；边界同分仍按稳定键取足 N。展示排序不改变正式 rank 或成员。

分片只用于读取/计算；全局排序和百分位在完整母体上完成。外部排序实现必须与单批结果逐项一致。

## 6. S2 数据、任务与持久化

### S2-01 预检与执行计划

预检一次返回全部 Issue：版本/工作区、Snapshot/Universe/as_of、字段与因子依赖、频率、lookback、PIT、staleness、类型/单位、条件、评分、top_n、复杂度和预计扫描量。提交 Run 时重新预检。

执行顺序固定：

1. 冻结 ScreenRunRequest，预分配 run_id 并提交 Job。
2. 在同一 DataView 解析历史母池。
3. 按因子声明的完整横截面/历史窗口计算所需输入。
4. 逐标的执行条件树并保存所有相关解释。
5. 在完整评分/排序母体上排名，再应用 selection。
6. 生成互斥 Summary、rows 与 explanation artifact。
7. 通过现有内容寻址与 fencing 协议原子发布终态。

空母池或合法规则筛出 0 个是 succeeded 空结果。整个依赖缺失、非法规则或发布失败是失败。FactorEngine 对空母池的处理需区分“无需计算”与“缺失依赖”。

缓存键包含 screener/config hash、bindings、snapshot hash、universe、as_of/timezone、strict PIT、required value policy、engine/scoring version。不得将最终日期名单缓存给过去时点。

已交付（`internal/screenrun`）：

- `Service.Preflight` / `Service.Execute` 共用一次解析：拆包 screener 版本、校验 universe 版本 pin 与其 definition hash 一致、校验 snapshot 严格性、开 PIT view 解析母池、逐绑定解析（factor 走窗口推导 + FactorEngine 预检，field 走数据集目录）、以解析出的输入目录校验规则。
- 因子窗口**由数据推导**而非把“回看期数”换算成时间跨度：取 `RecentEventTimes` 的第 N 新日期作为半开区间起点；快照历史不足时报告 `insufficient_history`，不静默缩短窗口。
- `Execute` 在有 error 级 finding 时拒绝计算（`screenrun.preflight_failed` → 422），因此不会发布“看起来权威”的结果。
- 提交（`POST /screen-runs`）重新预检，24h 幂等键必填，202 + `Location: /api/v1/jobs/{job_id}`；`screenrun.Request` 是唯一的冻结输入形状（`FrozenConfigOf` / `RequestOfFrozen`）。
- **单位可比性已交付**（SC-AC-01 的单位那半）：因子契约声明每个输入的 unit，数据集目录声明字段实际存储的 unit，`checkFactorInputs` 按**相等**比较（与表达式单位检查同规则），不一致 → `screenrun.unit_mismatch`，从未声明 → `screenrun.unit_undeclared`（未声明不等于兼容：无法核验就必须阻断），数据集/字段不存在 → `screenrun.dataset_unknown` / `screenrun.field_unknown`；全部 error 级，因此预检 `valid=false`、提交 422、运行拒绝计算。条件字面量不带单位（与表达式一致：无单位一侧继承另一侧），所以条件层没有单位可比性可查。
- **频率核对已交付**：数据集目录新增 `Dataset.frequencies`——该 dataset 的 batch 实际摄取过的频率（排序去重，契约 `Dataset.frequencies` 必需）。`Append` 把同一频率写在 batch 与它写入的每一行观测上，所以批级列表恰好回答“按这个频率读能得到什么”；`checkFactorInputs` 要求因子输入的 `Frequency` 在列表内，否则 `screenrun.frequency_mismatch`（error 级）。这样“读一个没有数据的频率”在预检就说清楚，而不是让每个成员都变成缺失值。
- 预检量的口径：`estimated_rows` = 母池成员数（只有池解析成功就有值，字段-only 方案也有），`estimated_scan_rows` = 成员数 × 推导出的窗口内日期数，**只在存在因子窗口时有值**（字段绑定读的是各成员最新值，不构成日期窗口扫描，不臆造估算）。预检不预演评分分位。
- **部分成员不可计算时继续运行（已按决策实现）**：因子引擎新增**可选**的宽容模式——`factor.RunRequest.TolerateMemberGaps`，配合 `RunRequest.Tolerates(code)` 作为“什么被容忍”的唯一定义（目前只容忍 `ProblemInsufficientHistory`，即某成员在窗口内点数不够；PIT 无法核验、数据集/字段缺失、参数非法等仍致命）。选股的 preflight 与 compute 都开启该模式，因此因子绑定在“部分成员算不出”时是 `Available: true` + warning，这些成员在 `Execute` 里成为缺失值并落入既有阶段：条件用途 → `condition_unknown`，排名用途 → `rank_insufficient`（0 入选时给出 `empty_reason`）。缓存键加入该标志，避免严格调用方被喂一份“含缺失成员”的结果。因子运行/分析保持严格默认。可见后果：研究台预检面板在“通过但有提示”时列出提示（不再只显示成功）。
- **`source_run_id` 必须指向存在的 Run**：它是调用方对“本次运行来自哪次运行”的断言，随运行一起发布并作为证据回读，所以 `resolve` 要求它在同一工作区内存在，否则按引用不存在处理（404 `resource.not_found`）。这是请求错误而非 finding——没有来源可查的请求无法被评估；反过来说，让一个指向不存在 Run 的重跑落库，等于把无法核验的来源当成事实。

### S2-02 元数据与结果

迁移编号按实施时现有序列分配，不在本文预占。最小表：

| 表 | 关键约束 |
| --- | --- |
| screener_versions | workspace + id/version 唯一；parent、canonical definition、schema/config hash |
| screen_runs | run/job、冻结 request、source_run、engine/policy、终态与 result refs |
| screen_result_rows | run + instrument 唯一；selected、rank、score、稳定查询列与 row artifact ref |

大解释树和完整矩阵进入 Artifact，元数据保存索引与摘要。rows 默认正式 rank + instrument_id 稳定分页；排除项 rank=null 并按明确阶段/稳定键排序。游标绑定 workspace、run、冻结结果 hash、filter/search/sort。

Summary 计数互斥且守恒：

```text
母池 = 条件 false + 条件 unknown + 条件 true
条件 true = 排名数据不足 + 可排名
可排名 = 入选 + 未入选
```

单标的可以记录多个失败节点，但汇总只能进入一个阶段。

已交付（迁移 `00009_screen_runs.sql`、`internal/data/screenrun.go`、`internal/screenrun/handler.go`）：

- **两表而非三表**：`screen_runs`（冻结 request + config hash + engine/scoring version + snapshot hash + summary + result hash + columns + artifact ids + published_at）与 `screen_result_rows`（run + instrument 唯一，ordinal 为引擎规范序，selected/stage 供过滤，`row_json` 为权威证据）。artifact 引用按 `jobs.result_refs` 的先例存为 `screen_runs` 上的 JSON 列，因此不设 `screen_run_artifacts` 表。
- **解释就在行证据里**：节点的 truth/input/threshold 与评分分量随 `row_json` 一起冻结，`GET .../explanations/{instrument_id}` 直接读该行，不需要第二份 artifact；`engine_version` 记录产生它的流水线版本。
- **结果不可变且原子发布**：`PublishScreenRun` 在**一个事务**内校验守恒、写 summary/result hash/artifact ids/published_at 并插入全部行；`WHERE published_at IS NULL` 让第二次发布成为 409 `resource.conflict`，崩溃只会留下未发布记录。发布前读取 rows/explanations 返回 409 `screenrun.result_not_ready`，`GET /screen-runs/{id}` 仍返回 200 且 `summary: null`。
- **run 行在 worker 开始执行时创建**：queued job 不会暴露尚未开始的 run；重试（新 Job）产生新 run 行，旧行保持未发布，符合“重试不覆盖”。
- **分页**：`ordinal` 键集顺序（隐藏 rank ⇒ 排除行天然排在最后）；游标用 scope 绑定 run + 冻结 result hash + state 过滤，换用即 400，过期 422 `pagination.cursor_expired`。
- **规范结果 artifact**：summary + columns + 全部行以规范 JSON 经内容寻址写入 artifact store，其 checksum 即 `result_hash`，导出与展示可据此核对一致。
- **列描述**：`columns` 由冻结行自身推导（观测到的 kind ⇒ Field.type，缺失该列的任一行 ⇒ nullable），从不全的列保持 `unknown`，与数据集目录同一口径。
- **行与阈值的 Value 形状走数据面同一映射**（2026-09-14 修正）：契约的 `Value` 把 `value` 与 `missing_reason` 都列为必需（不适用时为显式 null），而 `domain.Value` 的两个字段带 `omitempty`，直接序列化会在"缺失格子"上丢掉 `value`、在"有值格子"上丢掉 `missing_reason`——客户端看到的是"字段不存在"而不是"显式 null"。现在 `screenRowWire.values` 与 `screenNodeWire.threshold` 都改走数据面已用的 `wireValueOf`：缺失格子输出 `{"kind":...,"value":null,"missing_reason":"missing_value"}`，有值格子输出 `{"kind":"decimal","value":"10.40","missing_reason":null}`；既无编码又无缺失原因的格子按内部错误 fail-closed（这类格子是损坏的证据，不是 null）。回归测试见 `internal/server/screenruns_test.go` 的 `TestScreenRowValuesCarryBothContractKeys`。
- 未交付：S2-03 的静态池保存与导出端点。

### S2-03 静态池与导出

从成功 Run 保存静态池时只使用完整入选集合，不能使用当前页或 UI 搜索结果。空入选返回 422 `empty_selection`。UniverseVersion 保存 source run、as_of、snapshot hash、quality limits；回测预检拒绝其用于 selection as_of 之前的决策。

导出 scope 必填 `selected | all_candidates`，格式 `csv | json`。导出与冻结结果 checksum 一致；CSV 防公式注入，保留 decimal 文本、单位、时区、数据时间与质量说明，并执行数据许可策略。

静态池一半已交付（`POST /screen-runs/{id}/universe`、migration 00010/00011）：

- **请求体只有 `name`**：入选名单由服务端逐页读完全部 selected 行（`selectedMembers`），因为用当前页或客户端过滤后的列表保存，会静默存下与 Run 不同的池；客户端传 `members` 会因 `validation.unknown_field` 被拒。
- **契约补齐**：`Universe` 原本没有任何字段能承载来源证据（`additionalProperties: false`），SC-AC-09/10 因此无法通过契约表达，于是给 `Universe` 增加了可选 `source`（nullable `ScreenUniverseSource`）。`source` 与 name、snapshot 绑定一样**不进 definition_hash**（它不改变选中了谁，只记录何时、在什么限制下选中），存储上是 `universe_versions.source_json`（`''` 表示非 Run 保存），有测试断言同一定义带/不带 source 的 hash 相同。
- **quality limits 来自 Run 自己**：Run 在发布时冻结其非 error 级 finding 的 code（`screen_runs.quality_limits_json`，migration 00011，写进规范 artifact），保存池时复制到 `source.quality_limits`。“探索质量限制不丢失”因此是搬运既有证据，而不是重新推断。
- **时点不可回填已在选股侧落地**（SC-AC-10 的前半）：`source.as_of` 就是这份名单的选择时点，`screenrun.resolve` 现在要求 `source.as_of <= req.as_of`，否则 error 级 `screenrun.universe_selection_after_as_of`（消息里同时给出名单选择时点与本次决策时点）→ 预检 `valid=false`、提交 422。名单本可以用当时还看不到的数据挑出来，把这份选择摊到更早的决策上等于回填一个当时无人能做的决定；判定所需证据（`Universe.source`）在保存时就已落库，这一步只是开始消费它。回测侧的同一规则仍在 S4。
- 未交付：`/screen-runs/{id}/exports`（导出 Job）、以及 S4 里“用 t 时点名单做 t 之前决策 → 拒绝”的回测侧判定（选股侧的同一判定已交付，见上）。

## 7. S3 前端闭环

新增路由：`/screeners`、`/screeners/new`、`/screeners/:id`、`/screen-runs/:id`。实现细节见 [前端实施](frontend-delivery.md)。

页面必须区分方案、运行与名单：保存方案不运行；重新运行创建新 Run；保存名单创建新 UniverseVersion。条件分组可由按钮和键盘完成，不强制拖拽。结果页同时显示入选、条件不满足、数据不足与排名不足；0 结果不自动放宽规则。

## 8. S4 与策略/复盘衔接

- 静态名单只代表选择时点事实，不会每日自动更新。
- 动态调仓日执行 ScreenerVersion 是后续能力；不得把最终名单回填整个回测。
- ReplaySession 内选股由服务端固定 snapshot、universe、current_as_of 与 session revision；跨会话或未来 ScreenRun 不能提交订单来源。
- 人工订单即使来源于选股，也必须重新经过当前会话市场、资金和库存校验；选股结果不预留资金或库存。

## 9. 工作包与交付物

| ID | 交付 | 主要证据 |
| --- | --- | --- |
| S1-01 | 主需求、OpenAPI、Go/TS DTO 与契约测试 | 生成无漂移、未知枚举兼容、标准错误 |
| S1-02 | 输入能力目录 | field/factor schema、Snapshot 不可用说明 |
| S1-03 | 条件树、三值逻辑与解释 | 真值表、类型/单位、unknown/NOT 属性测试（`internal/screening/truth_test.go` 的 Kleene 表、SC-AC-02 负小数比较、范围/集合全覆盖） |
| S1-04 | 稳定排序、百分位评分与 top_n | 单元素/同值/分片/分页 oracle（`ranking_test.go` 的 m=1/全等→0.5、平局平均秩、shuffle oracle；`engine_test.go` 守恒/fail_run/空因） |
| S2-01 | Preflight、Engine 与 Job handler | PIT、空母池、取消/恢复/fencing（已交付：`/screen-runs/preflight` 与 `POST /screen-runs`(202+Job+幂等键)、`internal/screenrun` 的解析/绑定/coverage 与 `Execute`、数据驱动的因子窗口推导、**因子输入契约核对**（单位 `unit_mismatch`/`unit_undeclared`、频率 `frequency_mismatch`、数据集/字段未知，见 `internal/screenrun/units_test.go`）、**可选宽容缺口模式**（`factor.RunRequest.TolerateMemberGaps`，见 `internal/factor/tolerant_test.go`）、`screenrun.Handlers` 的 `screen.run` job kind；**纵向验收** `internal/server/m1s_acceptance_test.go` 走真实 provider→快照→池→方案→预检→运行→结果/解释→保存池，并断言重跑一致、薄数据容忍与 0 结果解释；**时点不可变性验收** `internal/server/m1s_pit_acceptance_test.go`（SC-AC-03）直写数据面：晚到修订（可用时间落在决策时点内）与决策时点后才可用的 bar 入新快照后，旧 Run 的快照 hash/行/rank/解释不变，而新快照看得见修订、看不见未来 bar）|
| S2-02 | 版本/Run/结果存储与查询 | 迁移、原子发布、Summary 守恒、游标（已交付：`screener_versions` + `/screeners` CRUD + `GET /screeners/{id}?version=`，以及 `00009_screen_runs.sql`、`/screen-runs` 清单/详情/rows/explanations、发布前 409 `screenrun.result_not_ready`、scope 绑定的行游标、守恒在发布事务内复核） |
| S2-03 | 静态池与导出 | 来源时点、完整 scope、许可与 CSV 安全（静态池已交付：`POST /screen-runs/{id}/universe` 只收 name、服务端读完整入选集、空入选 422 `screenrun.empty_selection`、`Universe.source` 携带 run/as_of/snapshot hash/quality limits 且不进 definition_hash）；未交付：`/screen-runs/{id}/exports` 与 S4 的时点拒绝判定 |
| S3 | 四个页面的完整路径 | 成功/失败/断线/键盘/窄视口 |
| S4 | 回测时点校验与 Replay 引用检查 | 未来名单/跨会话拒绝 |

## 10. 验收与完成定义

SC-AC-01..12 的自动化映射见 [验证与追踪](verification-and-traceability.md)。此外，以下全部成立才可标记 `GATE-SCREEN-DONE`：

- SCREEN-01..08 已纳入主需求，OpenAPI、Go/TS、迁移与实际 handler 同步。
- 纯规则、数据/因子、全局排名、Job 恢复、静态池与导出均有反例测试。
- 同一冻结输入重跑的名单、rank、score、解释和 checksum 一致。
- 页面不用手写 JSON 完成方案→预检→运行→解释→保存/导出。
- 真实市场字段与容量尚未验收时，只能声明 synthetic 完成，不能提升为完整 M1S。

