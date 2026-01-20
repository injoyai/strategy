package data

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/injoyai/goutil/database/sqlite"
	"github.com/injoyai/goutil/oss"
	"github.com/injoyai/tdx"
	"github.com/injoyai/tdx/extend"
)

const (
	DayKline = "day-kline"
	MinKline = "min-kline"
	BaseInfo = "base-info"
)

type (
	Handler = func(code, name string, ks extend.Klines)
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

func (this *Data) dayKlineDir() string {
	return filepath.Join(this.DatabaseDir, DayKline)
}

//func (this *Data) minKlineFilename(code string, year int) string {
//	return filepath.Join(this.DatabaseDir, MinKline, code+"-"+conv.String(year)+".db")
//}

func (this *Data) GetStockCodes() []string {
	return this.Codes.GetStockCodes()
}

func (this *Data) GetDayKlines(code string, start, end time.Time) (extend.Klines, error) {
	filename := filepath.Join(this.dayKlineDir(), code+".db")
	if !oss.Exists(filename) {
		return nil, fmt.Errorf("股票[%s]数据不存在", code)
	}
	db, err := sqlite.NewXorm(filename)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	data := extend.Klines{}
	err = db.Where("Unix>? and Unix<?", start.Unix(), end.Unix()).
		Cols("Time,Open,High,Low,Close,Volume,Amount").
		Asc("Unix").Find(&data)
	return data, err
}

func (this *Data) RangeDayKlines(start, end time.Time, f Handler) error {
	dir := filepath.Join(this.DatabaseDir, DayKline)
	return oss.RangeFileInfo(dir, func(info *oss.FileInfo) (bool, error) {
		code := strings.TrimSuffix(info.Name(), ".db")
		ks, err := this.GetDayKlines(code, start, end)
		if err != nil {
			return false, err
		}
		f(code, this.Codes.GetName(code), ks)
		return true, nil
	})
}

//func (this *Data) GetMinKlines(code string, start, end time.Time) (protocol.Klines, error) {
//	filename := this.minKlineFilename(code, 2025)
//	if !oss.Exists(filename) {
//		return nil, fmt.Errorf("股票[%s]数据不存在", code)
//	}
//	return nil, nil
//}
