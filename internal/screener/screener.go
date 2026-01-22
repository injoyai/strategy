package screener

import (
	"time"

	"github.com/injoyai/strategy/internal/common"
	"github.com/injoyai/strategy/internal/data"
	"github.com/injoyai/strategy/internal/strategy"
	"github.com/injoyai/tdx/extend"
)

// Item 选股结果项
type Item struct {
	data.Info               //基本信息
	Score     float64       `json:"score"`  // 评分
	Signal    int           `json:"signal"` // 信号类型 1:买入 -1:卖出
	Klines    extend.Klines `json:"klines"` //
}

// Request 选股请求参数
type Request struct {
	Strategies []string `json:"strategies"` // 策略名称列表
	StartTime  int64    `json:"start_time"` // 开始时间(秒级时间戳)
	EndTime    int64    `json:"end_time"`   // 结束时间(秒级时间戳)
}

// Run 执行选股策略
func Run(req Request) (items []Item, err error) {

	// 获取策略实例
	strat, err := strategy.Group(req.Strategies)
	if err != nil {
		return nil, err
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
		func(info data.Info, ks extend.Klines) {
			// 判断是否满足策略条件
			if strat.Meet(info, ks) {
				// 构造返回结果
				items = append(items, Item{
					Info:   info, //基本信息
					Score:  0,    // 默认评分
					Signal: 0,    // 买卖信号
					Klines: ks,   // K线数据
				})
			}
		},
	)

	return

}
