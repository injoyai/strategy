# 当前文档验证记录

日期：2026-09-12。本次范围仅为根据新增的选股与历史手动复盘设计更新实施文档；没有修改 OpenAPI、Go 或前端代码，也不把 Proposed 设计端点声明为已实现。

| 检查 | 结果 | 限制 |
| --- | --- | --- |
| `python docs/api/check_contract.py` | 通过：现有 OpenAPI 仍为 45 个路径、50 个操作、76 个 schema、618 个本地 `$ref`；生成文件匹配；检查到 70 个本地文档链接 | 标准库结构检查，不是完整 OpenAPI 合规或运行行为验证 |
| 实施追踪 ID 检查 | 通过：主需求 24、AC 12、SCREEN 8、SC-AC 12、REPLAY 10、RP-AC 14，均出现在实施文档 | SCREEN/REPLAY 仍来自 Proposed 设计，正式开发前还要进入主需求与契约生成链 |
| 独立 Markdown 相对链接检查 | 通过：排除 `.git`、`node_modules` 后共检查 26 个 Markdown 文件 | 只检查本地目标存在，不验证外部网页当前内容 |
| Proposed 契约隔离 | 通过：选股与 Replay 的候选端点仅写入设计/实施文档，未写入当前 OpenAPI | 不证明未来 DTO、handler、迁移或页面兼容 |
| 多 Agent 文档结构 | 通过：角色、Work Order、所有权、并发波次、当前阶段矩阵、交接、合并、验证责任、完成定义共 9 个必备部分齐全，并已从实施总纲和完成定义引用 | 只证明协作协议完整，不代表已经创建 Agent 分支或执行并发开发 |

曾启动统一仓库验证，但在第一步发现当前工作区的 `internal/jobs/jobs.go`、`internal/jobs/loop.go`、`internal/jobs/loop_test.go`、`internal/server/jobs.go` 需要 gofmt 后即停止；这些是本次范围外的既有开发文件，没有修改或格式化。本次不继续执行代码测试，文档交付只以以上结构、追踪和链接检查为证据。

新增实施入口见 [M1S 选股实施](implementation/m1-screening.md) 与 [M2R 手动历史复盘实施](implementation/m2-manual-replay.md)。真实数据、PIT、选股计算、人工订单、账本、恢复和浏览器场景仍待后续功能实施与对应门禁验证。
