# ADR-0003：Go 依赖选型（十进制、路由、迁移、日志、驱动、配置）

- 状态：Accepted
- 日期：2026-09-12
- 关联：IMP-04（已批准）、IMP-05（本范围已批准）、GATE-M0-START 最小样例验证

## 背景

M0-01 要求固定初始生产依赖并完成许可证、安全、Windows/目标平台与最小样例验证。原则（实施总纲 §7）：优先标准库和小依赖；新依赖先做最小兼容性验证并记录 ADR。

## 决策与验证证据

| 位置 | 选择 | 许可证 | 验证证据 |
| --- | --- | --- | --- |
| 十进制定点 | `github.com/shopspring/decimal v1.4.0` | MIT | `internal/deps_smoke`：精确加法 `0.1+0.2=0.3`、JSON 字符串编解码、拒绝 NaN/Inf/逗号/空串。领域层将包装为 `Money/Price/Quantity`，不泄漏驱动类型（M0-02）。注意：库本身接受 `1e999` 指数形式，拒绝指数是 HTTP 边界解析器职责 |
| HTTP 路由 | 标准库 `net/http`（Go 1.22+ 方法+路径模式） | Go 许可 | 标准库无新增依赖；M0-03 落地中间件链 |
| 元数据迁移 | `github.com/pressly/goose/v3 v3.28.0` | MIT | `internal/deps_smoke`：对 modernc SQLite 执行嵌入 SQL 迁移 up 成功并建表 |
| 日志 | 标准库 `log/slog` | Go 许可 | `internal/logging`：JSON/文本 handler + 稳定字段约定 |
| SQLite 驱动 | `github.com/glebarez/go-sqlite v1.22.0` + `modernc.org/sqlite v1.58.0` | MIT + BSD-3 | ADR-0006 将 driver-name 注册统一到前者、纯 Go 运行时继续显式锁定后者；`internal/deps_smoke` 与 store/data/jobs 测试验证 WAL、busy_timeout、读写及现有存储语义（Windows/amd64 无 CGO） |
| 配置格式 | `encoding/json` 配置文件 + `RESEARCHD_*` 环境变量覆盖 | Go 许可 | `internal/config`：优先级 defaults→file→env；错误含字段与来源；无 secret 入配置 |

## 后果

- `go.mod`/`go.sum` 成为版本事实源；升级需过最小样例验证并更新本 ADR。
- 领域包禁止导入以上适配库（shopspring/decimal 仅在领域包装类型内部使用）；架构规则由包导入检查与评审保证。
- OpenAPI 生成与 TypeScript 客户端工具见 ADR-0004。
