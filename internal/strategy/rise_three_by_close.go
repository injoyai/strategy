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

func (RiseThreeByClose) Meet(info data.Info, ks extend.Klines) bool {
	if len(ks) < 3 {
		return false
	}
	return ks[len(ks)-1].Close > ks[len(ks)-2].Close &&
		ks[len(ks)-2].Close > ks[len(ks)-3].Close
}

func init() {
	Register(RiseThreeByClose{})
}
