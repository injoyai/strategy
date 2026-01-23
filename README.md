# 📈 Quantitative Trading Strategy System (量化交易策略系统)

[![Go](https://img.shields.io/badge/Go-1.21+-00ADD8?logo=go&logoColor=white)](https://golang.org/)
[![React](https://img.shields.io/badge/React-18+-61DAFB?logo=react&logoColor=black)](https://reactjs.org/)
[![Vite](https://img.shields.io/badge/Vite-5+-646CFF?logo=vite&logoColor=white)](https://vitejs.dev/)
[![Ant Design](https://img.shields.io/badge/Ant%20Design-5+-0170FE?logo=antdesign&logoColor=white)](https://ant.design/)

一个功能强大的量化交易策略回测与选股系统，集成了高性能 Go 后端与现代 React 前端。支持 TDX 数据源、自定义策略组合、实时回测分析及可视化图表。

## ✨ 主要功能 (Features)

### 📊 智能选股 (Screener)
- **多策略组合**：支持同时选择多个策略（如均线交叉 + RSI），实现更精细的筛选。
- **可视化分析**：内置 K 线图表，自动标记买卖点信号，支持均线/布林带叠加显示。
- **关键指标**：直观展示股票的**换手率**、**总市值**、评分及买卖信号。
- **数据导出**：支持将筛选结果导出为 CSV 文件，便于进一步分析。

### 🚀 策略回测 (Backtest)
- **全历史回测**：基于高质量历史数据进行策略验证。
- **绩效评估**：提供资金曲线、年化收益率、最大回撤、夏普比率等专业指标。
- **仿真模拟**：支持自定义初始资金、交易费用、滑点等参数。

### 🧩 策略管理
- **内置策略库**：包含 SMA、MACD、RSI、布林带等经典技术指标策略。
- **灵活配置**：支持动态调整策略参数。
- **脚本扩展**：支持脚本化定义新策略（部分支持）。

### 📉 行情数据
- **TDX 数据源**：无缝对接通达信数据，覆盖 A 股全市场。
- **高性能架构**：优化的数据读取与缓存机制，毫秒级响应。

## 🛠️ 技术栈 (Tech Stack)

- **Backend**: Go (Golang), Gin/FBR Framework
- **Frontend**: React, TypeScript, Vite, Ant Design, ECharts
- **Data**: TDX Protocol, SQLite/MySQL
- **Build**: Vite (Web), Go Build (Server)

## 🚀 快速开始 (Quick Start)

### 前置要求
- Go 1.21 或更高版本
- Node.js 18+ (包含 npm)

### 1. 编译前端
进入 `web` 目录并构建前端资源：

```bash
cd web
npm install
npm run build
# Windows PowerShell 用户若遇权限问题可尝试: npm.cmd run build
```
> 前端构建产物 (`web/dist`) 将通过 Go embed 自动嵌入到后端二进制文件中。

### 2. 编译与运行后端
回到项目根目录：

```bash
cd ..
go mod tidy
go build -o strategy.exe ./cmd/server

# 启动服务
./strategy.exe
```

启动成功后，浏览器访问：[http://localhost:8080](http://localhost:8080)

## 📖 使用指南 (Usage)

### 选股器使用
1. 导航至 **“选股”** 页面。
2. 在 **“策略”** 下拉框中选择一个或多个策略。
3. （可选）设置回测时间范围。
4. 点击 **“运行选股”** 按钮。
5. 结果表格中可以查看每只股票的 **换手率** 和 **市值**，点击表头可进行排序。
6. 点击结果卡片中的图表可查看详细 K 线与买卖点。

### 常见问题
- **页面空白？** 
  - 请确保后端已重新编译（已包含 Windows MIME 类型修复）。
  - 确保使用的是 `HashRouter`（前端已默认配置）。
- **数据加载慢？**
  - 首次运行可能需要初始化本地数据缓存，后续运行将显著提速。

## 📁 项目结构

```
strategy/
├── cmd/server/         # 后端服务入口
├── internal/
│   ├── api/            # HTTP API 路由与处理
│   ├── data/           # 数据层与 TDX 接口
│   ├── strategy/       # 策略逻辑实现
│   └── screener/       # 选股器核心逻辑
├── web/                # 前端 React 项目
│   ├── src/            # 前端源代码
│   ├── dist/           # 构建后的静态文件
│   └── vite.config.ts  # Vite 配置
└── data/               # 本地数据文件存储
```

---

Built with ❤️ by InjoyAI Strategy Team.
