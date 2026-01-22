package data

import "github.com/injoyai/tdx/protocol"

type Info struct {
	Code       string         `json:"code"`       //代码
	Name       string         `json:"name"`       //名称
	Price      protocol.Price `json:"price"`      //最新价
	Turnover   float64        `json:"turnover"`   //换手率
	FloatStock int64          `json:"floatStock"` //流通股本
	TotalStock int64          `json:"totalStock"` //总股本
	FloatValue protocol.Price `json:"floatValue"` //流通市值
	TotalValue protocol.Price `json:"totalValue"` //总市值
}
