# 前端实施文档

目标：以 React + TypeScript + Vite、Ant Design 和 ECharts 实现中文研究工作台；后端 OpenAPI、Job 和 Run 是业务事实源，前端不复制指标、市场或数据规则。

## 1. 初始化门禁与目录

在 IMP-08 批准后锁定路由、服务端状态、表单/Schema 和测试工具版本。前端目录建议：

```text
web/src/app/                 路由、Provider、错误边界、全局布局
web/src/api/generated/       OpenAPI 生成代码，禁止手改
web/src/api/runtime/         auth、request-id、idempotency、SSE、错误适配
web/src/features/            connections/data/universes/factors/screeners/strategies/backtests/replay/experiments/jobs
web/src/components/          ResearchContextBar/PagedTable/SchemaForm/JobStatus/IssueList/MetricValue/ArtifactDownload
web/src/theme/tokens.ts      唯一设计 token 源并映射 Ant/CSS/ECharts
web/src/test/                render 工具、MSW/等价 API fixture、a11y helper
web/e2e/                     跨页面关键流程
```

feature 可以引用共享组件与 generated API，不能跨 feature 导入私有状态；需要共享的研究引用通过 typed route/query 或公共组件传递。任何前端派生值只用于展示，报告指标和数据质量以 API 返回为准。

## 2. 路由与逐阶段交付

| 阶段 | 路由 | 完成条件 |
| --- | --- | --- |
| M0 | `/connections`、`/data`、`/jobs` | synthetic 连接→更新→任务→质量→快照；刷新/断线可恢复 |
| M1 | `/universes`、`/factors`、`/factors/:id` | 历史成员预览、因子预检/运行/分析、缺依赖回链 |
| M1S | `/screeners`、`/screeners/new`、`/screeners/:id`、`/screen-runs/:id` | 方案→预检→运行→解释→静态池/导出；0 结果和数据不足可解释 |
| M2 | `/strategies`、`/backtests/new`、`/backtests/:id`、`/experiments` | 策略版本、回测闭环、对比、重跑、导出 |
| M2R | `/replay-sessions`、`/replay-sessions/new`、`/replay-sessions/:id` | 历史时点选股、人工委托、单日推进、账户、结束与报告 |

未交付路由显示明确的阶段和依赖，不放静态假数据或可点击的无效主操作。

M1S 四页已交付（`web/src/app/pages/ScreenersPage.tsx`、`ScreenerDetailPage.tsx`、`ScreenRunPage.tsx` + `web/src/app/screener-draft.ts`）：

- `/screeners` 列出不可变修订（服务端分页 + 搜索）并给出最近运行（可按 Job 状态过滤）；`/screeners/new` 与 `?from=<id>&fromVersion=<v>` 是同一编辑器，后者以既有修订为基底并带 `parent_id`，文案为“基于此版本新建”。
- 编辑器按 §8.1 分区编辑：输入绑定（字段/因子两种分支、因子参数表）、条件树（all/any/not/compare/range/set/missing，全部用按钮与下拉编辑，无拖拽）、排名/评分（互斥；权重之和必须为 1，否则拒绝）、数量与展示列。草稿到契约载荷的转换是纯函数（`screener-draft.ts` 的 `buildScreenerCreate`），拒绝时同时给出消息与聚焦目标，所以键盘路径与鼠标路径一致；该转换由 `screener-draft.test.ts` 直接测试，不需要渲染表单。
- `/screeners/:id?version=` 只读展示冻结规则（条件树与评分分量的可读渲染），并承载预检/提交运行表单：快照、母池（按所选快照过滤，并提示 `universe_ref.version` 必须是该版本的 definition hash）、as_of、时区、严格 PIT、必填值策略；提交 202 后显示任务链接并说明结果在发布前不可读。
- `/screen-runs/:id` 展示冻结配置与各哈希、Job 链接、互斥汇总（含空结果原因）、结果行（服务端分页 + `state` 过滤；列头显示声明单位与是否可缺失，缺失值给出原因而不是 0）、逐标的解释（节点 truth/阈值/缺失原因 + 评分分量），以及“保存为静态标的池”（只提交名称、说明使用完整入选集合，空入选不提供操作）。
- 未交付：导出端点（受 SCREEN-DEC-02「可导出数据许可」门禁约束）；浏览器端到端报告与窄视口截图待补。
- **页面层验收（2026-09-14 补）**：`ScreenerDetailPage.test.tsx` 现覆盖"预检失败后**保留全部输入**"（快照/母池/时点/时区都还在，否则改一条 finding 就得重填整个请求）与两条键盘路径（聚焦按钮按 Enter 触发预检；在表单字段里按 Enter 触发表单提交，即整条启动路径不需要鼠标）；`ScreenRunPage.test.tsx` 的空入选用例断言页面显示服务端给的 `empty_reason`（空结果是成功态但不是静默态）。断线恢复在 `src/api/sse.test.ts`（410 后清游标重连）与 `App.test.tsx`（服务不可用可重试）覆盖。**仍未覆盖**：真实浏览器里的端到端与窄屏渲染——`styles.css` 已有 1100/820/560 三档断点（≤820 时侧栏改为静态全宽、栅格折叠），但没有浏览器验证，也没有截图证据。

