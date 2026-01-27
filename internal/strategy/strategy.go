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
	custom   = map[string]Interface{}
	internal = map[string]Interface{}
)

func Register(s Interface) {
	internal[s.Name()] = s
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
	i := NewScript(s.Name, s.Type, f)
	custom[i.Name()] = i
	return nil
}

func Get(name string) Interface {
	i, ok := custom[name]
	if ok {
		return i
	}
	return internal[name]
}

func Del(name string) {
	delete(custom, name)
}

func Names(_type string) (out []string) {
	switch _type {
	case "custom":
		out = make([]string, 0, len(custom))
		for k := range custom {
			out = append(out, k)
		}
	case "internal":
		fallthrough
	default:
		out = make([]string, 0, len(internal))
		for k := range internal {
			out = append(out, k)
		}
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
