package screener

import (
	"fmt"
	"time"

	"github.com/injoyai/strategy/internal/common"
	"github.com/injoyai/strategy/internal/strategy"
	"github.com/injoyai/tdx/extend"
	"github.com/injoyai/tdx/protocol"
)

type Item struct {
	Code   string         `json:"code"`
	Score  float64        `json:"score"`
	Price  protocol.Price `json:"price"`
	Signal int            `json:"signal"`
}

type Request struct {
	Strategy  string `json:"strategy"`
	StartTime int64  `json:"start_time"`
	EndTime   int64  `json:"end_time"`
}

func Run(req Request) (kss []extend.Klines, err error) {

	strat := strategy.Get(req.Strategy)
	if strat == nil {
		return nil, fmt.Errorf("strategy %s not found", req.Strategy)
	}

	err = common.Data.RangeDayKlines(
		time.Unix(req.StartTime, 0),
		time.Unix(req.EndTime, 0),
		func(code, name string, ks extend.Klines) {
			if strat.Meet(code, name, ks) {
				kss = append(kss, ks)
			}
		},
	)

	return

}
