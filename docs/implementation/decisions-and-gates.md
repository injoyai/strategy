# 决策与交付门禁

状态：Proposed。这里集中管理会改变范围、正确性或部署行为的决定。`Blocked` 表示相关工作包不得用示例默认值继续，不表示所有实施停止。

## 1. 业务决策登记

| ID | 状态 | 必须回答的问题 | 解锁内容 | 最晚门禁 | 应形成的证据 |
| --- | --- | --- | --- | --- | --- |
| DEC-01 | Blocked | 首期市场、资产类别、主频率、交易时区和日历来源 | 市场规则、真实数据验收、因子首发 | `GATE-M1-START` | ADR、市场能力表、代表性证券/日期样本 |
| DEC-02 | Blocked | 供应商、账户权限、历史范围、修订/PIT 覆盖、预算与许可 | 真实 Provider 和数据可信度承诺 | `GATE-M1-START` | 官方接口/许可、账号探针、限流与字段样本 |
| DEC-03 | Partial（M0 假设 2026-09-12 已确认，见 ADR-0002） | 首期个人本地或团队部署；数据量、并发、备份与恢复目标 | SQLite/PostgreSQL、认证、性能指标 | `GATE-M0-START` 可先按单机实验；`GATE-M1-START` 前必须定案 | 部署 ADR、容量模型、故障与恢复目标 |
| DEC-04 | Blocked | 仅研究、研究+模拟或包含实盘 | M4 范围 | `GATE-M4-START` | 独立交易范围与合规/风控要求 |
| DEC-05 | Blocked | 与其他交易系统共享的模型、schema、包格式和兼容版本 | 成果包映射 | `GATE-M2-START`；M0 只实现内部 manifest | 跨系统契约与兼容性测试 |
| DEC-06 | Blocked | 首个验收策略、初始资金/币种、费用、滑点、基准和价格口径 | 真实回测验收 | `GATE-M2-START` | 手算样本、模型版本与 metric policy |

## 2. 工程决策登记

| ID | 状态 | 决策内容 | 约束与建议 | 门禁 |
| --- | --- | --- | --- | --- |
| IMP-01 | Approved（2026-09-12，ADR-0001） | module `github.com/injoyai/strategy`，go ≥ 1.26（toolchain 自动获取） | module path 使用项目拥有且长期可控的路径；不写 `example.com` 占位后继续扩散 | `GATE-M0-START` |
| IMP-02 | Approved（2026-09-12，ADR-0001） | 单仓库单 Go module，服务代码位于 `cmd/` 与 `internal/` | 与 Go 官方 server module 布局一致；将领域包保持为 internal，未来确需共享再拆 module | `GATE-M0-START` 批准 |
| IMP-03 | Partial（M0 用 SQLite，ADR-0002；随 DEC-03 于 GATE-M1-START 终定） | 元数据数据库与驱动 | M0：`modernc.org/sqlite`（纯 Go）+ WAL + 本机文件系统；团队/多主机 Worker 选择 PostgreSQL。若使用 SQLite WAL，数据库文件必须在本机文件系统，并验证运行时包含已修复 WAL-reset 问题的版本 | 随 DEC-03 |
| IMP-04 | Approved（2026-09-12，ADR-0003） | `shopspring/decimal`，领域层包装类型 | 已验证 JSON 字符串解析、精确加法、非法输入拒绝与 MIT 许可；指数拒绝由 HTTP 边界解析器承担 | M0 值对象前 |
| IMP-05 | Partial（2026-09-12，ADR-0003/0004） | 路由=标准库 `net/http`；迁移=`pressly/goose/v3`；日志=标准库 `log/slog`；TS 客户端=`openapi-typescript` + `openapi-fetch` | 优先标准库和小依赖；锁定版本、许可证与生成可重复性 | 对应代码首次合入前 |
| IMP-06 | Proposed | 文件格式和布局 | M0 用小型可核查实现证明 manifest/原子发布；Parquet 库必须用代表性行情/因子矩阵基准后选择 | M1 大数据写入前 |
| IMP-07 | Approved（2026-09-12，ADR-0002） | 单二进制 `researchd`、持久化队列、可分离 Worker | HTTP 与 Worker 组件可同进程部署，但不以内存队列作为事实源 | `GATE-M0-START` 批准 |
| IMP-08 | Approved（2026-09-12，ADR-0004） | React Router v7、TanStack Query v5、Vitest + Testing Library、openapi-typescript + openapi-fetch | React/Vite/AntD(6)/ECharts(6) 已定；版本锁定于 `web/package.json` | Web 初始化前 |

