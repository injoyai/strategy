import React, { useEffect, useState } from 'react'
import { Card, Form, Select, InputNumber, Button, Table, Tag, Space, message, Switch, Row, Col, DatePicker } from 'antd'
import { getStrategies, screener, getKlines, backtest } from '../lib/api'
import PriceChart from '../components/PriceChart'
import dayjs from 'dayjs'

export default function ScreenerPage() {
  const [strategies, setStrategies] = useState<string[]>([])
  const [auxStrategies, setAuxStrategies] = useState<string[]>([])
  const [data, setData] = useState<any[]>([])
  const [charts, setCharts] = useState<Record<string, { candles: any[], trades: { index: number, side: string }[] }>>({})
  const [loading, setLoading] = useState(false)
  const [form] = Form.useForm()
  const [visibleCount, setVisibleCount] = useState(60)
  const [loadingMore, setLoadingMore] = useState(false)
  const [showMA, setShowMA] = useState(true)
  const [showBoll, setShowBoll] = useState(false)
  const [sorter, setSorter] = useState<{ field?: string, order?: 'ascend' | 'descend' }>({})

  useEffect(() => {
    (async () => {
      try {
        const strats = await getStrategies('custom')
        const auxStrats = await getStrategies('internal')
        setStrategies(strats)
        setAuxStrategies(auxStrats)
        const defaultStart = dayjs().subtract(3, 'month')
        const defaultEnd = dayjs().hour(23).minute(23).second(0)
        form.setFieldsValue({ 
          strategy: strats[0], 
          lookback: 10,
          range: [defaultStart, defaultEnd]
        })
      } catch {
        setStrategies([])
        setAuxStrategies([])
        const defaultStart = dayjs().subtract(3, 'month')
        const defaultEnd = dayjs().hour(23).minute(23).second(0)
        form.setFieldsValue({ 
          lookback: 10,
          range: [defaultStart, defaultEnd]
        })
      }
    })()
  }, [])

  async function onRun() {
    const v = await form.validateFields()
    setLoading(true)
    try {
      const start = v.range?.[0] ? v.range[0].format('YYYY-MM-DD') : undefined
      const end = v.range?.[1] ? v.range[1].format('YYYY-MM-DD') : undefined
      const startTs = v.range?.[0] ? v.range[0].unix() : undefined
      const endTs = v.range?.[1] ? v.range[1].unix() : undefined
      
      const selectedStrategies = []
      if (v.strategy) selectedStrategies.push(v.strategy)
      if (v.aux_strategy) selectedStrategies.push(v.aux_strategy)

      const res = await screener({
        strategies: selectedStrategies,
        lookback: v.lookback,
        start_time: startTs,
        end_time: endTs
      })
      const list = Array.isArray(res) ? res : (res.list || [])
      setData(list)
      setVisibleCount(60)
      const ordered = getOrdered(list)
      await loadChartsFor(ordered.slice(0, 60), selectedStrategies)
    } catch (e: any) {
      message.error(e?.message || '选股失败')
    } finally {
      setLoading(false)
    }
  }

  function getOrdered(items: any[]) {
    if (!Array.isArray(items)) return []
    const { field, order } = sorter
    if (!field || !order) return items
    const arr = [...items]
    const dir = order === 'ascend' ? 1 : -1
    arr.sort((a, b) => {
      const fa = a[field]
      const fb = b[field]
      if (field === 'code' || field === 'name') {
        return String(fa || '').localeCompare(String(fb || '')) * dir
      }
      return (Number(fa) - Number(fb)) * dir
    })
    return arr
  }

  async function loadChartsFor(items: any[], strategiesList: string[]) {
    const nextCharts: Record<string, { candles: any[], trades: { index: number, side: string }[] }> = { ...charts }
    const chunkSize = 6
    const v = form.getFieldsValue()
    // 后端 GetKlines 和 Backtest 接口期望的时间格式是 YYYY-MM-DD
    const start = v.range?.[0] ? v.range[0].format('YYYY-MM-DD') : undefined
    const end = v.range?.[1] ? v.range[1].format('YYYY-MM-DD') : undefined
    
    for (let i = 0; i < items.length; i += chunkSize) {
      const batch = items.slice(i, i + chunkSize)
      const promises = batch.map(async (item) => {
        if (nextCharts[item.code]) return
        try {
          const cs = await getKlines({ code: item.code, start, end })
          const bt = await backtest({
            strategies: strategiesList,
            code: item.code,
            start,
            end,
            cash: 100000,
            size: 10,
          })
          nextCharts[item.code] = {
            candles: cs,
            trades: bt.trades.map((t: any) => ({ index: t.index, side: t.side }))
          }
        } catch {
          // ignore
        }
      })
      await Promise.all(promises)
      setCharts({ ...nextCharts })
    }
  }

  useEffect(() => {
    const onScroll = async () => {
      const nearBottom = window.innerHeight + window.scrollY >= document.body.offsetHeight - 300
      if (!nearBottom || loadingMore || data.length === 0) return
      const v = form.getFieldsValue()
      const ordered = getOrdered(data)
      setLoadingMore(true)
      const nextCount = Math.min(visibleCount + 60, ordered.length)
      const slice = ordered.slice(visibleCount, nextCount)
      
      const selectedStrategies = []
      if (v.strategy) selectedStrategies.push(v.strategy)
      if (v.aux_strategy) selectedStrategies.push(v.aux_strategy)
      
      await loadChartsFor(slice, selectedStrategies)
      setVisibleCount(nextCount)
      setLoadingMore(false)
    }
    window.addEventListener('scroll', onScroll)
    return () => window.removeEventListener('scroll', onScroll)
  }, [visibleCount, data])

  function onExportCSV() {
    const header = ['code','name','price','score','signal']
    const rows = data.map(r => [r.code, r.name || '', r.price, r.score, r.signal])
    const csv = [header.join(','), ...rows.map(x => x.join(','))].join('\n')
    const blob = new Blob([csv], { type: 'text/csv;charset=utf-8;' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = 'screener.csv'
    a.click()
    URL.revokeObjectURL(url)
  }

  return (
    <Space direction="vertical" style={{ width: '100%' }} size="large">
      <Card title="选股条件">
        <Form form={form} layout="inline">
          <Form.Item name="strategy" label="策略" rules={[{ required: true }]}>
            <Select style={{ width: 200 }} options={strategies.map(s => ({ value: s, label: s }))} />
          </Form.Item>
          <Form.Item name="aux_strategy" label="辅助">
            <Select style={{ width: 200 }} allowClear options={auxStrategies.map(s => ({ value: s, label: s }))} />
          </Form.Item>
          <Form.Item name="range" label="时间范围">
             <DatePicker.RangePicker format="YYYY-MM-DD" />
          </Form.Item>
          <Form.Item name="lookback" label="回看天数" hidden>
            <InputNumber min={5} max={60} />
          </Form.Item>
          <Form.Item name="min_score" label="最小评分" hidden>
            <InputNumber step={0.001} />
          </Form.Item>
          <Form.Item name="signal" label="信号" hidden>
            <Select style={{ width: 120 }} options={[
              { value: 0, label: '全部' },
              { value: 1, label: '买入' },
              { value: -1, label: '卖出' },
            ]} />
          </Form.Item>
          <Form.Item>
            <Button type="primary" onClick={onRun} loading={loading}>运行选股</Button>
          </Form.Item>
          <Form.Item>
            <Button onClick={onExportCSV}>导出CSV</Button>
          </Form.Item>
        </Form>
      </Card>
      <Card title="结果">
        <Table
          rowKey="code"
          dataSource={getOrdered(data)}
          onChange={(_, __, sorterArg: any) => setSorter({ field: String(sorterArg?.field || ''), order: sorterArg?.order })}
          columns={[
            { title: '股票代码', dataIndex: 'code', sorter: (a: any, b: any) => String(a.code).localeCompare(String(b.code)), sortDirections: ['ascend','descend'] },
            { title: '股票名称', dataIndex: 'name', sorter: (a: any, b: any) => String(a.name || '').localeCompare(String(b.name || '')), sortDirections: ['ascend','descend'] },
            { title: '价格', dataIndex: 'price', render: (v: number) => v.toFixed(2), sorter: (a: any, b: any) => Number(a.price) - Number(b.price), sortDirections: ['ascend','descend'] },
            { title: '评分', dataIndex: 'score', render: (v: number) => v.toFixed(4), sorter: (a: any, b: any) => Number(a.score) - Number(b.score), sortDirections: ['ascend','descend'] },
            { title: '信号', dataIndex: 'signal', render: (s: number) => s === 1 ? <Tag color="green">买入</Tag> : s === -1 ? <Tag color="red">卖出</Tag> : <Tag>观望</Tag>, sorter: (a: any, b: any) => Number(a.signal) - Number(b.signal), sortDirections: ['ascend','descend'] },
          ]}
        />
      </Card>
      <Card title="K线与买卖点">
        <Space style={{ marginBottom: 8 }}>
          <Button size="small" type={showMA ? 'primary' : 'default'} onClick={() => setShowMA(!showMA)}>均线</Button>
          <Button size="small" type={showBoll ? 'primary' : 'default'} onClick={() => setShowBoll(!showBoll)}>布林带</Button>
        </Space>
        <Row gutter={[12,12]}>
          {getOrdered(data).slice(0, visibleCount).map((item) => {
            const c = charts[item.code]
            return (
              <Col key={item.code} span={8}>
                <Card size="small" title={`${item.name || item.code}-${item.code}`}>
                  {c ? <PriceChart candles={c.candles} trades={c.trades} showMA={showMA} showBollinger={showBoll} /> : <div>加载中...</div>}
                </Card>
              </Col>
            )
          })}
        </Row>
      </Card>
    </Space>
  )
}
