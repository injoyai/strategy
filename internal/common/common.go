package common

import (
	"time"

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

	if len(BuildDate) > 0 {
		logs.Info("编译日期:", BuildDate)
		buildTime, err := time.Parse(time.DateOnly, BuildDate)
		logs.PrintErr(err)
		if err == nil && time.Now().Sub(buildTime) > time.Hour*24*180 {
			logs.Err("数据获取失败,请尝试更新版本")
		}
	}

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
