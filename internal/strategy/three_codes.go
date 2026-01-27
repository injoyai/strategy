package strategy

import (
	"github.com/injoyai/tdx/extend"
)

var _ Interface = (*Test)(nil)

type Test struct {
	selected map[string]struct{}
}

func (this Test) Name() string {
	return "三个测试股票"
}

func (this Test) Type() string { return DayKline }

func (this Test) Signal(info extend.Info, day, min extend.Klines) bool {
	_, ok := this.selected[info.Code]
	return ok
}

func init() {
	//Register(Test{map[string]struct{}{
	//	"bj920000": {},
	//	"sh600000": {},
	//	"sz000001": {},
	//}})
}
