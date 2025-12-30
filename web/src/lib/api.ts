import axios from 'axios'

export const api = axios.create({
  baseURL: 'http://localhost:8080/api'
})

function unwrap(d: any) {
  if (d && typeof d === 'object' && 'code' in d && 'data' in d) {
    if (Number(d.code) !== 200) {
      throw new Error(String(d.msg || '接口请求失败'))
    }
    return d.data
  }
  return d
}

export async function getStrategies() {
  const { data } = await api.get('/strategies')
  const body = unwrap(data)
  const strategies = Array.isArray(body) ? body.map((s: any) => String(s)) : (body.strategies || body.Strategies || []).map((s: any) => String(s))
  return strategies as string[]
}

export async function getStrategyNames(): Promise<string[]> {
  const { data } = await api.get('/strategy/names')
  const body = unwrap(data)
  const arr = Array.isArray(body) ? body : (body.names || body.list || body.items || [])
  return arr.map((s: any) => String(s))
}

export async function getStrategyAll(): Promise<{ name: string, script?: string, enable?: boolean, package?: string }[]> {
  const { data } = await api.get('/strategy/all')
  const body = unwrap(data)
  const arr = Array.isArray(body) ? body : (body.items || body.list || [])
  return arr.map((it: any) => ({
    name: String(it.Name ?? it.name ?? ''),
    script: String(it.Script ?? it.script ?? ''),
    enable: Boolean(it.Enable ?? it.enable ?? false),
    package: String(it.Package ?? it.package ?? 'strategy')
  }))
}

export async function createStrategy(body: { name: string, script: string, enable?: boolean }) {
  const payload = { Name: body.name, Script: body.script, Enable: Boolean(body.enable) }
  const { data } = await api.post('/strategy', payload)
  unwrap(data)
}

export async function updateStrategy(body: { name: string, script: string }) {
  const payload = { Name: body.name, Script: body.script }
  const { data } = await api.put('/strategy', payload)
  unwrap(data)
}

export async function setStrategyEnable(body: { name: string, enable: boolean }) {
  const payload = { Name: body.name, Enable: body.enable }
  const { data } = await api.put('/strategy/enable', payload)
  unwrap(data)
}

export async function deleteStrategy(name: string) {
  const { data } = await api.delete('/strategy', { data: { Name: name } })
  unwrap(data)
}

export async function getCodes(): Promise<{ code: string, name: string }[]> {
  const { data } = await api.get('/codes')
  const body = unwrap(data)
  const arr = Array.isArray(body) ? body : (body.codes || body.Codes || [])
  return arr.map((s: any) => {
    if (typeof s === 'string') return { code: String(s), name: String(s) }
    return {
      code: String(s.code ?? s.Code ?? ''),
      name: String(s.name ?? s.Name ?? s.code ?? s.Code ?? '')
    }
  })
}

export async function backtest(req: {
  strategy: string
  symbol: string
  start?: string
  end?: string
  cash?: number
  size?: number
  fee_rate?: number
  min_fee?: number
  slippage?: number
  stop_loss?: number
  take_profit?: number
}) {
  const payload: any = {
    strategy: req.strategy,
    symbol: req.symbol,
    start: req.start,
    end: req.end,
    cash: req.cash,
    size: req.size,
    fee_rate: req.fee_rate,
    min_fee: req.min_fee,
    slippage: req.slippage,
    stop_loss: req.stop_loss,
    take_profit: req.take_profit,
    feeRate: req.fee_rate,
    minFee: req.min_fee,
    stopLoss: req.stop_loss,
    takeProfit: req.take_profit,
  }
  const { data } = await api.post('/backtest', payload)
  const resp = unwrap(data)
  const eq = resp.equity || resp.Equity || resp.nav || []
  const cash = resp.cash || resp.Cash || []
  const pos = resp.position || resp.Position || resp.positions || []
  const trades = (resp.trades || resp.Trades || []).map((t: any) => ({
    time: t.time ?? t.timestamp ?? t.ts ?? 0,
    index: t.index ?? t.idx ?? t.bar_index ?? 0,
    price: t.price ?? t.px ?? t.fill_price ?? 0,
    side: String(t.side ?? t.Side ?? '').toLowerCase(),
    qty: t.qty ?? t.quantity ?? t.size ?? 0
  }))
  const ret = resp.return ?? resp.ret ?? resp.total_return ?? 0
  const max_drawdown = resp.max_drawdown ?? resp.maxDD ?? resp.MaxDD ?? resp.drawdown ?? 0
  const sharpe = resp.sharpe ?? resp.Sharpe ?? 0
  return resp as {
    equity: typeof eq
    cash: typeof cash
    position: typeof pos
    trades: typeof trades
    return: typeof ret
    max_drawdown: typeof max_drawdown
    sharpe: typeof sharpe
  }
}

