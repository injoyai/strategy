package strategy

import (
	"github.com/injoyai/tdx/extend"
)

var (
	Name = "三个测试股票"
)

func Signal(info extend.Info, day, min extend.Klines) bool {
	switch info.Code {
	case "bj920000", "sh600000", "sz000001":
		return true
	}
	return false
}
