package strategy

import (
	"fmt"

	"github.com/injoyai/tdx/extend"
)

var (
	_ Interface = (*script)(nil)
)

type SignalFunc = func(info extend.Info, day, min extend.Klines) bool

func NewScript(name, _type string, handler SignalFunc) *script {
	return &script{name: name, _type: _type, handler: handler}
}

type script struct {
	name    string
	_type   string
	handler SignalFunc
}

func (this *script) Name() string {
	return this.name
}

func (this *script) Type() string { return this._type }

func (this *script) Signal(info extend.Info, day, min extend.Klines) bool {
	return this.handler(info, day, min)
}

/*



 */

const (
	DefaultScript = `
import (
	"github.com/injoyai/tdx/extend"
)

func Signal(info extend.Info,day,min extend.Klines) bool {
	return false
}

`
)

type Script struct {
	Name    string `xorm:"pk"`
	Type    string
	Script  string
	Enable  bool
	Package string
}

func (this *Script) Content() string {
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
