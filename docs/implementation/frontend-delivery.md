# 前端实施文档

目标：以 React + TypeScript + Vite、Ant Design 和 ECharts 实现中文研究工作台；后端 OpenAPI、Job 和 Run 是业务事实源，前端不复制指标、市场或数据规则。

## 1. 初始化门禁与目录

在 IMP-08 批准后锁定路由、服务端状态、表单/Schema 和测试工具版本。前端目录建议：

```text
web/src/app/                 路由、Provider、错误边界、全局布局
web/src/api/generated/       OpenAPI 生成代码，禁止手改
web/src/api/runtime/         auth、request-id、idempotency、SSE、错误适配
web/src/features/            connections/data/universes/factors/strategies/backtests/experiments/jobs
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
| M2 | `/strategies`、`/backtests/new`、`/backtests/:id`、`/experiments` | 策略版本、回测闭环、对比、重跑、导出 |

未交付路由显示明确的阶段和依赖，不放静态假数据或可点击的无效主操作。

## 3. API 与状态所有权

### 3.1 三类状态

| 类型 | 所有者 | 示例 | 规则 |
| --- | --- | --- | --- |
| URL 状态 | Router | q、filter、sort、分页、tab、选中 ID | 可分享/刷新恢复；筛选变化清 cursor |
| 服务端状态 | 查询缓存 | 列表、详情、Job、Report | key 含 workspace、资源 ID 和规范化查询；mutation 后精确失效 |
| 本地草稿 | 表单/页面 | 未保存参数、列宽、临时选择 | 不作为运行配置；离开未保存时提示 |

研究引用（snapshot、universe、strategy、model、range）由 `ResearchContextBar` 展示，并从后端资源/URL 恢复。运行启动后详情读取冻结的 Run 配置，不继续绑定可编辑草稿。

### 3.2 HTTP runtime

- generated client 只负责 schema/调用；runtime 统一加入 Bearer、`X-Request-ID` 和写请求 `Idempotency-Key`。
- 每次用户提交生成一个 key，在响应不确定时复用同 key；用户明确再次创建才生成新 key。
- 将标准 Error 映射为字段 Issue、页面级 Issue 和重试信息。401、403、409、422、429 与 5xx 分别处理。
- 202 必须读取 Job/Run 引用并跳转状态页，不能显示“已完成”。
- 价格、金额、数量保持字符串直到格式化展示；不经过 JavaScript number。
- 日期/时间 API 保留 offset，显示时明确时区；交易时区来自模型而不是浏览器 locale。
- 下载先读取 Artifact 元信息，再由受权 content 端点获取；不拼文件系统路径。

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
- 键盘、axe/等价自动扫描、人工焦点/图表摘要、窄视口截图。
- 无 secret 落入 URL、localStorage、前端日志、测试快照或错误采集的验证。

