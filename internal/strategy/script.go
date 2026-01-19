package strategy

import (
	"fmt"

	"github.com/injoyai/tdx/extend"
)

var (
	_ Interface = (*Script)(nil)
)

type SignalsFunc = func(ks []*extend.Kline) bool

func NewScript(name, _type string, handler SignalsFunc) *Script {
	return &Script{name: name, _type: _type, handler: handler}
}

type Script struct {
	name    string
	_type   string
	handler SignalsFunc
}

func (this *Script) Name() string {
	return this.name
}

func (this *Script) Type() string { return this._type }

func (this *Script) Select(code, name string, ks []*extend.Kline) bool {
	return this.handler(ks)
}

/*



 */

const (
	DefaultScript = `
import (
	"github.com/injoyai/tdx/extend"
)

func Signals(code,name string,ks extend.Klines) bool {
	return false
}

`
)

type Strategy struct {
	Name    string `xorm:"pk"`
	Type    string
	Script  string
	Enable  bool
	Package string
}

func (this *Strategy) Content() string {
	return fmt.Sprintf("package %s\n%s", this.Package, this.Script)
}

type CreateReq struct {
	Name   string
	Script string
	Enable bool
}

type EnableReq struct {
	Name   string
	Enable bool
}
