package strategy

import (
	"strings"

	"github.com/injoyai/strategy/internal/data"
	"github.com/injoyai/tdx/extend"
	"github.com/injoyai/tdx/protocol"
)

var _ Interface = (*SHExchange)(nil)

type SHExchange struct{}

func (SHExchange) Name() string {
	return "上海交易所"
}

func (SHExchange) Type() string { return DayKline }

func (SHExchange) Signal(info data.Info, day, min extend.Klines) bool {
	return strings.HasPrefix(info.Code, protocol.ExchangeSH.String())
}

func init() {
	Register(SHExchange{})
}
