package strategy

import (
	"strings"

	"github.com/injoyai/tdx/extend"
	"github.com/injoyai/tdx/protocol"
)

var _ Interface = (*SHExchange)(nil)

type SHExchange struct{}

func (SHExchange) Name() string {
	return "只选上交所"
}

func (SHExchange) Type() string { return DayKline }

func (SHExchange) Signal(info extend.Info, day, min extend.Klines) bool {
	return strings.HasPrefix(info.Code, protocol.ExchangeSH.String())
}

func init() {
	Register(SHExchange{})
}
