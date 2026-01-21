package screener

import (
	"fmt"
	"time"

	"github.com/injoyai/strategy/internal/common"
	"github.com/injoyai/strategy/internal/strategy"
	"github.com/injoyai/tdx/extend"
)

// Item 选股结果项
type Item struct {
	Code   string  `json:"code"`   // 股票代码
	Name   string  `json:"name"`   //股票名称
	Score  float64 `json:"score"`  // 评分
	Price  float64 `json:"price"`  // 最新价格
	Signal int     `json:"signal"` // 信号类型 1:买入 -1:卖出
}

// Request 选股请求参数
type Request struct {
	Strategy  string `json:"strategy"`   // 策略名称
	StartTime int64  `json:"start_time"` // 开始时间(秒级时间戳)
	EndTime   int64  `json:"end_time"`   // 结束时间(秒级时间戳)
}

// Run 执行选股策略
func Run(req Request) (items []Item, err error) {

	// 获取策略实例
	strat := strategy.Get(req.Strategy)
	if strat == nil {
		return nil, fmt.Errorf("strategy %s not found", req.Strategy)
	}

	// 如果未指定结束时间，默认为当前时间
	if req.EndTime == 0 {
		req.EndTime = time.Now().Unix()
	}

	// 遍历所有股票的日K线数据
	err = common.Data.RangeDayKlines(
		100, // 并发数
		time.Unix(req.StartTime, 0),
		time.Unix(req.EndTime, 0),
		func(code, name string, ks extend.Klines) {
			// 判断是否满足策略条件
			if strat.Meet(code, name, ks) {
				var price float64
				// 获取最新收盘价
				if len(ks) > 0 {
					price = float64(ks[len(ks)-1].Close)
				}
				// 构造返回结果
				items = append(items, Item{
					Code:   code,
					Name:   name,
					Price:  price, // 最新收盘价
					Score:  0,     // 默认评分
					Signal: 0,     // 默认买入信号
				})
			}
		},
	)

	return

}