## 3. API 与状态所有权

### 3.1 三类状态

| 类型 | 所有者 | 示例 | 规则 |
| --- | --- | --- | --- |
| URL 状态 | Router | q、filter、sort、分页、tab、选中 ID | 可分享/刷新恢复；筛选变化清 cursor |
| 服务端状态 | 查询缓存 | 列表、详情、Job、Report | key 含 workspace、资源 ID 和规范化查询；mutation 后精确失效 |
| 本地草稿 | 表单/页面 | 未保存参数、列宽、临时选择 | 不作为运行配置；离开未保存时提示 |

研究引用（snapshot、universe、strategy、model、range）由 `ResearchContextBar` 展示，并从后端资源/URL 恢复。运行启动后详情读取冻结的 Run 配置，不继续绑定可编辑草稿。

选股额外区分 ScreenerVersion、ScreenRun 和临时表格筛选；只有前两者是研究事实。Replay 页以服务端 Session `current_as_of + revision` 为页面一致性边界，账户、图表、订单和选股都必须来自同一 revision；未提交订单表单仅是本地草稿。

### 3.2 HTTP runtime

- generated client 只负责 schema/调用；runtime 统一加入 Bearer、`X-Request-ID` 和写请求 `Idempotency-Key`。
- 每次用户提交生成一个 key，在响应不确定时复用同 key；用户明确再次创建才生成新 key。
- 将标准 Error 映射为字段 Issue、页面级 Issue 和重试信息。401、403、409、422、429 与 5xx 分别处理。
- 202 必须读取 Job/Run 引用并跳转状态页，不能显示“已完成”。
- 价格、金额、数量保持字符串直到格式化展示；不经过 JavaScript number。
- 日期/时间 API 保留 offset，显示时明确时区；交易时区来自模型而不是浏览器 locale。
- 下载先读取 Artifact 元信息，再由受权 content 端点获取；不拼文件系统路径。
- Replay 写命令同时发送 Idempotency-Key 与页面最后确认的 expected_revision；409 后读取最新 Session，不能自动用新 revision 重发交易。
- Replay data/query、图表和选股不允许请求晚于 current_as_of 的范围，响应缓存键包含 session/revision；浏览器不得预加载未来数据。

## 4. 分页、搜索与竞态

`PagedTable` 接收声明式 columns、filter schema、稳定 sort 和 cursor page。URL 保存筛选/排序，cursor 历史可保存在导航 state；若用户从深页刷新且前序 cursor 不可重建，则回首页并说明。

远程搜索 300ms 防抖，但中文输入法 `compositionstart` 至 `compositionend` 不发请求；清空立即发出。每个请求使用 AbortController 或请求序号，旧响应不得覆盖新查询。后端 cursor 400 时清除 cursor 重新加载首页，不循环重试。

大数据明细使用服务端分页；虚拟化只优化渲染，不绕过 API 上限。表格横向滚动时保留主键、状态和操作可发现性。

## 5. SchemaForm 与不可变版本

`SchemaForm` 支持后端注册的有限 JSON Schema 子集，首期明确允许 string/number-as-string/integer/boolean/enum/object/array、required、范围、pattern、description 和条件能力；遇到未知关键字显示“不支持该配置 schema”，不忽略校验。

流程：加载 schema/version → 建立草稿 → 本地即时校验 → 提交后端 → 映射全部 Issues → 成功跳转新版本并高亮。后端校验始终是最终结果。编辑既有不可变资源时 UI 文案为“基于此版本新建”，不出现原地保存幻觉。

Secret 字段单独组件：只写、不回显、不进入 URL/localStorage/日志/错误上报；成功后仅保存 secret_ref。复制/显示控制默认禁止。

## 6. JobStatus 与 SSE 恢复

浏览器使用可设置 Authorization header 的 fetch stream 订阅 SSE，不将 token 放在 URL。状态机 UI 与后端完全一致：

| 后端状态 | UI | 可用操作 |
| --- | --- | --- |
| queued | 等待执行 | 取消 |
| running | 阶段、已处理量；total 未知时不显示伪百分比 | 取消 |
| cancel_requested | 正在取消 | 无重复主操作 |
| succeeded | 完成并显示结果入口 | 查看/导出/按业务重跑 |
| failed | 稳定错误与 request_id | 查看 Issues、创建新重试 Job |
| cancelled | 已取消 | 按原配置新建重试（若允许） |

客户端持有最后业务 event ID：断线指数退避并发送 `Last-Event-ID`；410 时 GET 最新 Job，再从当前窗口订阅；SSE 不可用时降级轮询。断线不新建 Job。事件去重以 job_id + sequence；较旧事件不能覆盖新状态。页面隐藏可降低刷新频率，但恢复可见后立即同步。

## 7. 共用组件完成标准

