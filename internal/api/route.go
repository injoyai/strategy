package api

import (
	"mime"
	"time"

	"github.com/injoyai/frame/fbr"
	"github.com/injoyai/logs"
	dist "github.com/injoyai/strategy"
	"github.com/injoyai/strategy/internal/backtest"
	"github.com/injoyai/strategy/internal/common"
	"github.com/injoyai/strategy/internal/data"
	"github.com/injoyai/strategy/internal/screener"
	"github.com/injoyai/strategy/internal/strategy"
	"github.com/injoyai/tdx/extend"
)

func Run(port int) error {

	// 强制设置MIME类型，解决Windows下注册表缺失导致JS无法加载的问题
	_ = mime.AddExtensionType(".js", "application/javascript")
	_ = mime.AddExtensionType(".css", "text/css")

	common.DB.Sync2(new(strategy.Strategy))

	s := fbr.Default(
		fbr.WithPort(port),
		fbr.WithFS(dist.Dist, "web/dist"),
	)

	s.Group("/api", func(g fbr.Grouper) {
		g.Group("/strategy", func(g fbr.Grouper) {
			g.GET("/names", GetStrategyNames)
			g.GET("/all", GetStrategyAll)
			g.POST("/", PostStrategy)
			g.PUT("/", PutStrategy)
			g.PUT("/enable", PutStrategyEnable)
			g.DELETE("/", DelStrategy)
		})

		g.Group("/stock", func(g fbr.Grouper) {
			g.GET("/codes", GetCodes)
			g.GET("/klines", GetKlines)
			g.POST("/screener", GetScreener)
		})

		g.Group("/backtest", func(g fbr.Grouper) {
			g.POST("/", Backtest)
			g.GET("/all/ws", BacktestAllWS)
		})

	})

	return s.Run()
}

// GetCodes
// @Summary 获取股票代码
// @Description 获取股票代码
// @Tags 股票
// @Success 200 {array} CodesResp
func GetCodes(c fbr.Ctx) {
	codes := common.Data.GetStockCodes()
	ls := make([]*CodesResp, len(codes))
	for i, code := range codes {
		ls[i] = &CodesResp{
			Code: code,
			Name: common.Data.Codes.GetName(code),
		}
	}
	c.Succ(ls)
}

// GetKlines
// @Summary 获取K线
// @Description 获取K线
// @Tags K线
// @Param code query string true "股票代码例sz000001"
// @Param start query string true "开始时间"
// @Param end query string true "结束时间"
// @Success 200 {array} protocol.Kline
func GetKlines(c fbr.Ctx) {
	code := c.GetString("code")
	startStr := c.GetString("start", "1990-01-01")
	endStr := c.GetString("end", time.Now().Format(time.DateOnly))

	start, err := time.Parse("2006-01-02", startStr)
	c.CheckErr(err)

	end, err := time.Parse("2006-01-02", endStr)
	c.CheckErr(err)

	ks, err := common.Data.GetDayKlines(code, start, end)
	c.CheckErr(err)

	c.Succ(ks)
}

// GetScreener
// @Summary
// @Description
// @Tags
// @Param Authorization header string true "Authorization"
// Param data body x.X true "body"
// Param id query int false "id"
// Param name query string false "name"
// Success 200 {object} x.X
// Success 200 {array} x.X
// Success 200
// @Router / [get]
func GetScreener(c fbr.Ctx) {
	var req screener.Request
	c.Parse(&req)

	items, err := screener.Run(req)
	c.CheckErr(err)
	c.Succ(items)
}

func Backtest(c fbr.Ctx) {

	var req backtestReq
	c.Parse(&req)

	strat, err := strategy.Group(req.Strategies)
	c.CheckErr(err)

	var start, end time.Time
	if req.Start != "" {
		start, err = time.Parse("2006-01-02", req.Start)
		c.CheckErr(err)
	}
	if req.End != "" {
		end, err = time.Parse("2006-01-02", req.End)
		c.CheckErr(err)
	}

	dayKlines, err := common.Data.GetDayKlines(req.Code, start, end)
	c.CheckErr(err)

	minKlines, err := common.Data.GetMinKlines(req.Code, start, end)
	c.CheckErr(err)

	cash := req.Cash
	if cash <= 0 {
		cash = 100000
	}
	size := req.Size
	if size <= 0 {
		size = 1
	}
	if req.FeeRate <= 0 {
		req.FeeRate = 0.0005
	}
	if req.MinFee <= 0 {
		req.MinFee = 5
	}
	res := backtest.RunBacktestAdvanced(
		data.Info{
			Code:       "",
			Name:       "",
			Price:      0,
			Turnover:   0,
			FloatStock: 0,
			TotalStock: 0,
			FloatValue: 0,
			TotalValue: 0,
		},
		dayKlines, minKlines, strat,
		backtest.Settings{
			Cash:       cash,
			Size:       size,
			FeeRate:    req.FeeRate,
			MinFee:     req.MinFee,
			Slippage:   req.Slippage,
			StopLoss:   req.StopLoss,
			TakeProfit: req.TakeProfit,
		},
	)

	c.Succ(res)
}

