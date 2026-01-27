package strategy

import (
	"strings"

	"github.com/injoyai/strategy/internal/data"
	"github.com/injoyai/tdx/extend"
)

var _ Interface = (*SZExchange)(nil)

type NoBuyLimit struct{}

func (NoBuyLimit) Name() string {
	return "无资金要求"
}

func (NoBuyLimit) Type() string { return DayKline }

func (NoBuyLimit) Signal(info data.Info, day, min extend.Klines) bool {
	return strings.HasPrefix(info.Code, "sh6") ||
		strings.HasPrefix(info.Code, "sz0")
}

func init() {
	Register(NoBuyLimit{})
}
