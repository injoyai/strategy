package strategy

import "fmt"

type Strategy struct {
	Name    string `xorm:"pk"`
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
