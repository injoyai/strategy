# 交易系统骨架（Golang 后端）

- 后端：Go 1.21，标准库 `net/http`，内置回测与策略注册
- 前端建议：React + TypeScript + Vite + Ant Design + ECharts
- 目标：策略回测、选股、指标评估，后续可扩展实盘

## 快速开始

- 启动后端

```bash
go run ./cmd/server
```

- 查看策略与可用标的

```bash
curl http://localhost:8080/api/strategies
```

- 运行回测

```bash
curl -X POST http://localhost:8080/api/backtest \
  -H "Content-Type: application/json" \
  -d '{"strategy":"sma_cross","symbol":"DEMO","start":"2024-01-02","end":"2024-02-02","cash":100000,"size":10}'
```

## 项目结构

- 后端入口：[main.go](file:///d:/GOPATH/src/github.com/injoyai/strategy/cmd/server/main.go)
- HTTP 接口：[server.go](file:///d:/GOPATH/src/github.com/injoyai/strategy/internal/api/server.go)
- 数据与样例：[data.go](file:///d:/GOPATH/src/github.com/injoyai/strategy/internal/data/data.go)、[DEMO.csv](file:///d:/GOPATH/src/github.com/injoyai/strategy/internal/data/samples/DEMO.csv)
- 策略接口与示例：[strategy.go](file:///d:/GOPATH/src/github.com/injoyai/strategy/internal/strategy/strategy.go)、[sma.go](file:///d:/GOPATH/src/github.com/injoyai/strategy/internal/strategy/sma.go)
- 回测引擎：[backtest.go](file:///d:/GOPATH/src/github.com/injoyai/strategy/internal/engine/backtest.go)

## 已提供接口

- GET `/api/strategies` 返回策略列表与样例标的
- POST `/api/backtest` 执行回测
  - 请求：`{strategy,symbol,start,end,cash,size}`
  - 响应：权益曲线、交易列表、收益率、回撤、Sharpe

## 前端建议

- React + TypeScript + Vite
- 组件库：Ant Design
- 图表：ECharts 或 Recharts
- 页面：
  - 策略管理（列表、创建、参数）
  - 回测运行（选择策略/标的/区间/参数、运行、查看结果）
  - 选股器（条件筛选、因子打分、导出）
  - 结果可视化（权益曲线、回撤、交易点、指标表）

## 后续功能补充建议

- 数据源适配器：行情、财务、指数，支持 A 股/美股/加密
- 交易成本与滑点、分红与拆股处理
- 组合与风控：持仓限制、止盈止损、再平衡
- 批量回测与参数网格搜索、并行执行
- 选股因子库与打分器、模型集成
- 实盘接口：掘金、同花顺、IB、币安，模拟盘
- 事件总线与时间驱动引擎
- 持久化与缓存：SQLite/Postgres + Redis
- 报告导出：HTML/CSV/Excel
- 鉴权与多用户、任务队列与调度
- 实验追踪：策略版本与结果快照

## 扩展点

- 策略注册：实现 `Strategy` 接口并调用 `Register`
- 数据接入：实现 Loader 并替换 `data.Load`
- 引擎扩展：订单类型、手续费模型、仓位管理

