package data

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/injoyai/base/chans"
	"github.com/injoyai/goutil/database/sqlite"
	"github.com/injoyai/goutil/oss"
	"github.com/injoyai/logs"
	"github.com/injoyai/tdx"
	"github.com/injoyai/tdx/extend"
	"github.com/injoyai/tdx/protocol"
)

const (
	Kline    = "kline"
	DayKline = "day-kline"
	MinKline = "min-kline"
	BaseInfo = "base-info"
)

type (
	Handler = func(info Info, day, min extend.Klines)
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

func (this *Data) KlineDir() string {
	return filepath.Join(this.DatabaseDir, Kline)
}

func (this *Data) GetStockCodes() []string {
	return this.Codes.GetStockCodes()
}

func (this *Data) GetDayKlines(code string, start, end time.Time) (extend.Klines, error) {
	filename := filepath.Join(this.KlineDir(), code+".db")
	if !oss.Exists(filename) {
		return nil, fmt.Errorf("股票[%s]数据不存在", code)
	}
	db, err := sqlite.NewXorm(filename)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	data := extend.Klines{}
	err = db.Table("DayKline").Where("Unix>? and Unix<?", start.Unix(), end.Unix()).
		Asc("Unix").Find(&data)
	logs.PrintErr(err)
	return data, err
}

func (this *Data) GetMinKlines(code string, start, end time.Time) (extend.Klines, error) {
	filename := filepath.Join(this.KlineDir(), code+".db")
	if !oss.Exists(filename) {
		return nil, fmt.Errorf("股票[%s]数据不存在", code)
	}
	db, err := sqlite.NewXorm(filename)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	data := extend.Klines{}
	err = db.Table("MinuteKline").Where("Unix>? and Unix<?", start.Unix(), end.Unix()).
		Asc("Unix").Find(&data)
	logs.PrintErr(err)
	return data, err
}

func (this *Data) RangeKlines(limit int, start, end time.Time, f Handler) error {

	es, err := os.ReadDir(this.KlineDir())
	if err != nil {
		return err
	}

	wg := chans.NewWaitLimit(limit)

	for _, e := range es {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".db") {
			continue
		}
		code := strings.TrimSuffix(e.Name(), ".db")
		wg.Add()
		go func() {
			defer wg.Done()
			dayKlines, err := this.GetDayKlines(code, start, end)
			if err != nil {
				logs.Err(err)
				return
			}
			if len(dayKlines) == 0 {
				return
			}
			last := dayKlines[len(dayKlines)-1]
			info := Info{
				Code:       code,
				Name:       this.Codes.GetName(code),
				Price:      last.Close,
				Turnover:   last.Turnover,
				FloatStock: last.FloatStock,
				TotalStock: last.TotalStock,
				FloatValue: protocol.Price(last.FloatStock) * last.Close,
				TotalValue: protocol.Price(last.TotalStock) * last.Close,
			}

			var minKlines extend.Klines
			//minKlines, err := this.GetMinKlines(code, start, end)
			//if err != nil {
			//	logs.Err(err)
			//	return
			//}

			f(info, dayKlines, minKlines)
		}()

	}

	wg.Wait()

	return nil
}
