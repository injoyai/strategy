package strategy

import (
	"errors"

	"github.com/injoyai/strategy/internal/common"
	"github.com/injoyai/tdx/extend"
)

const (
	DayKline = "day-kline"
)

type Interface interface {
	Name() string                                      //策略名称
	Type() string                                      //策略类型
	Select(code, name string, ks []*extend.Kline) bool //策略
}

var strategies = map[string]Interface{}

func Register(s Interface) {
	strategies[s.Name()] = s
}

func RegisterScript(s *Strategy) error {
	res, err := common.Script.Eval(s.Content())
	if err != nil {
		return err
	}
	f, ok := res.Interface().(SignalsFunc)
	if !ok {
		return errors.New("脚本函数有误")
	}
	Register(NewScript(s.Name, s.Type, f))
	return nil
}

func Get(name string) Interface {
	return strategies[name]
}

func Del(name string) {
	delete(strategies, name)
}

func Registry() []string {
	out := make([]string, 0, len(strategies))
	for k := range strategies {
		out = append(out, k)
	}
	return out
}
