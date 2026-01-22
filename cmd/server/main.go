package main

import (
	"github.com/injoyai/conv/cfg"
	"github.com/injoyai/frame"
	"github.com/injoyai/logs"
	"github.com/injoyai/strategy/internal/api"
	"github.com/injoyai/strategy/internal/common"
)

var (
	port = cfg.GetInt("port", frame.DefaultPort)
)

func main() {
	err := common.Init()
	logs.PanicErr(err)

	common.Data.Start()

	logs.Err(api.Run(port))
}
