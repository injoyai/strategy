package strategy

import (
	"github.com/injoyai/strategy/internal/data"
	"github.com/injoyai/tdx/extend"
)

var _ Interface = (*RiseThreeByClose)(nil)

type RiseThreeByClose struct{}

func (RiseThreeByClose) Name() string {
	return "连涨3天(收盘价)"
}

func (RiseThreeByClose) Type() string { return DayKline }

func (RiseThreeByClose) Signal(info data.Info, day, min extend.Klines) bool {
	if len(day) < 3 {
		return false
	}
	return day[len(day)-1].Close > day[len(day)-2].Close &&
		day[len(day)-2].Close > day[len(day)-3].Close
}

func init() {
	Register(RiseThreeByClose{})
}
