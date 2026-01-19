package data

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/injoyai/conv"
	"github.com/injoyai/goutil/database/sqlite"
	"github.com/injoyai/goutil/oss"
	"github.com/injoyai/tdx"
	"github.com/injoyai/tdx/extend"
	"github.com/injoyai/tdx/protocol"
)

const (
	DayKline = "day-kline"
	MinKline = "min-kline"
	BaseInfo = "base-info"
)

func NewManage(m *tdx.Manage) (*Data, error) {
	updated, err := NewUpdated(filepath.Join(tdx.DefaultDatabaseDir, "updated.db"))
	if err != nil {
		return nil, err
	}
	return &Data{
		Retry:       tdx.DefaultRetry,
		Goroutines:  50,
		DatabaseDir: tdx.DefaultDatabaseDir,
		Manage:      m,
		Updated:     updated,
	}, nil
}

type Data struct {
	Retry       int
	Goroutines  int
	DatabaseDir string
	*tdx.Manage
	*Updated
}

func (this *Data) dayKlineFilename(code string) string {
	return filepath.Join(this.DatabaseDir, DayKline, code+".db")
}

func (this *Data) minKlineFilename(code string, year int) string {
	return filepath.Join(this.DatabaseDir, MinKline, code+"-"+conv.String(year)+".db")
}

func (this *Data) GetStockCodes() []string {
	return this.Codes.GetStockCodes()
}

func (this *Data) GetDayKlines(code string, start, end time.Time) (protocol.Klines, error) {
	filename := this.dayKlineFilename(code)
	if !oss.Exists(filename) {
		return nil, fmt.Errorf("股票[%s]数据不存在", code)
	}
	db, err := sqlite.NewXorm(filename)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	data := protocol.Klines{}
	err = db.Where("Time>? and Time<?", start, end).Cols("Time,Open,High,Low,Close,Volume,Amount").Asc("Time").Find(&data)
	return data, err
}

func (this *Data) RangeDayKlines(start, end time.Time, f func(code string, ks []*extend.Kline)) {
	dir := filepath.Join(this.DatabaseDir, DayKline)
	oss.RangeFileInfo(dir, func(info *oss.FileInfo) (bool, error) {
		db, err := sqlite.NewXorm(info.FullName())
		if err != nil {
			return false, err
		}
		defer db.Close()
		data := protocol.Klines{}
		err = db.Where("Unix>? and Unix<?", start.Unix(), end.Unix()).
			Cols("Time,Open,High,Low,Close,Volume,Amount").
			Asc("Time").Find(&data)
		return data, err

	})
}

func (this *Data) GetMinKlines(code string, start, end time.Time) (protocol.Klines, error) {
	filename := this.minKlineFilename(code, 2025)
	if !oss.Exists(filename) {
		return nil, fmt.Errorf("股票[%s]数据不存在", code)
	}
	return nil, nil
}
