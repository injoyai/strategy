package screener

import (
	"sort"
	"time"

	"github.com/injoyai/strategy/internal/common"
	"github.com/injoyai/strategy/internal/strategy"
	"github.com/injoyai/tdx/protocol"
)

type Item struct {
	Symbol string         `json:"symbol"`
	Score  float64        `json:"score"`
	Price  protocol.Price `json:"price"`
	Signal int            `json:"signal"`
}

type Request struct {
	Strategy string  `json:"strategy"`
	Lookback int     `json:"lookback"`
	MinScore float64 `json:"min_score"`
	Signal   int     `json:"signal"`
}

func Run(req Request) ([]Item, error) {
	codes := common.Data.GetStockCodes()
	out := make([]Item, 0, len(codes))
	strat := strategy.Get(req.Strategy)
	if strat == nil {
		strat = strategy.SMA{Fast: 5, Slow: 20}
	}
	for _, code := range codes {
		ks, err := common.Data.GetDayKlines(code, time.Now().AddDate(-1, 0, 0), time.Now())
		if err != nil {
			return nil, err
		}
		if len(ks) == 0 {
			continue
		}
		sigs := strat.Signals(ks)
		last := len(ks) - 1
		lb := req.Lookback
		if lb <= 0 || lb > last {
			lb = 10
		}
		var ret float64
		start := last - lb
		if start < 1 {
			start = 1
		}
		for i := start; i <= last; i++ {
			prev := ks[i-1].Close
			d := (ks[i].Close - prev) / prev
			ret += d.Float64()
		}
		item := Item{
			Symbol: code,
			Score:  ret,
			Price:  ks[last].Close,
			Signal: sigs[last],
		}
		if req.MinScore != 0 && item.Score < req.MinScore {
			continue
		}
		if req.Signal != 0 && item.Signal != req.Signal {
			continue
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out, nil
}
