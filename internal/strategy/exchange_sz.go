package strategy

import (
	"strings"

	"github.com/injoyai/tdx/extend"
	"github.com/injoyai/tdx/protocol"
)

var _ Interface = (*SZExchange)(nil)

type SZExchange struct{}

func (SZExchange) Name() string {
	return "只选深交所"
}

func (SZExchange) Type() string { return DayKline }

func (SZExchange) Signal(info extend.Info, day, min extend.Klines) bool {
	return strings.HasPrefix(info.Code, protocol.ExchangeSH.String())
}

func init() {
	Register(SZExchange{})
}
