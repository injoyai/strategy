package strategy

import "github.com/injoyai/tdx/protocol"

var _ Interface = (*Test)(nil)

type Test struct{}

func (this Test) Name() string {
	return "测试"
}

func (this Test) Signals(ks protocol.Klines) []int {
	out := make([]int, len(ks))
	return out
}

func init() {
	Register(Test{})
}
