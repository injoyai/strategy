import React from 'react'
import ReactDOM from 'react-dom/client'
import { BrowserRouter, Routes, Route, Link } from 'react-router-dom'
import { Layout, Menu } from 'antd'
import { LineChartOutlined } from '@ant-design/icons'
import BacktestPage from './pages/Backtest'
import StrategyPage from './pages/Strategy'
import ScreenerPage from './pages/Screener'
import MarketPage from './pages/Market'
import 'antd/dist/reset.css'

const { Header, Content } = Layout

function App() {
  return (
    <BrowserRouter>
      <Layout style={{ minHeight: '100vh' }}>
        <Header style={{ display: 'flex', alignItems: 'center' }}>
          <div style={{ color: '#fff', fontWeight: 600, marginRight: 24, fontSize: '18px', display: 'flex', alignItems: 'center', gap: '8px' }}>
            <LineChartOutlined />
            Strategy
          </div>
          <Menu theme="dark" mode="horizontal" items={[
            { key: 'screener', label: <Link to="/">选股</Link> },
            { key: 'backtest', label: <Link to="/backtest">回测</Link> },
            { key: 'strategy', label: <Link to="/strategy">策略</Link> },
            { key: 'market', label: <Link to="/market">行情</Link> },
          ]} />
        </Header>
        <Content style={{ padding: 24 }}>
          <Routes>
            <Route path="/" element={<ScreenerPage />} />
            <Route path="/backtest" element={<BacktestPage />} />
            <Route path="/strategy" element={<StrategyPage />} />
            <Route path="/screener" element={<ScreenerPage />} />
            <Route path="/market" element={<MarketPage />} />
          </Routes>
        </Content>
      </Layout>
    </BrowserRouter>
  )
}

ReactDOM.createRoot(document.getElementById('root')!).render(
  <React.StrictMode>
    <App />
  </React.StrictMode>
)
