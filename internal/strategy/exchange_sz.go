package strategy

import (
	"strings"

	"github.com/injoyai/strategy/internal/data"
	"github.com/injoyai/tdx/extend"
	"github.com/injoyai/tdx/protocol"
)

var _ Interface = (*SZExchange)(nil)

type SZExchange struct{}

func (SZExchange) Name() string {
	return "深圳交易所"
}

func (SZExchange) Type() string { return DayKline }

func (SZExchange) Meet(info data.Info, ks extend.Klines) bool {
	return strings.HasPrefix(info.Code, protocol.ExchangeSH.String())
}

func init() {
	Register(SZExchange{})
}