工程选型依据应记录在 `docs/adr/`。官方基线包括 [Go module 组织建议](https://go.dev/doc/modules/layout)、[SQLite WAL 的并发与同机限制](https://sqlite.org/wal.html)、[HTML SSE 标准](https://html.spec.whatwg.org/multipage/server-sent-events.html) 和 [WCAG 2.2](https://www.w3.org/TR/WCAG22/)。外部资料只作为证据，不替代本项目决策。

## 3. 门禁定义

### GATE-M0-START

- IMP-01 已批准；能创建真实 `go.mod`，不是临时 module path。
- DEC-03 至少确认 M0 的执行假设：单机/单 Worker 验证还是直接按团队部署设计；未确认时只允许纯领域和契约工作。
- 初始生产依赖完成许可证、安全、Windows/目标平台和最小样例验证。
- OpenAPI 生成源与生成物的所有权清楚，确认不能手改 `openapi.json`。

### GATE-M0-DONE

- 数据库从空库迁移到当前版本并可备份/恢复；失败迁移不留下半完成状态。
- 相同幂等键同请求返回原结果，同键异请求冲突；取消与成功有唯一获胜终态。
- Worker 异常退出后租约被回收，旧 fencing token 不能提交结果。
- synthetic 更新、质量报告、快照发布和页面查看纵向贯通。
- OpenAPI 同步、Go/前端类型检查、单元/集成测试和最小浏览器测试通过。

### GATE-M1-START

- DEC-01、DEC-02、DEC-03 已批准。
- 真实供应商账号探针证明权限、覆盖、分页、限流、单位、时区和错误语义；敏感值未进入文档和 fixture。
- 数据 schema、代码映射、日历与可用时间政策已版本化。
- 数据规模基准支持数据库与文件格式选型，或明确容量上限。

### GATE-M1-DONE

- AC-01..05 通过；强制拉取、修订、PIT、历史成分和缺失依赖均有反例测试。
- 原始批次到快照可追溯，严格 PIT 不接受 `pit_unverified`。
- 一个真实数据包及相应基础因子从页面完整运行，供应商限制在报告中可见。
- 数据与元数据联合备份恢复后，manifest 引用和校验和通过。

### GATE-M2-START

- DEC-05、DEC-06 已批准；DEC-01 对应市场规则、成交、费用、估值和 metric policy 均有版本。
- 手算 oracle 覆盖买入、卖出、费用、部分成交、公司行为、估值和至少一种歧义时序。
- 预检能拒绝所有缺失市场能力，不能以零费用或无滑点自动降级。

### GATE-M2-DONE

- AC-06..10 通过；相同输入重跑的订单/成交/账本逐项一致，统计值在书面容差内。
- 训练/验证/测试、预热和测试集使用历史在报告与 manifest 中固定。
- 页面成功、失败、加载、空结果、取消、重试、断线恢复、窄视口和键盘路径通过。
- 导出包校验和可验证；未获许可的原始数据只导出引用与摘要。

### GATE-M3-DONE

- AC-11 及新增因子/数据的 PIT、单位和修订测试通过。
- 批量任务的所有尝试、失败与取消均可追溯，不只保留最优结果。
- 成本压力、滚动验证和市场阶段切片使用同一版本化口径。

## 4. 决策流程

1. 发起人写出问题、可选方案、约束、代表性数据和不决策的后果。
2. 对兼容性、许可证、安全、迁移、可恢复性和性能做最小验证。
3. 将批准方案写入 ADR，并更新本表状态、日期和解锁工作包。
4. 若决策变化，新增 ADR 说明替代关系；不可悄悄修改已经被 Run 引用的版本语义。

