package strategy

import (
	"strings"

	"github.com/injoyai/tdx/extend"
	"github.com/injoyai/tdx/protocol"
)

var _ Interface = (*BJExchange)(nil)

type BJExchange struct{}

func (BJExchange) Name() string {
	return "只选北交所"
}

func (BJExchange) Type() string { return DayKline }

func (BJExchange) Signal(info extend.Info, day, min extend.Klines) bool {
	return strings.HasPrefix(info.Code, protocol.ExchangeSH.String())
}

func init() {
	Register(BJExchange{})
}
