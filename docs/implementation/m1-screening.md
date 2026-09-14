# M1S 选股功能实施

版本：0.4，2026-09-14。状态：S1-01..S1-04 契约与纯规则已落地；S2 已分两次落地——(1) ScreenerVersion 的 wire 模型与持久化：`internal/screening/wire.go` 按契约 oneOf 逐分支编解码（range 恒发 null 边界、factor binding 恒发 params、缺 factor_ref 拒绝解码），`screener_versions` 以 (workspace, id, version) 为主键、`id` 是方案身份而 `version` 是不可变修订，`parent_id` 追加 `v<N+1>`（修订号在写事务内推导），`definition_hash` 只覆盖规则契约（不含 name/description/parent_id，与 universe version 同规则），`GET /screeners/{id}` 补齐 required `version` query（与 `/factors/:id` 一致），端点 `/screeners`（列表/创建）与 `/screeners/{id}` 已实现；(2) ScreenRun preflight：新增 `internal/screenrun` 编排层（不放进 `internal/screening`，因为 `ports` 已导入 `screening`，反向导入会成环），`POST /screen-runs/preflight` 解析冻结的 screener/universe 版本、要求 universe 的 `definition_hash` 作为不可变 pin、校验 universe 与快照绑定一致、在绑定快照的视图上于 as_of 解析母池、做结构性规则校验，并按绑定给出 coverage——factor 绑定校验注册与参数规范化（失败为 error 级），field 绑定在当前无法解析时显式报告 `screenrun.catalog_unavailable`（warning 级，不冒充可用）。**尚未实现**：field 绑定的类型/单位校验与因子数据可用性（PIT/lookback/staleness）评估，二者分别被“数据集字段元数据未持久化”和“窗口推导策略未定义”阻塞；estimated_scan_rows/estimated_rows 暂为 null（与 factor preflight 同口径）；Run/rows/explanations 存储与查询、静态池与导出、S3/S4 均未开始。设计来源为 [选股功能设计](../stock-screening-design.md)。SCREEN-01..08 与 SC-AC-01..12 已进入主需求与 OpenAPI；其余端点、Go/TS handler 与页面尚未实现，本文件定义接入步骤，不表示功能已经存在。

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

### S2-02 元数据与结果

迁移编号按实施时现有序列分配，不在本文预占。最小表：

| 表 | 关键约束 |
| --- | --- |
| screener_versions | workspace + id/version 唯一；parent、canonical definition、schema/config hash |
| screen_runs | run/job、冻结 request、source_run、engine/policy、终态与 result refs |
| screen_result_rows | run + instrument 唯一；selected、rank、score、稳定查询列与 row artifact ref |
| screen_run_artifacts | run + kind 唯一；summary/rows/explanations/export 引用 |

大解释树和完整矩阵进入 Artifact，元数据保存索引与摘要。rows 默认正式 rank + instrument_id 稳定分页；排除项 rank=null 并按明确阶段/稳定键排序。游标绑定 workspace、run、冻结结果 hash、filter/search/sort。

Summary 计数互斥且守恒：

```text
母池 = 条件 false + 条件 unknown + 条件 true
条件 true = 排名数据不足 + 可排名
可排名 = 入选 + 未入选
```

单标的可以记录多个失败节点，但汇总只能进入一个阶段。

### S2-03 静态池与导出

从成功 Run 保存静态池时只使用完整入选集合，不能使用当前页或 UI 搜索结果。空入选返回 422 `empty_selection`。UniverseVersion 保存 source run、as_of、snapshot hash、quality limits；回测预检拒绝其用于 selection as_of 之前的决策。

导出 scope 必填 `selected | all_candidates`，格式 `csv | json`。导出与冻结结果 checksum 一致；CSV 防公式注入，保留 decimal 文本、单位、时区、数据时间与质量说明，并执行数据许可策略。

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
| S2-01 | Preflight、Engine 与 Job handler | PIT、空母池、取消/恢复/fencing（preflight 已交付：`/screen-runs/preflight` + `internal/screenrun`，覆盖解析/绑定/结构与逐绑定 coverage；引擎计算、Job handler 与 PIT/窗口级检查待做） |
| S2-02 | 版本/Run/结果存储与查询 | 迁移、原子发布、Summary 守恒、游标（版本部分已交付：`screener_versions` + `/screeners` CRUD + `GET /screeners/{id}?version=`） |
| S2-03 | 静态池与导出 | 来源时点、完整 scope、许可与 CSV 安全 |
| S3 | 四个页面的完整路径 | 成功/失败/断线/键盘/窄视口 |
| S4 | 回测时点校验与 Replay 引用检查 | 未来名单/跨会话拒绝 |

## 10. 验收与完成定义

SC-AC-01..12 的自动化映射见 [验证与追踪](verification-and-traceability.md)。此外，以下全部成立才可标记 `GATE-SCREEN-DONE`：

- SCREEN-01..08 已纳入主需求，OpenAPI、Go/TS、迁移与实际 handler 同步。
- 纯规则、数据/因子、全局排名、Job 恢复、静态池与导出均有反例测试。
- 同一冻结输入重跑的名单、rank、score、解释和 checksum 一致。
- 页面不用手写 JSON 完成方案→预检→运行→解释→保存/导出。
- 真实市场字段与容量尚未验收时，只能声明 synthetic 完成，不能提升为完整 M1S。

