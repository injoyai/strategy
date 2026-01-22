package strategy

import (
	"strings"

	"github.com/injoyai/strategy/internal/data"
	"github.com/injoyai/tdx/extend"
	"github.com/injoyai/tdx/protocol"
)

var _ Interface = (*BJExchange)(nil)

type BJExchange struct{}

func (BJExchange) Name() string {
	return "北京交易所"
}

func (BJExchange) Type() string { return DayKline }

func (BJExchange) Meet(info data.Info, ks extend.Klines) bool {
	return strings.HasPrefix(info.Code, protocol.ExchangeSH.String())
}

func init() {
	Register(BJExchange{})
}
