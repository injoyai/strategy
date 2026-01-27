import React, { useEffect, useState } from 'react'
import { Card, Form, Select, InputNumber, Button, Table, Tag, Space, message, Switch, Row, Col, DatePicker } from 'antd'
import { getStrategies, screener } from '../lib/api'
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
  const [showVertex, setShowVertex] = useState(false)
  const [showVertex6, setShowVertex6] = useState(false)
  const [showVertex10, setShowVertex10] = useState(false)
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
    
    const s1 = v.strategy || []
    const s2 = v.aux_strategy || []
    if (s1.length === 0 && s2.length === 0) {
      message.error('请至少选择一个策略（主策略或辅助策略）')
      return
    }

    setLoading(true)
    try {
      const start = v.range?.[0] ? v.range[0].format('YYYY-MM-DD') : undefined
      const end = v.range?.[1] ? v.range[1].format('YYYY-MM-DD') : undefined
      const startTs = v.range?.[0] ? v.range[0].unix() : undefined
      const endTs = v.range?.[1] ? v.range[1].unix() : undefined
      
      const selectedStrategies = [...s1, ...s2]

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
      await loadChartsFor(ordered.slice(0, 60))
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

  function formatValueWithUnit(v: any) {
    const n = Number(v ?? 0)
    if (!Number.isFinite(n)) return '0'
    if (n >= 100000000) return `${(n / 100000000).toFixed(2)}亿`
    if (n >= 10000) return `${(n / 10000).toFixed(2)}万`
    return `${n.toFixed(2)}`
  }

  async function loadChartsFor(items: any[]) {
    const nextCharts: Record<string, { candles: any[], trades: { index: number, side: string }[] }> = { ...charts }
    const chunkSize = 6
    
    for (let i = 0; i < items.length; i += chunkSize) {
      const batch = items.slice(i, i + chunkSize)
      const promises = batch.map(async (item) => {
        if (nextCharts[item.code]) return
        try {
          const cs = Array.isArray(item.klines) ? item.klines : []
          const trades = Array.isArray(item.trades) ? item.trades : []
          const mappedTrades = trades.map((t: any) => {
            const rawSide = t.side ?? t.Side ?? t.signal ?? t.Signal ?? t.s
            const side = rawSide === 1 || rawSide === '1' ? 'buy' : rawSide === -1 || rawSide === '-1' ? 'sell' : String(rawSide || '')
            const rawIndex = t.index ?? t.Index ?? t.i ?? t.idx
            const index = typeof rawIndex === 'number' ? rawIndex : Number(rawIndex)
            return { index, side }
          }).filter((t: any) => Number.isFinite(t.index) && (t.side === 'buy' || t.side === 'sell'))
          if (!cs.length) return
          nextCharts[item.code] = {
            candles: cs,
            trades: mappedTrades
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
      
      await loadChartsFor(slice)
      setVisibleCount(nextCount)
      setLoadingMore(false)
    }
    window.addEventListener('scroll', onScroll)
    return () => window.removeEventListener('scroll', onScroll)
  }, [visibleCount, data])

  function onExportCSV() {
    const header = ['code','name','price','score','turnover','floatValue','signal']
    const rows = data.map(r => {
      const v = Number(r.turnoverRate ?? r.turnover ?? r.TurnoverRate ?? r.Turnover ?? 0)
      const pct = v <= 1 ? v * 100 : v
      const fv = Number(r.floatValue ?? (Number(r.FloatValue ?? 0) / 1000))
      return [r.code, r.name || '', r.price, r.score, pct, formatValueWithUnit(fv), r.signal]
    })
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
          <Form.Item name="strategy" label="策略">
            <Select mode="multiple" maxTagCount="responsive" style={{ minWidth: 200, maxWidth: 400 }} options={strategies.map(s => ({ value: s, label: s }))} placeholder="选择策略" />
          </Form.Item>
          <Form.Item name="aux_strategy" label="辅助">
            <Select mode="multiple" maxTagCount="responsive" style={{ minWidth: 200, maxWidth: 400 }} allowClear options={auxStrategies.map(s => ({ value: s, label: s }))} placeholder="选择辅助策略" />
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
            { title: '换手率', dataIndex: 'turnover', render: (_: any, r: any) => {
              const v = Number(r.turnoverRate ?? r.turnover ?? r.TurnoverRate ?? r.Turnover ?? 0)
              const pct = v <= 1 ? v * 100 : v
              return `${pct.toFixed(2)}%`
            }, sorter: (a: any, b: any) => {
              const va = Number(a.turnoverRate ?? a.turnover ?? a.TurnoverRate ?? a.Turnover ?? 0)
              const vb = Number(b.turnoverRate ?? b.turnover ?? b.TurnoverRate ?? b.Turnover ?? 0)
              const pa = va <= 1 ? va * 100 : va
              const pb = vb <= 1 ? vb * 100 : vb
              return pa - pb
            }, sortDirections: ['ascend','descend'] },
            { title: '流通市值', dataIndex: 'floatValue', render: (v: number, r: any) => {
              const fv = Number(r.floatValue ?? (Number(r.FloatValue ?? v ?? 0) / 1000))
              return formatValueWithUnit(fv)
            }, sorter: (a: any, b: any) => {
              const va = Number(a.floatValue ?? (Number(a.FloatValue ?? 0) / 1000))
              const vb = Number(b.floatValue ?? (Number(b.FloatValue ?? 0) / 1000))
              return va - vb
            }, sortDirections: ['ascend','descend'] },
            { title: '信号', dataIndex: 'signal', render: (s: number) => s === 1 ? <Tag color="green">买入</Tag> : s === -1 ? <Tag color="red">卖出</Tag> : <Tag>观望</Tag>, sorter: (a: any, b: any) => Number(a.signal) - Number(b.signal), sortDirections: ['ascend','descend'] },
          ]}
        />
      </Card>
      <Card title="K线与买卖点">
        <Space style={{ marginBottom: 8 }}>
          <Button size="small" type={showMA ? 'primary' : 'default'} onClick={() => setShowMA(!showMA)}>均线</Button>
          <Button size="small" type={showBoll ? 'primary' : 'default'} onClick={() => setShowBoll(!showBoll)}>布林带</Button>
          <Button size="small" type={showVertex6 ? 'primary' : 'default'} onClick={() => setShowVertex6(!showVertex6)}>顶点(6)</Button>
          <Button size="small" type={showVertex ? 'primary' : 'default'} onClick={() => setShowVertex(!showVertex)}>顶点(8)</Button>
          <Button size="small" type={showVertex10 ? 'primary' : 'default'} onClick={() => setShowVertex10(!showVertex10)}>顶点(10)</Button>
        </Space>
        <Row gutter={[12,12]}>
          {getOrdered(data).slice(0, visibleCount).map((item) => {
            const c = charts[item.code]
            return (
              <Col key={item.code} span={8}>
                <Card size="small" title={`${item.name || item.code}-${item.code}`}>
                  {c ? <PriceChart candles={c.candles} trades={c.trades} showMA={showMA} showBollinger={showBoll} showVertex={showVertex} showVertex6={showVertex6} showVertex10={showVertex10} showReturns={false} /> : <div>加载中...</div>}
                </Card>
              </Col>
            )
          })}
        </Row>
      </Card>
    </Space>
  )
}