export async function backtestAll(req: {
  strategy: string
  start?: string
  end?: string
  cash?: number
  size?: number
  fee_rate?: number
  min_fee?: number
  slippage?: number
  stop_loss?: number
  take_profit?: number
}) {
  const payload: any = {
    strategy: req.strategy,
    start: req.start,
    end: req.end,
    cash: req.cash,
    size: req.size,
    fee_rate: req.fee_rate,
    min_fee: req.min_fee,
    slippage: req.slippage,
    stop_loss: req.stop_loss,
    take_profit: req.take_profit,
  }
  const { data } = await api.post('/backtest_all', payload)
  const body = unwrap(data)
  const items = (body.items || []).map((it: any) => ({
    code: String(it.code ?? ''),
    name: String(it.name ?? ''),
    return: Number(it.return ?? 0),
    max_drawdown: Number(it.max_drawdown ?? 0),
    sharpe: Number(it.sharpe ?? 0),
  }))
  return {
    avg_return: Number(body.avg_return ?? 0),
    avg_sharpe: Number(body.avg_sharpe ?? 0),
    avg_max_drawdown: Number(body.avg_max_drawdown ?? 0),
    count: Number(body.count ?? items.length),
    items,
  } as {
    avg_return: number
    avg_sharpe: number
    avg_max_drawdown: number
    count: number
    items: { code: string, name: string, return: number, max_drawdown: number, sharpe: number }[]
  }
}

export function backtestAllWS(req: {
  strategy: string
  start?: string
  end?: string
  cash?: number
  size?: number
  fee_rate?: number
  min_fee?: number
  slippage?: number
  stop_loss?: number
  take_profit?: number
}) {
  const base = api.defaults.baseURL || 'http://localhost:8080/api'
  const u = new URL(base.replace(/^http/i, 'ws'))
  u.pathname = '/api/backtest_all/ws'
  const params = new URLSearchParams()
  params.set('strategy', req.strategy)
  if (req.start) params.set('start', req.start)
  if (req.end) params.set('end', req.end)
  if (typeof req.cash === 'number') params.set('cash', String(req.cash))
  if (typeof req.size === 'number') params.set('size', String(req.size))
  if (typeof req.fee_rate === 'number') params.set('fee_rate', String(req.fee_rate))
  if (typeof req.min_fee === 'number') params.set('min_fee', String(req.min_fee))
  if (typeof req.slippage === 'number') params.set('slippage', String(req.slippage))
  if (typeof req.stop_loss === 'number') params.set('stop_loss', String(req.stop_loss))
  if (typeof req.take_profit === 'number') params.set('take_profit', String(req.take_profit))
  u.search = params.toString()
  const ws = new WebSocket(u.toString())
  return ws
}

export async function screener(body: { strategy: string, lookback?: number }) {
  const { data } = await api.post('/screener', body)
  const body2 = unwrap(data)
  const arr = Array.isArray(body2) ? body2 : (body2.items || body2.list || [])
  return arr.map((it: any) => ({
    symbol: it.symbol ?? it.ticker ?? it.code,
    score: it.score ?? it.value ?? 0,
    price: it.price ?? it.last ?? 0,
    signal: it.signal ?? it.sig ?? 0
  })) as { symbol: string, score: number, price: number, signal: number }[]
}

