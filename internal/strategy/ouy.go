package strategy

import (
	"github.com/injoyai/strategy/internal/data"
	"github.com/injoyai/tdx/extend"
)

// Ouy 欧阳总策略结构体
type Ouy struct {
	LimitUpThreshold    float64 // 涨停阈值（默认0.098，即9.8%）
	RecentDaysToCheck   int     // 检查最近多少个交易日（默认20天）
	ConsecutiveBullDays int     // 跳空后要求的连续阳线天数（含跳空当天，默认2天）
	VolumeAvgDays       int     // 成交量均线计算天数（默认5天）
}

func (o *Ouy) Name() string { return "欧阳总策略" }

func (o *Ouy) Type() string { return DayKline }

// Signal 选股策略，满足以下4个条件：
// 1. 近N个交易日内出现过一次涨停（涨幅 ≥ LimitUpThreshold）
// 2. 涨停之后的下一天出现向上跳空高开（当日开盘价 > 涨停日收盘价）
// 3. 跳空之后出现连续阳线（至少 ConsecutiveBullDays 根连续阳线，含跳空当天）
// 4. 最新一个交易日的成交量明显放大（> 过去M日均量 且 > 昨日成交量）
func (o *Ouy) Signal(info data.Info, klines, minKlines extend.Klines) bool {
	if o.RecentDaysToCheck <= 0 {
		o.RecentDaysToCheck = 20
	}

	if len(klines) < o.RecentDaysToCheck {
		return false
	}

	foundPattern := false

	// 从最近的K线往前检查，限定在最近N个交易日内
	n := len(klines)
	startIndex := n - o.RecentDaysToCheck
	if startIndex < 0 {
		startIndex = 0
	}
	// 确保有前一日数据用于计算涨跌幅
	if startIndex == 0 {
		startIndex = 1
	}

	// 条件1+2+3：寻找「涨停 → 次日跳空 → 跳空后连续阳线」的形态
	for i := startIndex; i < n-1; i++ {
		curr := klines[i]
		prev := klines[i-1]

		// 条件1：涨停（用收盘价相对昨收涨幅近似判断）
		if prev.Close == 0 {
			continue
		}
		change := (float64(curr.Close) - float64(prev.Close)) / float64(prev.Close)

		if change >= o.LimitUpThreshold {
			// 找到涨停日，检查下一交易日是否跳空高开
			next := klines[i+1]

			// 条件2：跳空（次日开盘价 > 涨停日收盘价）
			if float64(next.Open) > float64(curr.Close) {
				// 条件3：跳空后有连续阳线，这里从跳空当天开始数
				bullOk := true
				for j := 0; j < o.ConsecutiveBullDays; j++ {
					idx := i + 1 + j
					if idx >= n {
						bullOk = false
						break
					}
					day := klines[idx]
					// 简单定义：收盘价 > 开盘价 为阳线
					if !(day.Close > day.Open) {
						bullOk = false
						break
					}
				}
				if bullOk {
					foundPattern = true
					break
				}
			}
		}
	}

	if !foundPattern {
		return false
	}

	// 条件4：最近成交量放大
	// 要求：最新一日成交量 > 过去M日平均成交量 且 > 昨日成交量

	lastIndex := n - 1
	lastVol := float64(klines[lastIndex].Volume)

	sumVol := 0.0
	count := 0

	// 计算过去M日的成交量总和（不含今日）
	for i := 1; i <= o.VolumeAvgDays; i++ {
		idx := lastIndex - i
		if idx >= 0 {
			sumVol += float64(klines[idx].Volume)
			count++
		}
	}

	if count > 0 {
		avgVol := sumVol / float64(count)
		prevVol := float64(klines[lastIndex-1].Volume)

		// 必须放量：大于均量 且 大于昨量
		if lastVol > avgVol && lastVol > prevVol {
			return true
		}
	}

	return false
}

func init() {
	// 使用默认配置注册
	Register(&Ouy{
		LimitUpThreshold:    0.098, // 涨停阈值（默认0.098，即9.8%）
		RecentDaysToCheck:   20,    // 检查最近多少个交易日（默认20天）
		ConsecutiveBullDays: 2,     // 跳空后要求的连续阳线天数（含跳空当天，默认2天）
		VolumeAvgDays:       5,     // 成交量均线计算天数（默认5天）
	})
}
