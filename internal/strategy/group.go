package strategy

import (
	"errors"
	"fmt"

	"github.com/injoyai/tdx/extend"
)

type group struct {
	List []Interface
}

func (c *group) Name() string {
	return "Group"
}

func (c *group) Type() string {
	return DayKline
}

func (c *group) Signal(info extend.Info, day, min extend.Klines) bool {
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
