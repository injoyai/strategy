import React, { useEffect, useState } from 'react'
import { Card, Form, Select, DatePicker, Button, Space, message, Switch } from 'antd'
import dayjs from 'dayjs'
import { getCodes, getKlines } from '../lib/api'
import PriceChart from '../components/PriceChart'

export default function MarketPage() {
  const [symbols, setSymbols] = useState<{ code: string, name: string }[]>([])
  const [candles, setCandles] = useState<any[]>([])
  const [loading, setLoading] = useState(false)
  const [form] = Form.useForm()
  const [showMA, setShowMA] = useState(true)
  const [showBoll, setShowBoll] = useState(false)
  const [showVertex, setShowVertex] = useState(true)
  const [showVertex6, setShowVertex6] = useState(false)

  useEffect(() => {
    (async () => {
      try {
        const codes = await getCodes()
        setSymbols(codes)
        form.setFieldsValue({ code: codes[0]?.code, range: [dayjs().subtract(10, 'year'), dayjs()] })
        fetchCandles(codes[0]?.code)
      } catch {
        const syms = ['DEMO','DEMO2','TRENDUP','TRENDDOWN','RANGE']
        setSymbols(syms.map(s => ({ code: s, name: s })))
        form.setFieldsValue({ code: syms[0], range: [dayjs().subtract(10, 'year'), dayjs()] })
        fetchCandles(syms[0])
      }
    })()
  }, [])

  async function fetchCandles(code: string) {
    setLoading(true)
    try {
      const v = form.getFieldsValue()
      const res = await getKlines({
        code: code,
        start: v.range?.[0] ? v.range[0].format('YYYY-MM-DD') : undefined,
        end: v.range?.[1] ? v.range[1].format('YYYY-MM-DD') : undefined,
      })
      setCandles(res)
    } catch (e: any) {
      message.error(e?.message || '加载失败')
    } finally {
      setLoading(false)
    }
  }

  return (
    <Space direction="vertical" style={{ width: '100%' }} size="large">
      <Card title="行情数据">
        <Form form={form} layout="inline">
          <Form.Item name="code" label="股票" rules={[{ required: true }]}>
            <Select
              style={{ width: 240 }}
              showSearch
              placeholder="搜索股票"
              filterOption={(input, option) =>
                String(option?.value || '').toLowerCase().includes(String(input).toLowerCase()) ||
                String(option?.label || '').toLowerCase().includes(String(input).toLowerCase())
              }
              options={symbols.map(s => ({ value: s.code, label: `${s.name}-${s.code}` }))}
              onChange={fetchCandles}
            />
          </Form.Item>
          <Form.Item name="range" label="区间">
            <DatePicker.RangePicker disabledDate={d => d.isAfter(dayjs('2025-12-31'))} />
          </Form.Item>
          <Form.Item>
            <Button type="primary" onClick={() => fetchCandles(form.getFieldValue('code'))} loading={loading}>加载</Button>
          </Form.Item>
        </Form>
      </Card>
      <Card title="K线">
        <Space style={{ marginBottom: 8 }}>
          <Button size="small" type={showMA ? 'primary' : 'default'} onClick={() => setShowMA(!showMA)}>均线</Button>
          <Button size="small" type={showBoll ? 'primary' : 'default'} onClick={() => setShowBoll(!showBoll)}>布林带</Button>
          <Button size="small" type={showVertex6 ? 'primary' : 'default'} onClick={() => setShowVertex6(!showVertex6)}>顶点(6)</Button>
          <Button size="small" type={showVertex ? 'primary' : 'default'} onClick={() => setShowVertex(!showVertex)}>顶点(8)</Button>
        </Space>
        <PriceChart candles={candles} enableZoom defaultWindowCount={120} showMA={showMA} showBollinger={showBoll} showVertex={showVertex} showVertex6={showVertex6} />
      </Card>
    </Space>
  )
}