export async function grid(body: {
  symbol: string
  start?: string
  end?: string
  cash?: number
  size?: number
  fast_min: number
  fast_max: number
  slow_min: number
  slow_max: number
  step?: number
  top_k?: number
}) {
  const { data } = await api.post('/backtest/grid', body)
  const body2 = unwrap(data)
  const arr = Array.isArray(body2) ? body2 : (body2.items || body2.list || [])
  return arr.map((g: any) => ({
    fast: g.fast ?? g.fast_period ?? 0,
    slow: g.slow ?? g.slow_period ?? 0,
    return: g.return ?? g.ret ?? g.total_return ?? 0,
    sharpe: g.sharpe ?? g.Sharpe ?? 0,
    max_drawdown: g.max_drawdown ?? g.maxDD ?? g.drawdown ?? 0
  })) as { fast: number, slow: number, return: number, sharpe: number, max_drawdown: number }[]
}

export async function getCandles(params: { symbol: string, start?: string, end?: string }) {
  const { data } = await api.get('/candles', { params })
  const body = unwrap(data)
  const arr = Array.isArray(body) ? body : (body.items || body.list || [])
  return arr.map((c: any) => {
    const t = c.Time ?? c.time ?? c.timestamp ?? c.ts ?? c.date
    const iso = typeof t === 'number' ? new Date(t * (t > 10000000000 ? 1 : 1000)).toISOString() : String(t)
    return {
      Time: iso,
      Open: c.Open ?? c.open ?? c.o ?? 0,
      High: c.High ?? c.high ?? c.h ?? 0,
      Low: c.Low ?? c.low ?? c.l ?? 0,
      Close: c.Close ?? c.close ?? c.c ?? 0,
      Volume: c.Volume ?? c.volume ?? c.v ?? 0,
      Symbol: c.Symbol ?? c.symbol ?? c.ticker ?? c.code ?? params.symbol
    }
  }) as { Time: string, Open: number, High: number, Low: number, Close: number, Volume: number, Symbol: string }[]
}

export async function getKlines(params: { code: string, start?: string, end?: string }) {
  const { data } = await api.get('/klines', { params })
  const body = unwrap(data)
  const arr = Array.isArray(body) ? body : (body.items || body.list || [])
  return arr.map((c: any) => {
    const t = c.Time ?? c.time ?? c.timestamp ?? c.ts ?? c.date
    const iso = typeof t === 'number' ? new Date(t * (t > 10000000000 ? 1 : 1000)).toISOString() : String(t)
    return {
      Time: iso,
      Open: Number(((c.Open ?? c.open ?? c.o ?? c.OpenPrice ?? 0) / 1000).toFixed(4)),
      High: Number(((c.High ?? c.high ?? c.h ?? c.HighPrice ?? 0) / 1000).toFixed(4)),
      Low: Number(((c.Low ?? c.low ?? c.l ?? c.LowPrice ?? 0) / 1000).toFixed(4)),
      Close: Number(((c.Close ?? c.close ?? c.c ?? c.ClosePrice ?? 0) / 1000).toFixed(4)),
      Volume: c.Volume ?? c.volume ?? c.v ?? c.TradeVolume ?? 0,
      Amount: c.Amount ? Number(((c.Amount ?? 0) / 1000).toFixed(2)) :
               c.amount ? Number(((c.amount ?? 0) / 1000).toFixed(2)) :
               c.Turnover ? Number(((c.Turnover ?? 0) / 1000).toFixed(2)) :
               c.trade_amount ? Number(((c.trade_amount ?? 0) / 1000).toFixed(2)) : undefined,
      Symbol: c.Symbol ?? c.symbol ?? c.ticker ?? c.code ?? params.code
    }
  }) as { Time: string, Open: number, High: number, Low: number, Close: number, Volume: number, Amount?: number, Symbol: string }[]
}
