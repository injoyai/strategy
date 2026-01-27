package strategy

import (
	"github.com/injoyai/tdx/extend"
)

func init() {
	Register(&TrendUp{
		Window:          8,  // 顶底判断窗口大小 (默认8)
		MinKlines:       30, // 最小K线数量要求 (默认30)
		MaxGainMultiple: 5,  // 高点涨幅和低点涨幅的最大差距倍数 (默认5)
	})
}

// TrendUp 上升趋势策略
// 逻辑：
// 1. 识别顶底：前后N个数据的最高点/最低点 (N=Window)
// 2. 取最新的2个顶点(H1, H2)和2个低点(L1, L2)
// 3. 要求顺序为 H1 -> L1 -> H2 -> L2 (时间先后)
// 4. 要求低点抬高(L2 > L1)，高点抬高(H2 > H1)
// 5. 要求低点小于高点(L < H)
// 6. 高点涨幅和低点涨幅的差距不能大于N倍(MaxGainMultiple)
type TrendUp struct {
	Window          int     // 顶底判断窗口大小 (默认8)
	MinKlines       int     // 最小K线数量要求 (默认30)
	MaxGainMultiple float64 // 高点涨幅和低点涨幅的最大差距倍数 (默认5)
}

func (s *TrendUp) Name() string {
	return "底部抬升"
}

func (s *TrendUp) Type() string {
	return DayKline
}

func (s *TrendUp) Signal(info extend.Info, day, min extend.Klines) bool {

	ks := day

	if s.MinKlines <= 0 {
		s.MinKlines = 30
	}
	if s.Window <= 0 {
		s.Window = 8
	}
	if s.MaxGainMultiple <= 0 {
		s.MaxGainMultiple = 5
	}

	if len(ks) < s.MinKlines {
		return false
	}

	// 定义关键点结构
	type Point struct {
		Index int
		Value int64
	}

	var highs []Point
	var lows []Point

	// 顶底判断的窗口大小 (前后8个 => 窗口半径8)
	window := s.Window
	// 允许右侧窗口最小为一半
	minRightWindow := window / 2

	// 从后往前遍历寻找关键点
	// 之前是从 len-1-window 开始，现在改为允许右边最少 minRightWindow
	startIdx := len(ks) - 1 - minRightWindow
	// 确保左边有足够的空间
	if startIdx < window {
		startIdx = window
	}

	for i := startIdx; i >= window; i-- {
		// 优化：如果已经找到足够的点，可以提前退出吗？
		// 我们需要最新的2个高点和2个低点。但为了确保顺序交替，我们可能需要多找几个，然后看最后4个是否满足？
		// 这里策略是：分别找 highs 和 lows 列表，然后取前两个（因为是倒序遍历，前两个就是最新的两个）
		if len(highs) >= 2 && len(lows) >= 2 {
			break
		}

		currentHigh := ks[i].High
		currentLow := ks[i].Low
		isHigh := true
		isLow := true

		// 检查左边 window 个点 (严格要求)
		for j := i - window; j < i; j++ {
			if ks[j].High > currentHigh {
				isHigh = false
			}
			if ks[j].Low < currentLow {
				isLow = false
			}
		}

		if !isHigh && !isLow {
			continue
		}

		// 检查右边
		// 正常情况下检查 window 长度
		// 如果右边剩余数量不足 window (但至少 minRightWindow)，则检查剩余的所有数据
		rightEnd := i + window
		if rightEnd >= len(ks) {
			rightEnd = len(ks) - 1
		}

		for j := i + 1; j <= rightEnd; j++ {
			if ks[j].High > currentHigh {
				isHigh = false
			}
			if ks[j].Low < currentLow {
				isLow = false
			}
		}

		if isHigh {
			highs = append(highs, Point{i, int64(currentHigh)})
		}
		if isLow {
			lows = append(lows, Point{i, int64(currentLow)})
		}
	}

	// 检查是否找到足够的点
	if len(highs) < 2 || len(lows) < 2 {
		return false
	}

	// 获取最新的两个点（注意highs/lows是倒序的，0是最新，1是次新）
	h2 := highs[0] // 最新高点
	h1 := highs[1] // 次新高点
	l2 := lows[0]  // 最新低点
	l1 := lows[1]  // 次新低点

	// 1. 验证时间顺序: H1 -> L1 -> H2 -> L2
	// 也就是 Index(H1) < Index(L1) < Index(H2) < Index(L2)
	if !(h1.Index < l1.Index && l1.Index < h2.Index && h2.Index < l2.Index) {
		return false
	}

	// 2. 验证价格形态
	// 低点越来越高
	if l2.Value <= l1.Value {
		return false
	}
	// 高点越来越高
	if h2.Value <= h1.Value {
		return false
	}
	// 低点不能大于高点 (L1 < H1, L2 < H2)
	// 注意：这里比较的是对应区间的顶底
	if l1.Value >= h1.Value {
		return false
	}
	if l2.Value >= h2.Value {
		return false
	}

	// 3. 验证涨幅差距
	if h1.Value == 0 || l1.Value == 0 {
		return false
	}
	// 高点涨幅
	hGain := float64(h2.Value-h1.Value) / float64(h1.Value)
	// 低点涨幅
	lGain := float64(l2.Value-l1.Value) / float64(l1.Value)

	if hGain > lGain*s.MaxGainMultiple || lGain > hGain*s.MaxGainMultiple {
		return false
	}

	return true
}