- `ResearchContextBar`：完整显示 snapshot、universe、strategy/model version、range/timezone；窄屏换行；链接可定位依赖。
- `PagedTable`：服务端分页、URL 状态、加载/空/无结果/失败/部分结果、键盘和横向滚动。
- `SchemaForm`：字段说明、单位、错误聚焦、草稿保护、不可变版本文案、secret 边界。
- `JobStatus`：阶段/数量、取消竞争、断线重放、失败详情和 parent job 链。
- `IssueList`：severity、code、path、message、详情与可执行回链；不只 toast。
- `MetricValue`：value/null、unit、definition、policy、missing reason；数字 tabular。
- `ArtifactDownload`：媒体类型、大小、checksum、许可提示、异步未就绪状态。

每个组件必须有成功、加载、空、错误、权限、陈旧/断线和长文本/大数字 fixture；不是每个组件都强行显示所有状态，而是其适用状态都有测试。

## 8. 页面纵向实现顺序

每个页面按相同顺序交付：

1. Route loader/查询键与 OpenAPI 类型。
2. 只读列表/详情和 URL 状态。
3. mutation、幂等、错误/Issue 和跳转。
4. Job/Run 状态与恢复。
5. 空、部分、权限、陈旧、超长内容和窄视口。
6. 单元/组件测试、浏览器成功与失败路径、键盘检查。

优先完成真实纵向路径，不先批量搭建只有静态壳的所有页面。

### 8.1 选股页面专项

- 方案编辑明确区分条件、排名/评分、selection 和展示列；AND/OR/NOT 可用按钮与键盘编辑，不强制拖拽。
- 每个输入显示类型、单位、频率、可用历史与因子参数。unknown、false、排名不足和仅展示缺失使用不同文字状态。
- 保存方案不触发运行；运行绑定 snapshot/universe/as_of；重新运行创建新 ScreenRun。
- 结果表的 UI 搜索/分页/展示排序不改变正式成员/rank。保存静态池始终使用完整入选集合；导出 scope 明确。
- 解释抽屉显示节点 truth、输入时间/修订、评分分量与排除阶段；0 结果解释母池空、条件不满足或排名数据不足，不自动放宽。

### 8.2 历史复盘页面专项

- 页首持续显示“历史复盘”、历史日期/时区、snapshot、revision 和质量限制；现实时间只用于操作日志。
- K 线可见区间有清楚截止线，tooltip、指标和自动缩放不读取未来。网络测试验证响应/缓存无未来数据。
- 下单表单说明参考价格时点与最早可能成交时段；受理反馈不能写“成交”。持仓不随当日选股/母池变化消失。
- “下一交易日”只推进一天；advancing 时锁定下单/撤单/再次推进，刷新恢复同一 Job。跨标签冲突提示刷新事实。
- 账户、订单、成交、事件和图表只渲染同一已提交 revision。结束确认明确“取消余量并保留持仓”，不能写成自动平仓。

## 9. 图表实施

ECharts 数据来自版本化 Report/ResearchSeries：

- 净值与回撤共享 x 轴，显示基准、训练/验证/测试和缺失段。
- 因子图显示分布、覆盖、IC/分组收益；tooltip 包含时点、单位、样本数和缺失。
- 颜色不单独传达正负/状态；线型、符号和文字同时表达。
- 为每张图提供可访问名称、文字摘要和数据表/导出入口。
- resize 容器稳定，加载/错误有固定最小高度，避免布局跳动。
- 降采样请求/结果明确标识为展示数据；报告统计与导出使用完整精度。

## 10. 视觉、响应与无障碍

`tokens.ts` 实现 DESIGN.md 中颜色、字体、间距、圆角和图表语义色，再映射 Ant Design theme、CSS variables 与 ECharts theme。页面不得复制十六进制颜色。实现时用自动工具和人工测试验证 WCAG 2.2 AA 对比度，不把设计种子当已通过审计。

键盘验收覆盖：跳至主内容、导航、表格操作、筛选、弹窗焦点圈/返回、日期/下拉、Issue 回链、取消确认与下载。焦点可见且不被 sticky header 遮挡；错误聚焦首项并有摘要。减少动态效果设置下禁用非必要动画。

桌面以 216px 左导航和研究主区为基线；窄屏导航折叠、参数与结果顺序排列，上下文条换行。必要表格/图表局部横滚，关键版本和操作不因截断消失。浏览器/视口支持范围由 IMP-08 明确后进入 CI matrix。

## 11. 前端验收证据

- OpenAPI 生成代码与 JSON 同步，生成目录无手改。
- TypeScript strict、lint、unit/component/build 通过。
- 每个阶段关键路径的浏览器报告，包含成功、422、403、429/重试、500、取消、SSE 断线/410、刷新恢复。
- 选股覆盖三值条件、全局排序、0 结果、解释、完整名单保存与 scope 导出；Replay 覆盖 D+1 网络隔离、订单受理/推进、revision 冲突、服务重启和结束持仓。
- 键盘、axe/等价自动扫描、人工焦点/图表摘要、窄视口截图。
- 无 secret 落入 URL、localStorage、前端日志、测试快照或错误采集的验证。
