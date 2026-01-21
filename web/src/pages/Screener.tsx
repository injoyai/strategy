import React, { useEffect, useState } from 'react'
import { Card, Form, Select, InputNumber, Button, Table, Tag, Space, message, Switch, Row, Col, DatePicker } from 'antd'
import { getStrategies, screener, getKlines, backtest } from '../lib/api'
import PriceChart from '../components/PriceChart'
import dayjs from 'dayjs'

export default function ScreenerPage() {
  const [strategies, setStrategies] = useState<string[]>([])
  const [data, setData] = useState<any[]>([])
  const [charts, setCharts] = useState<Record<string, { candles: any[], trades: { index: number, side: string }[] }>>({})
  const [loading, setLoading] = useState(false)
  const [form] = Form.useForm()
  const [visibleCount, setVisibleCount] = useState(60)
  const [loadingMore, setLoadingMore] = useState(false)

  useEffect(() => {
    (async () => {
      try {
        const strats = await getStrategies()
        setStrategies(strats)
        const defaultStart = dayjs().subtract(1, 'month')
        const defaultEnd = dayjs().hour(23).minute(23).second(0)
        form.setFieldsValue({ 
          strategy: strats[0], 
          lookback: 10,
          range: [defaultStart, defaultEnd]
        })
      } catch {
        setStrategies([])
        const defaultStart = dayjs().subtract(1, 'month')
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
      
      const res = await screener({ 
        strategy: v.strategy, 
        lookback: v.lookback,
        start_time: startTs,
        end_time: endTs
      })
      setData(res)
      setVisibleCount(60)
      await loadChartsFor(res.slice(0, 60), v.strategy)
    } catch (e: any) {
      message.error(e?.message || '选股失败')
    } finally {
      setLoading(false)
    }
  }

  async function loadChartsFor(items: any[], strategy: string) {
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
            strategy,
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
      setLoadingMore(true)
      const nextCount = Math.min(visibleCount + 60, data.length)
      const slice = data.slice(visibleCount, nextCount)
      await loadChartsFor(slice, v.strategy)
      setVisibleCount(nextCount)
      setLoadingMore(false)
    }
    window.addEventListener('scroll', onScroll)
    return () => window.removeEventListener('scroll', onScroll)
  }, [visibleCount, data])

  function onExportCSV() {
    const header = ['code','price','score','signal']
    const rows = data.map(r => [r.code, r.price, r.score, r.signal])
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
          dataSource={data}
          columns={[
            { title: '股票', dataIndex: 'code' },
            { title: '价格', dataIndex: 'price', render: (v: number) => v.toFixed(2) },
            { title: '评分', dataIndex: 'score', render: (v: number) => v.toFixed(4) },
            { title: '信号', dataIndex: 'signal', render: (s: number) => s === 1 ? <Tag color="green">买入</Tag> : s === -1 ? <Tag color="red">卖出</Tag> : <Tag>观望</Tag> },
          ]}
        />
      </Card>
      <Card title="K线与买卖点">
        <Row gutter={[12,12]}>
          {data.slice(0, visibleCount).map((item) => {
            const c = charts[item.code]
            return (
              <Col key={item.code} span={8}>
                <Card size="small" title={item.code}>
                  {c ? <PriceChart candles={c.candles} trades={c.trades} /> : <div>加载中...</div>}
                </Card>
              </Col>
            )
          })}
        </Row>
      </Card>
    </Space>
  )
}
