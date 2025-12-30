package main

import (
	"github.com/injoyai/conv/cfg"
	"github.com/injoyai/frame"
	"github.com/injoyai/logs"
	"github.com/injoyai/trategy/internal/api"
	"github.com/injoyai/trategy/internal/common"
)

func main() {
	logs.PanicErr(common.Init())
	common.Data.Start()
	port := cfg.GetInt("port", frame.DefaultPort)
	logs.Err(api.Run(port))
}
