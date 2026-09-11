# ADR-0004：前端工具链（IMP-08）

- 状态：Accepted
- 日期：2026-09-12
- 关联：IMP-08（已批准）、前端实施文档

## 背景

React + TypeScript + Vite + Ant Design + ECharts 已定；路由、服务端状态库、测试框架、TS 客户端工具的具体包和版本须在 Web 初始化前锁定。2026-09 生态版本核查结果见下。

## 决策

| 位置 | 选择 | 版本 | 说明 |
| --- | --- | --- | --- |
| 路由 | `react-router` | ^7.18.3 | 维护者批准 v7；v8 已发布但未批准，锁定 v7 线 |
| 服务端状态 | `@tanstack/react-query` | ^5.102.8 | 缓存、重试、SSE 配合的既定选型 |
| UI 组件 | `antd` | ^6.6.3 | v6 官方 peer 要求 react ≥18，与 React 19.3 兼容 |
| 图表 | `echarts` | ^6.1.0 | Apache-2.0 |
| 构建 | `vite` + `@vitejs/plugin-react` | ^8.3.0 / ^6.1.1 | |
| TypeScript | `typescript` | ~5.9.3 | 锁 5.9 线；TS 7（原生编译器）尚未被全部工具链适配，采用保守版本 |
| 测试 | `vitest` + `@testing-library/*` + `jsdom` | ^5.0.0 等 | vitest 5 官方 peer 支持 vite 8 |
| TS 客户端 | `openapi-typescript` + `openapi-fetch` | ^7.13.0 / ^0.17.0 | 由 `docs/api/openapi.json` 生成 `web/src/api/schema.d.ts`（提交入库），运行时用轻量 fetch 客户端 |

全部依赖许可证为 MIT（ECharts 为 Apache-2.0）。版本以 `web/package.json` + `package-lock.json` 为准；升级须重新验证构建与测试并更新本 ADR。

## 后果

- `npm run gen:api` 从 OpenAPI 生成类型；`scripts/verify.ps1` 重新生成并检查无未提交差异。
- React Router 升级到 v8 属破坏性变更，需新的 ADR。
