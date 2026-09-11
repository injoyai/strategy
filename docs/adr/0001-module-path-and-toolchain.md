# ADR-0001：Go module path 与工具链

- 状态：Accepted
- 日期：2026-09-12
- 关联：IMP-01、IMP-02（已批准）；GATE-M0-START

## 背景

实施总纲要求在写任何 Go 代码前批准真实、长期可控的 module path（禁止 `example.com` 占位后扩散），并固定 Go 工具链约束。本仓库无既有 Git remote，路径由维护者于 2026-09-12 明确提供。

## 决策

1. Go module path 固定为 `github.com/injoyai/strategy`。
2. `go.mod` 的 `go` 指令为 `1.26.0`，并依赖 Go 标准工具链机制（默认 `GOTOOLCHAIN=auto`）按 go.mod 中 `toolchain` 行自动获取精确工具链；不要求开发者手动安装特定版本。
3. 采用单仓库单 module；服务代码位于 `cmd/` 与 `internal/`，领域包保持 internal（与 [Go 官方 module 布局建议](https://go.dev/doc/modules/layout) 一致）。未来确需跨仓库共享时再拆分 module。

## 后果

- 全新 checkout 只需安装任意较新 Go（≥1.26 发行版或启用自动工具链），`go build/test` 即可复现，满足 M0-01 验收。
- 所有包导入路径自始固定，避免后续全库重命名。
- 选择 `pressly/goose/v3 v3.28.0`（要求 go ≥ 1.26）因此可行。
