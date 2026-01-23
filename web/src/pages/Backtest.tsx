import React, { useEffect, useState } from 'react'
import { Card, Form, Select, DatePicker, InputNumber, Button, Space, Statistic, Row, Col, message, Table, Checkbox, Tabs } from 'antd'
import dayjs from 'dayjs'
import PriceChart from '../components/PriceChart'
import { getStrategies, getCodes, backtest, grid, getKlines, backtestAll, backtestAllWS } from '../lib/api'
import { useRef } from 'react'

export default function BacktestPage() {
  const [loading, setLoading] = useState(false)
  const [strategies, setStrategies] = useState<string[]>([])
  const [symbols, setSymbols] = useState<{ code: string, name: string }[]>([])
  const [activeTab, setActiveTab] = useState<'all' | 'single'>('all')
  const [equity, setEquity] = useState<number[]>([])
  const [cash, setCash] = useState<number[]>([])
  const [candles, setCandles] = useState<any[]>([])
  const [metrics, setMetrics] = useState<{ret?: number, dd?: number, sharpe?: number}>({})
  const [trades, setTrades] = useState<{ index: number, side: string, price: number }[]>([])
  const [gridData, setGridData] = useState<{ fast: number, slow: number, return: number, sharpe: number, max_drawdown: number }[]>([])
  const [form] = Form.useForm()
  const [screenList, setScreenList] = useState<{ code: string, name: string, return: number, max_drawdown: number, sharpe: number }[]>([])
  const [showBuy, setShowBuy] = useState(true)
  const [showSell, setShowSell] = useState(true)
  const [showReturns, setShowReturns] = useState(true)
  const [showMA, setShowMA] = useState(true)
  const [showBoll, setShowBoll] = useState(false)
  const [showVertex, setShowVertex] = useState(true)
  const [showVertex6, setShowVertex6] = useState(false)
  const [indexSymbol, setIndexSymbol] = useState<string>('SSE')
  const [indexCandles, setIndexCandles] = useState<any[]>([])
  const wsRef = useRef<WebSocket | null>(null)

  useEffect(() => {
    (async () => {
      try {
        const strats = await getStrategies()
        const codes = await getCodes()
        setStrategies(strats)
        setSymbols(codes)
        form.setFieldsValue({ strategy: strats[0], code: codes[0]?.code, cash: 100000, size: 10, fee_rate: 0.5, min_fee: 5 })
      } catch {
        const syms = ['DEMO','DEMO2','TRENDUP','TRENDDOWN','RANGE']
        const strats = ['sma_cross','rsi']
        setSymbols(syms.map(s => ({ code: s, name: s })))
        setStrategies(strats)
        form.setFieldsValue({ strategy: strats[0], code: syms[0], cash: 100000, size: 10, fee_rate: 0.5, min_fee: 5 })
      }
    })()
  }, [])

  async function onRunForCode(code: string) {
    await onRunCode(code)
  }

  async function onRun() {
    const v = await form.validateFields()
    try {
      if (wsRef.current) {
        try { wsRef.current.close() } catch {}
        wsRef.current = null
      }
      setScreenList([])
      setMetrics({})
      setLoading(true)
      const ws = backtestAllWS({
        strategy: v.strategy,
        start: v.range?.[0] ? v.range[0].format('YYYY-MM-DD') : undefined,
        end: v.range?.[1] ? v.range[1].format('YYYY-MM-DD') : undefined,
        cash: v.cash,
        size: v.size,
        fee_rate: typeof v.fee_rate === 'number' ? v.fee_rate / 10000 : undefined,
        min_fee: v.min_fee,
        slippage: v.slippage,
        stop_loss: v.stop_loss,
        take_profit: v.take_profit,
      })
      wsRef.current = ws
      ws.onopen = () => {}
      ws.onmessage = (ev) => {
        try {
          const msg = JSON.parse(String(ev.data || '{}'))
          if (msg.type === 'item' && msg.item) {
            setScreenList(prev => {
              const next = [...prev, msg.item]
              return next
            })
          } else if (msg.type === 'summary') {
            setMetrics({ ret: msg.avg_return, dd: msg.avg_max_drawdown, sharpe: msg.avg_sharpe })
            setLoading(false)
            try { ws.close() } catch {}
            wsRef.current = null
          }
        } catch {}
      }
      ws.onerror = () => {
        setLoading(false)
        message.error('回测WS连接失败')
      }
      ws.onclose = () => {
        setLoading(false)
      }
    } catch (e: any) {
      setLoading(false)
      message.error(e?.message || '回测失败')
    }
  }

  async function onRunCode(code: string) {
    const v = await form.getFieldsValue()
    setLoading(true)
    try {
      const res = await backtest({
        strategy: v.strategy,
        code: code,
        start: v.range?.[0] ? v.range[0].format('YYYY-MM-DD') : undefined,
        end: v.range?.[1] ? v.range[1].format('YYYY-MM-DD') : undefined,
        cash: v.cash,
        size: v.size,
        fee_rate: typeof v.fee_rate === 'number' ? v.fee_rate / 10000 : undefined,
        min_fee: v.min_fee,
        slippage: v.slippage,
        stop_loss: v.stop_loss,
        take_profit: v.take_profit,
      })
      setEquity(res.equity)
      setCash(res.cash)
      setMetrics({ ret: res.return, dd: res.max_drawdown, sharpe: res.sharpe })
      setTrades(res.trades.map((t: any) => ({ index: t.index, side: t.side, price: t.price })))
      const c = await getKlines({
        code: code,
        start: v.range?.[0] ? v.range[0].format('YYYY-MM-DD') : undefined,
        end: v.range?.[1] ? v.range[1].format('YYYY-MM-DD') : undefined,
      })
      setCandles(c)
      try {
        const ic = await getKlines({
          code: indexSymbol,
          start: v.range?.[0] ? v.range[0].format('YYYY-MM-DD') : undefined,
          end: v.range?.[1] ? v.range[1].format('YYYY-MM-DD') : undefined,
        })
        setIndexCandles(ic)
      } catch {
        setIndexCandles([])
      }
    } catch (e: any) {
      message.error(e?.message || '个股回测失败')
    } finally {
      setLoading(false)
    }
  }

  async function onGridRun() {
    const v = await form.validateFields()
    setLoading(true)
    try {
      const res = await grid({
        code: v.code,
        start: v.range?.[0] ? v.range[0].format('YYYY-MM-DD') : undefined,
        end: v.range?.[1] ? v.range[1].format('YYYY-MM-DD') : undefined,
        cash: v.cash,
        size: v.size,
        fast_min: v.fast_min || 3,
        fast_max: v.fast_max || 10,
        slow_min: v.slow_min || 15,
        slow_max: v.slow_max || 50,
        step: v.step || 1,
        top_k: 10
      })
      setGridData(res)
    } catch (e: any) {
      message.error(e?.message || '网格回测失败')
    } finally {
      setLoading(false)
    }
  }

  async function onScreenerRun() {}

  function renderGlobalChart() {}

  return (
    <Space direction="vertical" style={{ width: '100%' }} size="large">
      <Card title="回测参数">
        <Tabs
          activeKey={activeTab}
          onChange={k => setActiveTab(k as 'all' | 'single')}
          items={[
            {
              key: 'all',
              label: '全市场回测',
              children: (
                <Form form={form} layout="vertical">
                  <Row gutter={[16, 8]}>
                    <Col xs={24} md={8}>
                      <Form.Item name="strategy" label="策略" rules={[{ required: true }]}>
                        <Select size="small" style={{ width: '100%' }} options={strategies.map(s => ({ value: s, label: s }))} />
                      </Form.Item>
                    </Col>
                    <Col xs={24} md={8}>
                      <Form.Item name="range" label="区间">
                        <DatePicker.RangePicker size="small" style={{ width: '100%' }} disabledDate={d => d.isAfter(dayjs('2025-12-31'))} />
                      </Form.Item>
                    </Col>
                  </Row>
                  <Row gutter={[16, 8]}>
                    <Col xs={24} md={6}>
                      <Form.Item name="cash" label="资金">
                        <InputNumber size="small" min={1000} step={1000} style={{ width: '100%' }} />
                      </Form.Item>
                    </Col>
                    <Col xs={24} md={6}>
                      <Form.Item name="size" label="手数">
                        <InputNumber size="small" min={1} style={{ width: '100%' }} />
                      </Form.Item>
                    </Col>
                    <Col xs={24} md={6}>
                      <Form.Item name="fee_rate" label="手续费(万)">
                        <InputNumber size="small" min={0} max={1} step={0.01} placeholder="万5填0.5" style={{ width: '100%' }} />
                      </Form.Item>
                    </Col>
                    <Col xs={24} md={6}>
                      <Form.Item name="min_fee" label="最低手续费(元)">
                        <InputNumber size="small" min={0} step={1} placeholder="默认5元" style={{ width: '100%' }} />
                      </Form.Item>
                    </Col>
                  </Row>
                  <Row gutter={[16, 8]}>
                    <Col xs={24} md={8}>
                      <Form.Item name="slippage" label="滑点">
                        <InputNumber size="small" min={0} max={0.05} step={0.0005} style={{ width: '100%' }} />
                      </Form.Item>
                    </Col>
                    <Col xs={24} md={8}>
                      <Form.Item name="stop_loss" label="止损">
                        <InputNumber size="small" min={0} max={0.2} step={0.001} style={{ width: '100%' }} />
                      </Form.Item>
                    </Col>
                    <Col xs={24} md={8}>
                      <Form.Item name="take_profit" label="止盈">
                        <InputNumber size="small" min={0} max={1} step={0.001} style={{ width: '100%' }} />
                      </Form.Item>
                    </Col>
                  </Row>
                  <Row gutter={[16, 8]} align="middle">
                    <Col xs={24} md={16}>
                      <Space wrap>
                        <Button size="small" type="primary" onClick={onRun} loading={loading}>全市场回测</Button>
                      </Space>
                    </Col>
                  </Row>
                </Form>
              )
            },
            {
              key: 'single',
              label: '个股回测',
              children: (
                <Form form={form} layout="vertical">
                  <Row gutter={[16, 8]}>
                    <Col xs={24} md={8}>
                      <Form.Item name="strategy" label="策略" rules={[{ required: true }]}>
                        <Select size="small" style={{ width: '100%' }} options={strategies.map(s => ({ value: s, label: s }))} />
                      </Form.Item>
                    </Col>
                    <Col xs={24} md={8}>
                      <Form.Item name="code" label="股票" rules={[{ required: true }]}>
                        <Select
                          size="small"
                          style={{ width: '100%' }}
                          showSearch
                          placeholder="搜索股票"
                          filterOption={(input, option) =>
                            String(option?.value || '').toLowerCase().includes(String(input).toLowerCase()) ||
                            String(option?.label || '').toLowerCase().includes(String(input).toLowerCase())
                          }
                          options={symbols.map(s => ({ value: s.code, label: `${s.name}-${s.code}` }))}
                        />
                      </Form.Item>
                    </Col>
                    <Col xs={24} md={8}>
                      <Form.Item name="range" label="区间">
                        <DatePicker.RangePicker size="small" style={{ width: '100%' }} disabledDate={d => d.isAfter(dayjs('2025-12-31'))} />
                      </Form.Item>
                    </Col>
                  </Row>
                  <Row gutter={[16, 8]}>
                    <Col xs={24} md={6}>
                      <Form.Item name="cash" label="资金">
                        <InputNumber size="small" min={1000} step={1000} style={{ width: '100%' }} />
                      </Form.Item>
                    </Col>
                    <Col xs={24} md={6}>
                      <Form.Item name="size" label="手数">
                        <InputNumber size="small" min={1} style={{ width: '100%' }} />
                      </Form.Item>
                    </Col>
                    <Col xs={24} md={6}>
                      <Form.Item name="fee_rate" label="手续费(万)">
                        <InputNumber size="small" min={0} max={1} step={0.01} placeholder="万5填0.5" style={{ width: '100%' }} />
                      </Form.Item>
                    </Col>
                    <Col xs={24} md={6}>
                      <Form.Item name="min_fee" label="最低手续费(元)">
                        <InputNumber size="small" min={0} step={1} placeholder="默认5元" style={{ width: '100%' }} />
                      </Form.Item>
                    </Col>
                  </Row>
                  <Row gutter={[16, 8]}>
                    <Col xs={24} md={8}>
                      <Form.Item name="slippage" label="滑点">
                        <InputNumber size="small" min={0} max={0.05} step={0.0005} style={{ width: '100%' }} />
                      </Form.Item>
                    </Col>
                    <Col xs={24} md={8}>
                      <Form.Item name="stop_loss" label="止损">
                        <InputNumber size="small" min={0} max={0.2} step={0.001} style={{ width: '100%' }} />
                      </Form.Item>
                    </Col>
                    <Col xs={24} md={8}>
                      <Form.Item name="take_profit" label="止盈">
                        <InputNumber size="small" min={0} max={1} step={0.001} style={{ width: '100%' }} />
                      </Form.Item>
                    </Col>
                  </Row>
                  <Row gutter={[16, 8]} align="middle">
                    <Col xs={24} md={16}>
                      <Space wrap>
                        <Button size="small" type="primary" onClick={() => onRunCode(form.getFieldValue('code'))} loading={loading}>个股回测</Button>
                        <Button size="small" onClick={onGridRun} loading={loading}>SMA 网格回测</Button>
                      </Space>
                    </Col>
                  </Row>
                </Form>
              )
            }
          ]}
        />
      </Card>
      {activeTab === 'all' && (
      <Card title="全局回测汇总">
        <Space style={{ marginBottom: 12 }}>
          <span>指数</span>
          <Select
            style={{ width: 160 }}
            value={indexSymbol}
            onChange={async (val) => {
              setIndexSymbol(val)
              try {
                const v = form.getFieldsValue()
                const ic = await getKlines({
                  code: val,
                  start: v.range?.[0] ? v.range[0].format('YYYY-MM-DD') : undefined,
                  end: v.range?.[1] ? v.range[1].format('YYYY-MM-DD') : undefined,
                })
                setIndexCandles(ic)
              } catch {
                setIndexCandles([])
              }
            }}
            options={[
              { value: 'SSE', label: '上证指数' },
              { value: 'CSI300', label: '沪深300' },
            ]}
          />
        </Space>
        <Row gutter={24} style={{ marginTop: 12 }}>
          <Col span={8}><Statistic title="整体收益(平均)" value={metrics.ret ? metrics.ret * 100 : 0} suffix="%" precision={2} /></Col>
          <Col span={8}><Statistic title="整体回撤(平均)" value={metrics.dd ? metrics.dd * 100 : 0} suffix="%" precision={2} /></Col>
          <Col span={8}><Statistic title="整体Sharpe(平均)" value={metrics.sharpe || 0} precision={2} /></Col>
        </Row>
      </Card>
      )}
      <Row gutter={[16,16]}>
        {activeTab === 'all' && (
          <Col span={8}>
            <Card title="股票列表">
              <Space style={{ marginBottom: 12 }}>点击查看右侧该股票回测结果</Space>
              <Table
                size="small"
                rowKey="code"
                dataSource={screenList}
                onRow={r => ({ onClick: () => onRunForCode(r.code) })}
                pagination={{ pageSize: 8 }}
                columns={[
                  { title: '标的', dataIndex: 'name', render: (_: any, r: any) => `${r.name}-${r.code}` },
                  { title: '收益', dataIndex: 'return', render: (v: number) => `${(v*100).toFixed(2)}%` },
                  { title: '回撤', dataIndex: 'max_drawdown', render: (v: number) => `${(v*100).toFixed(2)}%` },
                  { title: 'Sharpe', dataIndex: 'sharpe', render: (v: number) => (typeof v === 'number' ? v.toFixed(2) : v) },
                ]}
              />
            </Card>
          </Col>
        )}
        <Col span={activeTab === 'all' ? 16 : 24}>
          <Card title="个股回测">
            <Space style={{ marginBottom: 12 }}>
              <Checkbox checked={showBuy} onChange={e => setShowBuy(e.target.checked)}>买点</Checkbox>
              <Checkbox checked={showSell} onChange={e => setShowSell(e.target.checked)}>卖点</Checkbox>
              <Checkbox checked={showReturns} onChange={e => setShowReturns(e.target.checked)}>收益</Checkbox>
              <Button size="small" type={showMA ? 'primary' : 'default'} onClick={() => setShowMA(!showMA)}>均线</Button>
              <Button size="small" type={showBoll ? 'primary' : 'default'} onClick={() => setShowBoll(!showBoll)}>布林带</Button>
              <Button size="small" type={showVertex6 ? 'primary' : 'default'} onClick={() => setShowVertex6(!showVertex6)}>顶点(6)</Button>
              <Button size="small" type={showVertex ? 'primary' : 'default'} onClick={() => setShowVertex(!showVertex)}>顶点(8)</Button>
            </Space>
            <PriceChart candles={candles} trades={trades} equity={equity} showBuy={showBuy} showSell={showSell} showReturns={showReturns} showMA={showMA} showBollinger={showBoll} showVertex={showVertex} showVertex6={showVertex6} />
            <Row gutter={24} style={{ marginTop: 12 }}>
              <Col span={8}><Statistic title="累计收益" value={metrics.ret ? metrics.ret * 100 : 0} suffix="%" precision={2} /></Col>
              <Col span={8}><Statistic title="最大回撤" value={metrics.dd ? metrics.dd * 100 : 0} suffix="%" precision={2} /></Col>
              <Col span={8}><Statistic title="Sharpe" value={metrics.sharpe || 0} precision={2} /></Col>
            </Row>
          </Card>
        </Col>
      </Row>
      <Card title="SMA 网格结果">
        <Row gutter={[12,12]}>
          {gridData.map((g, i) => (
            <Col key={i} span={12}>
              <Card size="small">
                <Space>
                  <span>Fast {g.fast}</span>
                  <span>Slow {g.slow}</span>
                  <span>收益 {(g.return*100).toFixed(2)}%</span>
                  <span>Sharpe {g.sharpe.toFixed(2)}</span>
                  <span>回撤 {(g.max_drawdown*100).toFixed(2)}%</span>
                </Space>
              </Card>
            </Col>
          ))}
        </Row>
      </Card>
    </Space>
  )
}
