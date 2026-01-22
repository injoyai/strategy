package common

import (
	"github.com/injoyai/conv/cfg"
	"github.com/injoyai/goutil/database/sqlite"
	"github.com/injoyai/goutil/database/xorms"
	"github.com/injoyai/logs"
	"github.com/injoyai/strategy/internal/data"
	"github.com/injoyai/strategy/internal/lib"
	"github.com/injoyai/tdx"
	"github.com/traefik/yaegi/interp"
	"github.com/traefik/yaegi/stdlib"
)

var (
	Data *data.Data

	DB *xorms.Engine

	Script *interp.Interpreter

	BuildDate string
)

func Init() error {

	logs.Info("编译日期:", BuildDate)

	m, err := tdx.NewManage(
		tdx.WithClients(3),
		tdx.WithDialGbbqDefault(),
	)
	if err != nil {
		return err
	}
	Data, err = data.NewManage(m)
	if err != nil {
		return err
	}

	DB, err = sqlite.NewXorm(cfg.GetString("database.filename"))
	if err != nil {
		return err
	}

	Script = interp.New(interp.Options{})
	err = Script.Use(stdlib.Symbols)
	if err != nil {
		logs.Err(err)
	}
	err = Script.Use(lib.Symbols)
	if err != nil {
		logs.Err(err)
	}

	return nil
}
