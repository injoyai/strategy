package strategy

import (
	"fmt"

	"github.com/injoyai/strategy/internal/data"
	"github.com/injoyai/tdx/extend"
)

var (
	_ Interface = (*Script)(nil)
)

type SignalFunc = func(info data.Info, day, min extend.Klines) bool

func NewScript(name, _type string, handler SignalFunc) *Script {
	return &Script{name: name, _type: _type, handler: handler}
}

type Script struct {
	name    string
	_type   string
	handler SignalFunc
}

func (this *Script) Name() string {
	return this.name
}

func (this *Script) Type() string { return this._type }

func (this *Script) Signal(info data.Info, day, min extend.Klines) bool {
	return this.handler(info, day, min)
}

/*



 */

const (
	DefaultScript = `
import (
	"github.com/injoyai/tdx/extend"
)

func Signal(code,name string,day,min extend.Klines) bool {
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
