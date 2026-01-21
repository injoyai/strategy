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
)

const (
	Kline    = "kline"
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
		Cols("Time,Open,High,Low,Close,Volume,Amount").
		Asc("Unix").Find(&data)
	logs.PrintErr(err)
	return data, err
}

func (this *Data) RangeDayKlines(limit int, start, end time.Time, f Handler) error {

	es, err := os.ReadDir(this.KlineDir())
	if err != nil {
		return err
	}

	wg := chans.NewWaitLimit(limit)

	for _, info := range es {
		if info.IsDir() || !strings.HasSuffix(info.Name(), ".db") {
			continue
		}
		code := strings.TrimSuffix(info.Name(), ".db")
		wg.Add()
		go func() {
			defer wg.Done()
			ks, err := this.GetDayKlines(code, start, end)
			if err != nil {
				logs.Err(err)
				return
			}
			f(code, this.Codes.GetName(code), ks)
		}()

	}

	wg.Wait()

	return nil
}
