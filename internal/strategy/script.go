package strategy

import (
	"github.com/injoyai/tdx/protocol"
)

var (
	_ Interface = (*Script)(nil)
)

func NewScript(name string, handler SignalsFunc) *Script {
	return &Script{name: name, handler: handler}
}

type Script struct {
	name    string
	handler SignalsFunc
}

func (this *Script) Name() string {
	return this.name
}

func (this *Script) Signals(ks protocol.Klines) []int {
	return this.handler(ks)
}
