# ADR-0002：M0 部署假设与元数据存储

- 状态：Accepted
- 日期：2026-09-12
- 关联：DEC-03（M0 假设已确认，最终定案不晚于 GATE-M1-START）、IMP-03（M0 部分）、IMP-07

## 背景

GATE-M0-START 要求确认 M0 的执行假设：按单机/单 Worker 验证，还是直接按团队部署设计。该决定约束元数据库、认证模式与故障注入范围。

## 决策

M0 按以下假设执行（维护者 2026-09-12 批准）：

1. **单机实验部署**：单台机器、单进程二进制 `researchd`，内含 HTTP 与 Worker 两个组件，仅通过持久化 Job 协作，测试中可分别启动/停止（IMP-07 批准：单二进制 + 持久化队列 + 可分离 Worker）。
2. **元数据存储用 SQLite**（IMP-03 的 M0 部分）：
   - `modernc.org/sqlite` 提供纯 Go、无 CGO 的 SQLite 运行时（Windows 友好，BSD-3）；自 ADR-0006 起由 `github.com/glebarez/go-sqlite` 注册应用使用的 `sqlite` driver name，以兼容 TDX 依赖树并避免重复注册 panic。
   - 数据库文件仅允许本机文件系统；启用 WAL 并设置 `busy_timeout`；运行时版本须包含上游已修复的 WAL-reset 问题，由 go.mod 锁定 modernc 版本保证。
3. **认证模式**：M0 支持 `local`（显式声明，强制绑定回环地址，启动日志提示仅限开发）与 `bearer`（令牌文件引用）两种；`local` 不可绑定非回环地址，由配置校验拒绝。

## 后果

- M0-04/05 的迁移、租约、fencing 与崩溃测试按单机 SQLite 设计；不引入 PostgreSQL 依赖。
- `GATE-M1-START` 前必须按 DEC-03 完整定案（数据量、并发、备份恢复目标）；若届时转向团队部署，存储层通过端口隔离，替换为 PostgreSQL 只影响 adapter 与迁移。
- 备份演练需覆盖数据库文件与 WAL（M0-04 交付时验证）。
