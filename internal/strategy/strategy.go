package strategy

import (
	"errors"

	"github.com/injoyai/tdx/protocol"
	"github.com/injoyai/trategy/internal/common"
)

type Interface interface {
	Name() string
	Signals(ks protocol.Klines) []int
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
	Register(NewScript(s.Name, f))
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

type SignalsFunc = func(ks protocol.Klines) []int
