package strategy

import (
	"github.com/injoyai/tdx/extend"
)

var _ Interface = (*BullishAlignment)(nil)

type BullishAlignment struct{}

func (BullishAlignment) Name() string {
	return "多头排列(5,10,20,30)"
}

func (BullishAlignment) Type() string { return DayKline }

func (BullishAlignment) Signal(info extend.Info, dks, min extend.Klines) bool {

	if len(dks) < 31 {
		return false
	}

	// 1. 计算当日均线
	ma5 := MA(dks, 5)
	ma10 := MA(dks, 10)
	ma20 := MA(dks, 20)
	ma30 := MA(dks, 30)

	// 计算昨日均线
	prevDks := dks[:len(dks)-1]
	prevMa5 := MA(prevDks, 5)
	prevMa10 := MA(prevDks, 10)
	prevMa20 := MA(prevDks, 20)
	prevMa30 := MA(prevDks, 30)

	// 2. 判断均线多头排列 (MA5 > MA10 > MA20 > MA30) 且 均线向上 (当日 > 昨日)
	// 且是刚刚变成多头排列 (昨日不是多头排列)
	isCurrentBullish := ma5 > ma10 && ma10 > ma20 && ma20 > ma30
	isPrevBullish := prevMa5 > prevMa10 && prevMa10 > prevMa20 && prevMa20 > prevMa30

	if !isCurrentBullish || isPrevBullish {
		return false
	}

	if !(ma5 > prevMa5 && ma10 > prevMa10 && ma20 > prevMa20 && ma30 > prevMa30) {
		return false
	}

	return true
}

func init() {
	Register(BullishAlignment{})
}

func MA(dks extend.Klines, n int) float64 {
	if len(dks) < n {
		return 0
	}
	sum := 0.0
	// 取最后n个
	for _, k := range dks[len(dks)-n:] {
		sum += k.Close.Float64()
	}
	return sum / float64(n)
}
