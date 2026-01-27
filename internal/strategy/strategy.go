package strategy

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/injoyai/conv"
	"github.com/injoyai/logs"
	"github.com/injoyai/strategy/internal/common"
	"github.com/injoyai/tdx/extend"
)

const (
	DayKline = "day-kline"
	GoExt    = ".go"
)

type Interface interface {
	Name() string                                         //策略名称
	Type() string                                         //策略类型
	Signal(info extend.Info, day, min extend.Klines) bool //策略
}

var (
	strategies = map[string]Interface{}
)

func Register(s Interface) {
	strategies[s.Name()] = s
}

func RegisterScript(s *Script) error {
	if !s.Enable {
		return nil
	}

	res, err := common.Script.Eval(s.Content())
	if err != nil {
		return err
	}
	res, err = common.Script.Eval(s.FuncName())
	if err != nil {
		return err
	}
	f, ok := res.Interface().(func(extend.Info, extend.Klines, extend.Klines) bool)
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

func Names() []string {
	out := make([]string, 0, len(strategies))
	for k := range strategies {
		out = append(out, k)
	}
	return out
}

/*



 */

func Loading(dir string) error {

	err := LoadingDatabase()
	if err != nil {
		return err
	}

	err = LoadingFile(dir)
	if err != nil {
		return err
	}

	return nil
}

func LoadingDatabase() error {
	err := common.DB.Sync2(new(Script))
	if err != nil {
		return err
	}
	ls := []*Script(nil)
	err = common.DB.Find(&ls)
	if err != nil {
		return err
	}
	for _, s := range ls {
		if err = RegisterScript(s); err != nil {
			return err
		}
	}
	return nil
}

func LoadingFile(dir string) error {

	es, err := os.ReadDir(dir)
	if err != nil {
		return err
	}

	for _, f := range es {
		if f.IsDir() || strings.HasSuffix(f.Name(), ".go") {
			continue
		}
		bs, err := os.ReadFile(filepath.Join(dir, f.Name()))
		if err != nil {
			return err
		}
		bs = bytes.TrimLeft(bs, " ")
		bs = bytes.TrimPrefix(bs, []byte("package strategy"))
		name := strings.TrimSuffix(f.Name(), GoExt)
		if err = RegisterScript(&Script{
			Name:    name,
			Type:    DayKline,
			Script:  string(bs),
			Enable:  true,
			Package: name + conv.String(time.Now().Unix()),
		}); err != nil {
			logs.Err(err)
			continue
		}
	}

	return nil
}
