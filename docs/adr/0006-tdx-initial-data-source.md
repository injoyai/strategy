# ADR-0006：TDX 作为首个真实数据源

- 状态：Accepted（有界范围）
- 日期：2026-09-14
- 关联：DEC-01（Partial）、DEC-02（Partial）、IMP-03（SQLite 驱动兼容修订）、M1-01/02

## 背景

当前没有第二个数据源。用户批准将 `github.com/injoyai/tdx` 接入系统，但公共 TDX 7709 协议不是带服务等级、数据授权和历史修订承诺的供应商接口。因此本决策要同时做到两件事：让系统能够实际采集数据，并阻止“能拉到行情”被误读为“已经具备可证明的历史时点正确性”。

2026-09-14 的脱敏探针通过 `124.71.187.122:7709` 成功获取 `bj920992` 的 20 根日线。本地协议/拉取测试通过；接入固定上游提交 `2b3dcae30c42cae1f5e2f3a33359d12b761ae7fe`，不跟随浮动分支。

## 决策

1. 首个真实 Provider 为 `tdx@v1-2b3dcae`，当前只支持 A 股股票的：
   - `instrument/static`：当前代码表中的证券代码、名称、MIC、资产类别和币种；上市时间缺失并给出原因。
   - `calendar/daily`：由上证综指日线存在性推导的常规 09:30..15:00 交易日。
   - `bar/daily`：协议日线 OHLC、成交量和成交额；不声明复权或公司行为处理。
2. 协议价格/成交额的厘值转成 3 位十进制元；股票成交量从手转成股。协议日期按 `Asia/Shanghai` 墙钟重建，不使用宿主本地时区。
3. Provider 页保存原始 payload checksum、抓取时刻和游标证据；Normalizer 的 schema/policy/revision 标识均固定版本。内容行哈希作为本次观测的稳定 revision id，但不冒充上游修订号。
4. 所有 TDX capability 均声明 `PITLevel=unverified`。上游未提供权威 `published_at`、历史可用时刻或 supersedes 链，因此保留 `published_at=null`；本地抓取时刻只表示 first-seen `available_at/ingested_at`。严格 PIT 发布必须拒绝，探索性非严格快照持续携带 `quality.pit_unverified`。
5. 连接配置只接受 `host:7709`、去重的有序节点和 100..60000ms timeout；不记录凭据。默认节点来自锁定的依赖版本，生产使用前仍需独立节点健康、超时、限流和容量验证。
6. 引入 TDX 后，进程同时链接 TDX 的 `github.com/glebarez/go-sqlite` 与原有 `modernc.org/sqlite` blank import 会因双方注册 `sqlite` driver name 而启动 panic。应用统一改由 `glebarez/go-sqlite` 注册 driver name，同时显式锁定 `modernc.org/sqlite v1.58.0` 运行时；现有 DSN/WAL/store 行为必须由原测试套件持续验证。

## 未解锁的事项

- DEC-01 仍缺权威交易日历、复权/公司行为和可交易市场口径；由指数日线推导的日历不可用于声称交易所级完整性。
- DEC-02 仍缺数据许可（缓存、展示、导出、共享、保留期）、服务等级、限流、完整历史覆盖和历史修订证据。
- TDX 不能支撑严格 PIT 回测；在获得可证明的发布/修订数据源前，历史结果只能标为探索性，不能升级为 M1 完成证据。
- 当前没有基本面、财务公告、历史成分、停复牌状态、公司行为或权威复权因子；依赖这些输入的因子、选股与回测继续 fail closed。

## 验证

- 离线契约测试覆盖：配置拒绝、能力声明、A 股代码边界、分页/游标、价格与成交量单位、Asia/Shanghai 时间、Normalizer schema/provenance 与 `pit_unverified`。
- 现有 store/data/jobs/pipeline/server 测试覆盖 SQLite 注册冲突修复及采集编排兼容性。
- 联网验证由 `TDX_LIVE=1 go test ./internal/tdxprovider -run TestLiveTDXDailyBar -v` 显式触发；默认测试不依赖公共节点。
