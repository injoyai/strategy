package strategy

import (
	"github.com/injoyai/tdx/extend"
)

var _ Interface = (*Test)(nil)

type Test struct {
	selected map[string]struct{}
}

func (this Test) Name() string {
	return "测试"
}

func (this Test) Type() string { return DayKline }

func (this Test) Meet(code, name string, ks extend.Klines) bool {
	_, ok := this.selected[code]
	return ok
}

func init() {
	Register(Test{map[string]struct{}{
		"bj920000": {},
		"sh600000": {},
		"sz000001": {},
	}})
}