func BacktestAllWS(c fbr.Ctx) {

	// 读取参数（query）
	strategyName := c.GetString("strategy")
	strat := strategy.Get(strategyName)
	if strat == nil {
		c.Err("strategy not found")
	}

	startStr := c.GetString("start")
	endStr := c.GetString("end")
	var start, end time.Time
	var err2 error
	if startStr != "" {
		start, err2 = time.Parse("2006-01-02", startStr)
		c.CheckErr(err2)
	} else {
		start = time.Date(1990, 1, 1, 0, 0, 0, 0, time.Local)
	}
	if endStr != "" {
		end, err2 = time.Parse("2006-01-02", endStr)
		c.CheckErr(err2)
	} else {
		end = time.Now()
	}

	settings := backtest.Settings{
		Cash:       c.GetFloat64("cash", 100000),
		Size:       c.GetInt("size", 1),
		FeeRate:    c.GetFloat64("fee_rate", 0.0005),
		MinFee:     c.GetFloat64("min_fee", 5),
		Slippage:   c.GetFloat64("slippage", 0),
		StopLoss:   c.GetFloat64("stop_loss", 0),
		TakeProfit: c.GetFloat64("take_profit", 0),
	}

	// WebSocket 接入（fasthttp）
	c.Websocket(func(conn *fbr.Websocket) {

		var sumRet, sumSharpe, sumDD float64
		var cnt int

		err := common.Data.RangeKlines(
			100, start, end,
			func(info data.Info, day, min extend.Klines) {
				res := backtest.RunBacktestAdvanced(info, day, min, strat, settings)
				item := BacktestItem{
					Code:        info.Code,
					Name:        common.Data.Codes.GetName(info.Code),
					Return:      res.Return,
					MaxDrawdown: res.MaxDD,
					Sharpe:      res.Sharpe,
				}
				sumRet += res.Return
				sumSharpe += res.Sharpe
				sumDD += res.MaxDD
				cnt++
				// 流式发送单条结果
				_ = conn.WriteJSON(map[string]any{"type": "item", "item": item})
			},
		)
		if err != nil {
			logs.Err(err)
			return
		}

		var avgRet, avgSharpe, avgDD float64
		if cnt > 0 {
			avgRet = sumRet / float64(cnt)
			avgSharpe = sumSharpe / float64(cnt)
			avgDD = sumDD / float64(cnt)
		}
		// 发送汇总
		_ = conn.WriteJSON(map[string]any{
			"type":             "summary",
			"avg_return":       avgRet,
			"avg_sharpe":       avgSharpe,
			"avg_max_drawdown": avgDD,
			"count":            cnt,
		})
	})

}

//func BacktestAll(c fbr.Ctx) {
//	var req backtestReq
//	c.Parse(&req)
//
//	strat, err := strategy.Group(req.Strategies)
//	c.CheckErr(err)
//
//	var start, end time.Time
//	if req.Start != "" {
//		start, err = time.Parse("2006-01-02", req.Start)
//		c.CheckErr(err)
//	} else {
//		start = time.Date(1990, 1, 1, 0, 0, 0, 0, time.Local)
//	}
//	if req.End != "" {
//		end, err = time.Parse("2006-01-02", req.End)
//		c.CheckErr(err)
//	} else {
//		end = time.Now()
//	}
//
//	cash := req.Cash
//	if cash <= 0 {
//		cash = 100000
//	}
//	size := req.Size
//	if size <= 0 {
//		size = 1
//	}
//	if req.FeeRate <= 0 {
//		req.FeeRate = 0.0005
//	}
//	if req.MinFee <= 0 {
//		req.MinFee = 5
//	}
//
//	settings := backtest.Settings{
//		Cash:       cash,
//		Size:       size,
//		FeeRate:    req.FeeRate,
//		MinFee:     req.MinFee,
//		Slippage:   req.Slippage,
//		StopLoss:   req.StopLoss,
//		TakeProfit: req.TakeProfit,
//	}
//
//	codes := common.Data.GetStockCodes()
//	items := make([]BacktestItem, 0, len(codes))
//	var sumRet, sumSharpe, sumDD float64
//	var cnt int
//	for _, code := range codes {
//		ks, err := common.Data.GetDayKlines(code, start, end)
//		if err != nil || len(ks) == 0 {
//			continue
//		}
//		res := backtest.RunBacktestAdvanced(code, common.Data.Codes.GetName(code), ks, strat, settings)
//		item := BacktestItem{
//			Code:        code,
//			Name:        common.Data.Codes.GetName(code),
//			Return:      res.Return,
//			MaxDrawdown: res.MaxDD,
//			Sharpe:      res.Sharpe,
//		}
//		items = append(items, item)
//		sumRet += res.Return
//		sumSharpe += res.Sharpe
//		sumDD += res.MaxDD
//		cnt++
//	}
//
//	// sort by return desc
//	for i := 0; i < len(items); i++ {
//		for j := i + 1; j < len(items); j++ {
//			if items[j].Return > items[i].Return {
//				items[i], items[j] = items[j], items[i]
//			}
//		}
//	}
//	if len(items) > 200 {
//		items = items[:200]
//	}
//
//	var avgRet, avgSharpe, avgDD float64
//	if cnt > 0 {
//		avgRet = sumRet / float64(cnt)
//		avgSharpe = sumSharpe / float64(cnt)
//		avgDD = sumDD / float64(cnt)
//	}
//
//	resp := BacktestAllResp{
//		AvgReturn:      avgRet,
//		AvgSharpe:      avgSharpe,
//		AvgMaxDrawdown: avgDD,
//		Count:          cnt,
//		Items:          items,
//	}
//	c.Succ(resp)
//}
