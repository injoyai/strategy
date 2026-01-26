package strategy

import (
	"errors"
	"fmt"

	"github.com/injoyai/strategy/internal/common"
	"github.com/injoyai/strategy/internal/data"
	"github.com/injoyai/tdx/extend"
)

const (
	DayKline = "day-kline"
)

type Interface interface {
	Name() string                                       //策略名称
	Type() string                                       //策略类型
	Signal(info data.Info, day, min extend.Klines) bool //策略
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
	f, ok := res.Interface().(SignalFunc)
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

/*



 */

type group struct {
	List []Interface
}

func (c *group) Name() string {
	return "Group"
}

func (c *group) Type() string {
	return DayKline
}

func (c *group) Signal(info data.Info, day, min extend.Klines) bool {
	for _, s := range c.List {
		if !s.Signal(info, day, min) {
			return false
		}
	}
	return true
}

func Group(names []string) (Interface, error) {
	if len(names) == 0 {
		return nil, errors.New("未选择策略")
	}
	c := &group{}
	for _, name := range names {
		s := Get(name)
		if s == nil {
			return nil, fmt.Errorf("策略[%s]不存在", name)
		}
		c.List = append(c.List, s)
	}
	return c, nil
}
