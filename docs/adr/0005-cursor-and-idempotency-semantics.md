# ADR-0005：分页游标与幂等键过期语义统一

- 状态：Accepted
- 日期：2026-09-12
- 关联：IMP-09、GATE-SCREEN-CONTRACT、GATE-REPLAY-CONTRACT

## 背景

`interfaces.md` 的分页与幂等过期语义与已交付实现/测试在两处不一致，选股（M1S）与复盘（M2R）handler 复用这些共享约定前必须先统一（IMP-09）：

1. **游标**：文档写"无效或过期游标返回 400"；实现（`internal/server/pagination.go`）与测试区分结构非法/排序不匹配（400 `validation.invalid`）与超过 24 小时有效期（422 `pagination.cursor_expired`）。
2. **幂等键**：文档要求"过期键返回 409 而不是悄悄再次执行"；实现把过期键当作新请求回收重执行（memory 与 SQLite 存储均如此），并有测试锁定该行为。对有副作用的 POST，静默重执行是安全隐患。

## 决策

1. **游标**：结构非法、版本未知或排序不匹配返回 400 `validation.invalid`；结构合法但超过 24 小时有效期返回 422 `pagination.cursor_expired`。两种情况客户端均从首页重查。实现保持不变，文档按实现修正。
2. **幂等键**：
   - 相同键 + 相同规范化请求在窗口内回放原响应（`Idempotent-Replay: true`）。
   - 相同键 + 不同请求始终返回 409 `idempotency.conflict`（即使键已过期，键摘要保留至工作区删除）。
   - 仍在处理中的键返回 409 `idempotency.conflict`。
   - 超过 24 小时重试窗口的键返回 409 `idempotency.key_expired`，绝不悄悄再次执行；客户端必须换新键。
   - 存储层（memory 与 SQLite）不再回收过期记录；实现按契约修正。
3. 错误码采用点分约定（`pagination.cursor_expired`、`idempotency.key_expired`），`interfaces.md` 中的下划线写法同步更正。

## 后果

- 幂等过期测试反转：过期后同键重试从"重新执行"改为 409，对副作用写入是 fail-closed。
- Commit 失败（响应未持久化）后，同键重试会得到 409 冲突而不是重执行；调用方以新键重试。
- 选股与复盘 handler 通过共享分页与幂等中间件继承同一套语义，不得新增第三套规则。
