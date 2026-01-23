package api

import (
	"sort"
	"time"

	"github.com/injoyai/conv"
	"github.com/injoyai/frame/fbr"
	"github.com/injoyai/strategy/internal/common"
	"github.com/injoyai/strategy/internal/strategy"
)

// GetStrategyNames
// @Summary 获取策略名称
// @Description 获取策略名称
// @Tags 策略
// @Success 200 {array} string
func GetStrategyNames(c fbr.Ctx) {
	names := strategy.Registry()
	sort.Strings(names)
	c.Succ(names)
}

// GetStrategyAll
// @Summary 获取全部策略
// @Description 获取全部策略
// @Tags 策略
// @Success 200 {array} strategy.Strategy
func GetStrategyAll(c fbr.Ctx) {
	data := []*strategy.Strategy(nil)
	err := common.DB.Find(&data)
	c.CheckErr(err)
	c.Succ(data)
}

// PostStrategy
// @Summary 创建策略
// @Description 创建策略
// @Tags 策略
// @Param data body strategy.CreateReq true "body"
// @Success 200
func PostStrategy(c fbr.Ctx) {
	var req strategy.CreateReq
	c.Parse(&req)
	if req.Name == "" {
		c.Err("name is required")
	}

	s := &strategy.Strategy{
		Name:    req.Name,
		Type:    strategy.DayKline,
		Script:  strategy.DefaultScript,
		Enable:  req.Enable,
		Package: req.Name + conv.String(time.Now().Unix()),
	}

	_, err := common.DB.Insert(s)
	c.CheckErr(err)

	if req.Enable {
		err = strategy.RegisterScript(s)
		c.CheckErr(err)
	} else {
		strategy.Del(req.Name)
	}

	c.Succ(s)
}

func PutStrategy(c fbr.Ctx) {
	var req strategy.CreateReq
	c.Parse(&req)

	s := new(strategy.Strategy)
	_, err := common.DB.Where("Name=?", req.Name).Get(s)
	c.CheckErr(err)

	s.Script = req.Script
	s.Enable = req.Enable
	s.Package = req.Name + conv.String(time.Now().Unix())

	_, err = common.DB.Where("Name=?", req.Name).Cols("Script,Enable,Package").Update(s)
	c.CheckErr(err)

	if req.Enable {
		err = strategy.RegisterScript(s)
		c.CheckErr(err)
	} else {
		strategy.Del(req.Name)
	}

	c.Succ(s)
}

func PutStrategyEnable(c fbr.Ctx) {
	var req strategy.EnableReq
	c.Parse(&req)

	s := new(strategy.Strategy)
	_, err := common.DB.Where("Name=?", req.Name).Get(s)
	c.CheckErr(err)

	if s.Enable == req.Enable {
		c.Succ(nil)
	}

	_, err = common.DB.Where("Name=?", req.Name).Cols("Enable").Update(&strategy.Strategy{
		Enable: req.Enable,
	})
	c.CheckErr(err)

	if req.Enable {
		err = strategy.RegisterScript(s)
		c.CheckErr(err)
	} else {
		strategy.Del(req.Name)
	}

	c.Succ(nil)
}

func DelStrategy(c fbr.Ctx) {
	name := c.GetString("Name")
	if len(name) == 0 {
		c.Succ(nil)
	}
	_, err := common.DB.Where("Name=?", name).Delete(&strategy.Strategy{})
	c.CheckErr(err)
	strategy.Del(name)
	c.Succ(nil)
}
