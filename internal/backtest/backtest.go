package backtest

import (
	"math"
	"time"

	"github.com/injoyai/strategy/internal/data"
	"github.com/injoyai/strategy/internal/strategy"
	"github.com/injoyai/tdx/extend"
)

type Trade struct {
	Time  int64   `json:"time"`
	Index int     `json:"index"`
	Price float64 `json:"price"`
	Side  string  `json:"side"`
	Qty   int     `json:"qty"`
}

type Result struct {
	// Equity 每根K线对应的总资产（现金 + 持仓市值）
	// 计算方式：eq + pos*close，其中 eq 为现金余额，pos 为持仓数量，close 为该根K线的收盘价
	Equity []float64 `json:"equity"`
	// Cash 每根K线对应的现金余额（扣除手续费、滑点、买入成本或卖出回款后的剩余现金）
	Cash []float64 `json:"cash"`
	// Position 每根K线对应的持仓数量（单位：股/手，随买入卖出、止盈止损而变化）
	Position []int `json:"position"`
	// Trades 回测期间产生的交易记录（包含时间、索引、成交价、方向、数量）
	Trades []Trade `json:"trades"`
	// Return 总收益率（(最终总资产 - 初始现金) / 初始现金）
	Return float64 `json:"return"`
	// MaxDD 最大回撤比例（期间总资产相对峰值的最大下跌比例）
	MaxDD float64 `json:"max_drawdown"`
	// Sharpe 夏普比率（以日收益率序列计算：mean/StdDev * sqrt(252)）
	Sharpe float64 `json:"sharpe"`
	// Klines K线数据
	Klines interface{} `json:"klines"`
	// Signals 策略信号序列 (1: Buy, 0: None, -1: Sell)
	Signals []int `json:"signals"`
}

type Settings struct {
	Cash       float64
	Size       int
	FeeRate    float64
	MinFee     float64
	Slippage   float64
	StopLoss   float64
	TakeProfit float64
}

type Candle struct {
	Time   time.Time
	Open   float64
	High   float64
	Low    float64
	Close  float64
	Volume float64
	Symbol string
}

func RunBacktestAdvanced(info data.Info, day, min extend.Klines, strat strategy.Interface, cfg Settings) Result {
	ks := day
	if len(day) == 0 && len(min) == 0 {
		return Result{
			Equity:   []float64{},
			Cash:     []float64{},
			Position: []int{},
			Trades:   []Trade{},
			Return:   0,
			MaxDD:    0,
			Sharpe:   0,
		}
	}

	sigs := strat.Signal(info, day, min)
	_ = sigs
	n := len(day)
	equity := make([]float64, n)
	cashSeries := make([]float64, n)
	posSeries := make([]int, n)
	var pos int
	var eq float64 = cfg.Cash
	trades := make([]Trade, 0, 64)
	var peak float64
	rets := make([]float64, 0, n)
	signals := make([]int, n)
	var entry float64
	for i := 0; i < n; i++ {
		price := ks[i].Close.Float64()
		buyPx := price * (1 + cfg.Slippage)
		sellPx := price * (1 - cfg.Slippage)
		s := 0
		if strat.Signal(info, ks[:i+1], min) {
			s = 1
		}
		signals[i] = s
		if s == 1 && pos == 0 {
			cost := buyPx * float64(cfg.Size)
			fee := cost * cfg.FeeRate
			if fee < cfg.MinFee {
				fee = cfg.MinFee
			}
			if eq >= cost+fee {
				eq -= cost + fee
				pos += cfg.Size
				entry = buyPx
				trades = append(trades, Trade{Time: ks[i].Time.Unix(), Index: i, Price: buyPx, Side: "buy", Qty: cfg.Size})
			}
		} else if s == -1 && pos > 0 {
			proceeds := sellPx * float64(pos)
			fee := proceeds * cfg.FeeRate
			if fee < cfg.MinFee {
				fee = cfg.MinFee
			}
			eq += proceeds - fee
			trades = append(trades, Trade{Time: ks[i].Time.Unix(), Index: i, Price: sellPx, Side: "sell", Qty: pos})
			pos = 0
			entry = 0
		} else if pos > 0 {
			if cfg.StopLoss > 0 && entry > 0 {
				r := (sellPx - entry) / entry
				if r <= -cfg.StopLoss {
					proceeds := sellPx * float64(pos)
					fee := proceeds * cfg.FeeRate
					if fee < cfg.MinFee {
						fee = cfg.MinFee
					}
					eq += proceeds - fee
					trades = append(trades, Trade{Time: ks[i].Time.Unix(), Index: i, Price: sellPx, Side: "sell", Qty: pos})
					pos = 0
					entry = 0
				}
			}
			if cfg.TakeProfit > 0 && entry > 0 && pos > 0 {
				r := (sellPx - entry) / entry
				if r >= cfg.TakeProfit {
					proceeds := sellPx * float64(pos)
					fee := proceeds * cfg.FeeRate
					if fee < cfg.MinFee {
						fee = cfg.MinFee
					}
					eq += proceeds - fee
					trades = append(trades, Trade{Time: ks[i].Time.Unix(), Index: i, Price: sellPx, Side: "sell", Qty: pos})
					pos = 0
					entry = 0
				}
			}
		}
		mtm := eq + float64(pos)*price
		equity[i] = mtm
		cashSeries[i] = eq
		posSeries[i] = pos
		if i > 0 {
			rets = append(rets, (equity[i]-equity[i-1])/equity[i-1])
		}
		if mtm > peak {
			peak = mtm
		}
	}
	var totalRet float64
	if n > 0 && cfg.Cash > 0 {
		totalRet = (equity[n-1] - cfg.Cash) / cfg.Cash
	}
	maxDD := drawdown(equity)
	sharpe := sharpeRatio(rets)
	return Result{
		Equity:   equity,
		Cash:     cashSeries,
		Position: posSeries,
		Trades:   trades,
		Return:   totalRet,
		MaxDD:    maxDD,
		Sharpe:   sharpe,
		Klines:   ks,
		Signals:  signals,
	}
}

func drawdown(eq []float64) float64 {
	var peak float64
	var maxdd float64
	for _, v := range eq {
		if v > peak {
			peak = v
		}
		if peak > 0 {
			dd := (peak - v) / peak
			if dd > maxdd {
				maxdd = dd
			}
		}
	}
	return maxdd
}

func sharpeRatio(xs []float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	var mean float64
	for _, v := range xs {
		mean += v
	}
	mean /= float64(len(xs))
	var sd float64
	for _, v := range xs {
		d := v - mean
		sd += d * d
	}
	sd = math.Sqrt(sd / float64(len(xs)))
	if sd == 0 {
		return 0
	}
	return mean / sd * math.Sqrt(252)
}
