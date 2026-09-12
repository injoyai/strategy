# 策略研究平台

Go 后端、可视化研究工作台。当前处于 M0（契约与工程基础）阶段：`researchd` 服务骨架已可运行，HTTP 业务契约与前端工作台按 [实施文档](docs/implementation/README.md) 逐个工作包交付。

## 快速启动

环境要求：Go ≥ 1.26、Node.js ≥ 20、Python 3（仅标准库，用于契约生成检查）。

```powershell
# 运行服务（默认绑定 127.0.0.1:8080，local 认证，SQLite 位于 data/metadata.db）
go run ./cmd/researchd
go run ./cmd/researchd -config configs/researchd.dev.json

# 健康检查
curl http://127.0.0.1:8080/healthz
curl http://127.0.0.1:8080/readyz

# 统一验证入口：gofmt / go vet / go test / 契约生成检查 / 前端 typecheck+build+test
.\scripts\verify.ps1            # -SkipWeb 跳过前端部分

# 前端开发（Vite dev server 代理 /api 到 127.0.0.1:8080）
cd web; npm install; npm run dev
```

配置优先级：内置默认 → JSON 文件（`-config` 或 `RESEARCHD_CONFIG`）→ `RESEARCHD_*` 环境变量。可用字段见 [configs/researchd.dev.json](configs/researchd.dev.json) 与 [internal/config](internal/config/config.go)；错误信息会指出字段名与来源。

> 若本机 npm 对官方源无响应，可一次性加 `--registry=https://registry.npmmirror.com` 安装（锁文件中的 resolved 地址即为实际安装源）。

## 文档入口

| 文档 | 内容 |
| --- | --- |
| [需求说明](docs/requirements.md) | 产品边界、模块、阶段、验收、待确认决策 |
| [接口契约](docs/interfaces.md) | Go 模块边界、HTTP 语义、任务与扩展契约 |
| [Go 接口草案](docs/contracts/contracts.go) | 可编译的内部扩展接口及领域类型，非业务实现 |
| [HTTP OpenAPI](docs/api/openapi.json) | 前后端集成使用的机器可读 API 草案 |
| [因子与数据源清单](docs/factor-data-catalog.md) | K 线、PE、财报等数据需求、因子依赖及供应商评估 |
| [选股功能设计](docs/stock-screening-design.md) | 条件筛选、因子排名、历史时点、结果解释、界面与待接入接口；尚未实现 |
| [历史选股与手动模拟交易](docs/historical-replay-trading-design.md) | 逐日复盘、人工买卖、撮合与账户、恢复和报告；参考成熟项目的设计提案 |
| [界面操作需求](docs/frontend.md) | 前端选型、页面、端到端操作与异常状态 |
| [设计方向](DESIGN.md) | 未来工作台的布局、颜色、排版约定 |
| [实施文档](docs/implementation/README.md) | M0-M3、选股、历史手动复盘、M4 边界、前端、验收追踪与多 Agent 并发规范 |
| [验证记录](docs/verification.md) | 本轮实际检查与未执行的运行验收 |

版本：0.1，2026-09-12。Go 后端为已确认要求；前端方案由本轮选定；首期市场、交易频率、供应商、交易接入范围待确认。文档中的候选数据源不代表已经购买、授权或验证可用。

建议阅读顺序：需求说明 → 因子与数据源清单 → 接口契约 → 界面操作需求 → 实施文档。
