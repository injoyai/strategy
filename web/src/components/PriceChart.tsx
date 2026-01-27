import React, { useRef } from 'react'
import ReactECharts from 'echarts-for-react'
import dayjs from 'dayjs'

export default function PriceChart({ candles, trades, equity, signals, showBuy = false, showSell = true, showSignals = false, showReturns = true, enableZoom = false, defaultWindowCount, showBollinger = false, showVolume = false, showMA = true, showVertex = false, showVertex6 = false, showVertex10 = false }: { candles: { Time: string, Open: number, High: number, Low: number, Close: number, Volume?: number, Amount?: number }[], trades?: { index: number, side: string }[], equity?: number[], signals?: number[], showBuy?: boolean, showSell?: boolean, showSignals?: boolean, showReturns?: boolean, enableZoom?: boolean, defaultWindowCount?: number, showBollinger?: boolean, showVolume?: boolean, showMA?: boolean, showVertex?: boolean, showVertex6?: boolean, showVertex10?: boolean }) {
  if (!candles || candles.length === 0) return null
  const x = candles.map(c => dayjs(c.Time).format('YY-MM-DD'))
  const maxPrice = Math.max(...candles.map(c => Math.max(Number(c.Open || 0), Number(c.High || 0), Number(c.Low || 0), Number(c.Close || 0))))
  const factor = maxPrice > 1000 ? 1000 : 1
  const ohlc = candles.map(c => [c.Open / factor, c.Close / factor, c.Low / factor, c.High / factor])
  const closes = candles.map(c => c.Close / factor)
  const highs = candles.map(c => Number(c.High || 0) / factor)
  const lows = candles.map(c => Number(c.Low || 0) / factor)
  const vols = candles.map(c => Number(c.Volume ?? 0))
  const volumeSeriesData = vols.map((v, i) => ({
    value: v,
    itemStyle: { color: ((candles[i].Close / factor) >= (candles[i].Open / factor)) ? '#f5222d' : '#52c41a' }
  }))
  const sma = (n: number) => {
    const out = new Array<number>(closes.length).fill(NaN)
    let sum = 0
    for (let i = 0; i < closes.length; i++) {
      sum += closes[i]
      if (i >= n) sum -= closes[i - n]
      if (i >= n - 1) out[i] = Number((sum / n).toFixed(4))
    }
    return out
  }
  const boll = (n = 20, k = 2) => {
    const mid = sma(n)
    const up = new Array<number>(closes.length).fill(NaN)
    const low = new Array<number>(closes.length).fill(NaN)
    let sum = 0
    let sumsq = 0
    for (let i = 0; i < closes.length; i++) {
      const v = closes[i]
      sum += v
      sumsq += v * v
      if (i >= n) {
        const vOut = closes[i - n]
        sum -= vOut
        sumsq -= vOut * vOut
      }
      if (i >= n - 1) {
        const mean = sum / n
        const variance = Math.max(0, sumsq / n - mean * mean)
        const std = Math.sqrt(variance)
        up[i] = Number((mean + k * std).toFixed(4))
        low[i] = Number((mean - k * std).toFixed(4))
      }
    }
    return { mid, up, low }
  }
  const buyPts = (trades || [])
    .filter(t => t.side === 'buy' && t.index >= 0 && t.index < closes.length)
    .map(t => ({
      value: [t.index, closes[t.index]],
      symbolSize: 10,
      itemStyle: { borderColor: '#237804', borderWidth: 2, color: '#52c41a' }
    }))
  const sellPts = (trades || [])
    .filter(t => t.side === 'sell' && t.index >= 0 && t.index < closes.length)
    .map(t => ({
      value: [t.index, closes[t.index]],
      symbolSize: 10,
      itemStyle: { borderColor: '#a8071a', borderWidth: 2, color: '#f5222d' }
    }))
  const buyLines = (trades || [])
    .filter(t => t.side === 'buy' && t.index >= 0 && t.index < x.length)
    .map(t => ({
      xAxis: t.index, lineStyle: { type: 'dashed', color: '#52c41a' }, label: { formatter: '买入' }
    }))
  const sellLines = (trades || [])
    .filter(t => t.side === 'sell' && t.index >= 0 && t.index < x.length)
    .map(t => ({
      xAxis: t.index, lineStyle: { type: 'dashed', color: '#f5222d' }, label: { formatter: '卖出' }
    }))
  
  const getVertexPts = (windowSize: number) => {
    const pts: any[] = []
    for (let i = windowSize; i < highs.length - windowSize; i++) {
      // Peak
      const h = highs[i]
      let isPeak = true
      for (let j = i - windowSize; j <= i + windowSize; j++) {
        if (i === j) continue
        if (highs[j] >= h) {
          isPeak = false
          break
        }
      }
      if (isPeak) {
        pts.push({
          value: [i, h],
          symbol: 'triangle',
          symbolRotate: 180,
          symbolOffset: [0, '-100%'],
          symbolSize: 10,
          itemStyle: { color: '#f5222d', borderColor: '#f5222d' }
        })
      }
      // Valley
      const l = lows[i]
      let isValley = true
      for (let j = i - windowSize; j <= i + windowSize; j++) {
        if (i === j) continue
        if (lows[j] <= l) {
          isValley = false
          break
        }
      }
      if (isValley) {
        pts.push({
          value: [i, l],
          symbol: 'triangle',
          symbolRotate: 0,
          symbolOffset: [0, '100%'],
          symbolSize: 10,
          itemStyle: { color: '#52c41a', borderColor: '#52c41a' }
        })
      }
    }
    return pts
  }

  const vertexPts: any[] = []
  if (showVertex) {
    vertexPts.push(...getVertexPts(8))
  }
  if (showVertex6) {
    vertexPts.push(...getVertexPts(6))
  }
  if (showVertex10) {
    vertexPts.push(...getVertexPts(10))
  }

  const signalPts = (signals || []).map((s, i) => {
    if (i >= closes.length) return null
    if (s === 1) {
      return {
        value: [i, lows[i]],
        symbol: 'arrow',
        symbolRotate: 0,
        symbolOffset: [0, '200%'],
        symbolSize: 8,
        itemStyle: { color: '#faad14' }
      }
    }
    return null
  }).filter(v => !!v)

  let retLine: number[] | undefined
  let benchmarkLine: number[] | undefined
  if (equity && equity.length) {
    const base = equity[0] || 1
    retLine = equity.map(v => Number((((v / base) - 1) * 100).toFixed(2)))
  }
  if (closes && closes.length) {
    const base = closes[0] || 1
    benchmarkLine = closes.map(v => Number((((v / base) - 1) * 100).toFixed(2)))
  }
  const { mid, up, low } = boll(20, 2)
  const hasVol = showVolume
  const priceSeries: any[] = [
    { type: 'candlestick', name: 'K线', data: ohlc, itemStyle: { color: '#f5222d', color0: '#52c41a', borderColor: '#f5222d', borderColor0: '#52c41a' }, markLine: { symbol: ['none','none'], silent: true, data: [...(showBuy ? buyLines : []), ...(showSell ? sellLines : [])] } },
    ...(showMA ? [
      { type: 'line', name: 'SMA5', data: sma(5), smooth: true, showSymbol: false },
      { type: 'line', name: 'SMA20', data: sma(20), smooth: true, showSymbol: false },
    ] : []),
    ...(showBollinger ? [
      { type: 'line', name: '布林中轨', data: mid, smooth: true, showSymbol: false } as any,
      { type: 'line', name: '布林上轨', data: up, smooth: true, showSymbol: false } as any,
      { type: 'line', name: '布林下轨', data: low, smooth: true, showSymbol: false } as any,
    ] : []),
    (showVertex || showVertex6 || showVertex10) ? { type: 'scatter', name: '顶点', data: vertexPts } as any : undefined,
    showSignals && signalPts.length ? { type: 'scatter', name: '信号', data: signalPts } as any : undefined,
    benchmarkLine && showReturns ? { type: 'line', name: '基准', yAxisIndex: 1, data: benchmarkLine, smooth: true, showSymbol: false, lineStyle: { width: 1, type: 'solid', color: '#8c8c8c' } } as any : undefined,
    retLine && showReturns ? { type: 'line', name: '收益', yAxisIndex: 1, data: retLine, smooth: true, showSymbol: false, lineStyle: { width: 2, color: '#722ed1' }, areaStyle: { color: 'rgba(114,46,209,0.12)' } } as any : undefined,
    buyPts.length && showBuy ? { type: 'scatter', name: '买入', data: buyPts, symbol: 'triangle' } as any : undefined,
    sellPts.length && showSell ? { type: 'scatter', name: '卖出', data: sellPts, symbol: 'triangle', symbolRotate: 180 } as any : undefined,
  ].filter(Boolean) as any[]
  const volumeSeries = hasVol ? [{ type: 'bar', name: '成交量', data: volumeSeriesData, xAxisIndex: 1, yAxisIndex: 2 } as any] : []
  const series = [...priceSeries, ...volumeSeries]
  const startIdxRef = useRef<number | undefined>(undefined)
  const prevLenRef = useRef<number>(0)
  const prevLen = prevLenRef.current
  const xLen = x.length
  let shouldInitZoom = false
  if (startIdxRef.current === undefined || prevLen !== xLen) {
    startIdxRef.current = defaultWindowCount ? Math.max(0, xLen - defaultWindowCount) : 0
    shouldInitZoom = true
    prevLenRef.current = xLen
  }
  const startIdx = startIdxRef.current as number
  const option = {
    tooltip: {
      trigger: 'axis',
      formatter: (params: any) => {
        const p = Array.isArray(params) ? params[0] : params
        const idx = p?.dataIndex ?? 0
        const c = candles[idx]
        const open = Number(c.Open || 0) / factor
        const close = Number(c.Close || 0) / factor
        const high = Number(c.High || 0) / factor
        const low = Number(c.Low || 0) / factor
        const fmtWanYi = (n: number, unit: string, wanUnit: string, yiUnit: string) => {
          if (n >= 100000000) return `${(n / 100000000).toFixed(2)}${yiUnit}`
          if (n >= 10000) return `${(n / 10000).toFixed(2)}${wanUnit}`
          return `${n}${unit}`
        }
        const lines: string[] = []
        lines.push(`日期：${x[idx]}`)
        lines.push(`开盘：${open} 元`)
        lines.push(`收盘：${close} 元`)
        lines.push(`最高：${high} 元`)
        lines.push(`最低：${low} 元`)
        lines.push(`成交量：${fmtWanYi(vols[idx], '手', '万手', '亿手')}`)
        if (typeof c.Amount === 'number') lines.push(`成交额：${fmtWanYi(c.Amount, '元', '万元', '亿元')}`)
        return lines.join('<br/>')
      }
    },
    legend: { 
      top: 4,
      data: [
        ...(showMA ? ['SMA5','SMA20'] : []),
        ...(showBollinger ? ['布林中轨','布林上轨','布林下轨'] : []),
      ]
    },
    axisPointer: { link: [{ xAxisIndex: hasVol ? [0,1] : [0] }] },
    grid: hasVol ? [
      { left: 50, right: 24, top: 36, height: '60%', containLabel: true },
      { left: 50, right: 24, bottom: 80, height: '18%', containLabel: true }
    ] : undefined,
    xAxis: hasVol ? [
      { type: 'category', data: x, gridIndex: 0, boundaryGap: true, axisTick: { alignWithLabel: true, show: false }, axisLabel: { show: false }, axisLine: { show: false }, splitLine: { show: true, lineStyle: { type: 'solid', color: '#ddd' } } },
      { type: 'category', data: x, gridIndex: 1, boundaryGap: true, axisTick: { alignWithLabel: true }, axisLabel: { show: true }, axisLine: { show: true }, splitLine: { show: true, lineStyle: { type: 'solid', color: '#ddd' } } }
    ] : { type: 'category', data: x, boundaryGap: true, axisTick: { alignWithLabel: true }, splitLine: { show: true, lineStyle: { type: 'solid', color: '#ddd' } } },
    yAxis: hasVol ? [
      { scale: true, name: '价格(元)', nameGap: 24, gridIndex: 0, position: 'left', splitLine: { show: true, lineStyle: { type: 'solid', color: '#ddd' } } },
      { type: 'value', name: '收益(%)', position: 'right', axisLabel: { formatter: (v: number) => `${v.toFixed(0)}%` }, gridIndex: 0, splitLine: { show: true, lineStyle: { type: 'solid', color: '#ddd' } } },
      { type: 'value', name: '成交量(手)', position: 'left', gridIndex: 1, axisLabel: { show: false }, axisTick: { show: false }, axisLine: { show: true }, splitLine: { show: true, lineStyle: { type: 'solid', color: '#ddd' } } }
    ] : [
      { scale: true, name: '价格(元)', position: 'left', splitLine: { show: true, lineStyle: { type: 'solid', color: '#ddd' } } },
      { type: 'value', name: '收益(%)', position: 'right', axisLabel: { formatter: (v: number) => `${v.toFixed(0)}%` }, splitLine: { show: true, lineStyle: { type: 'solid', color: '#ddd' } } },
    ],
    dataZoom: enableZoom ? (
      hasVol ? [
        { type: 'inside', xAxisIndex: [0,1], ...(shouldInitZoom ? { startValue: startIdx, endValue: x.length - 1 } : {}) },
        { type: 'slider', xAxisIndex: [0,1], bottom: 20, height: 24, ...(shouldInitZoom ? { startValue: startIdx, endValue: x.length - 1 } : {}) }
      ] : [
        { type: 'inside', xAxisIndex: 0, ...(shouldInitZoom ? { startValue: startIdx, endValue: x.length - 1 } : {}) },
        { type: 'slider', xAxisIndex: 0, ...(shouldInitZoom ? { startValue: startIdx, endValue: x.length - 1 } : {}) }
      ]
    ) : undefined,
    series: series.map(s => {
      if (s?.type === 'candlestick') return { ...s, barWidth: '60%' }
      if (s?.type === 'bar' && hasVol) return { ...s, barWidth: '60%', barCategoryGap: '0%' }
      return s
    })
  }
  return <ReactECharts option={option} replaceMerge={['series']} style={{ height: hasVol ? 520 : 460 }} />
}
