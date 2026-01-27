package main

import (
	"github.com/injoyai/conv/cfg"
	"github.com/injoyai/frame"
	"github.com/injoyai/logs"
	"github.com/injoyai/strategy/internal/api"
	"github.com/injoyai/strategy/internal/common"
	"github.com/injoyai/strategy/internal/strategy"
)

var (
	port      = cfg.GetInt("port", frame.DefaultPort)
	scriptDir = cfg.GetString("script_dir", "./data/strategy")
)

func main() {

	//初始化全局变量
	err := common.Init()
	logs.PanicErr(err)

	//自动更新数据
	common.Data.Start()

	//加载脚本
	err = strategy.Loading(scriptDir)
	logs.PanicErr(err)

	//运行服务
	err = api.Run(port)
	logs.Err(err)
}
