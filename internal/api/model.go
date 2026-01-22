package api

type backtestReq struct {
	Strategy   string   `json:"strategy"`
	Strategies []string `json:"strategies"`
	Code       string   `json:"code"`
	Start      string   `json:"start"`
	End        string   `json:"end"`
	Cash       float64  `json:"cash"`
	Size       int      `json:"size"`
	FeeRate    float64  `json:"fee_rate"`
	MinFee     float64  `json:"min_fee"`
	Slippage   float64  `json:"slippage"`
	StopLoss   float64  `json:"stop_loss"`
	TakeProfit float64  `json:"take_profit"`
}

type CodesResp struct {
	Code string
	Name string
}

type BacktestItem struct {
	Code        string  `json:"code"`
	Name        string  `json:"name"`
	Return      float64 `json:"return"`
	MaxDrawdown float64 `json:"max_drawdown"`
	Sharpe      float64 `json:"sharpe"`
}

type BacktestAllResp struct {
	AvgReturn      float64        `json:"avg_return"`
	AvgSharpe      float64        `json:"avg_sharpe"`
	AvgMaxDrawdown float64        `json:"avg_max_drawdown"`
	Count          int            `json:"count"`
	Items          []BacktestItem `json:"items"`
}
