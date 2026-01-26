package data

import (
	"path/filepath"
	"time"

	"github.com/injoyai/goutil/database/sqlite"
	"github.com/injoyai/goutil/database/xorms"
	"github.com/injoyai/logs"
	"github.com/injoyai/tdx"
	"github.com/injoyai/tdx/extend"
	"github.com/robfig/cron/v3"
)

/*



 */

// Start 更新数据
func (this *Data) Start() {
	p := extend.NewPullKline(extend.PullKlineConfig{
		Tables:     []string{extend.Day, extend.Minute},
		Goroutines: this.Goroutines,
		Dir:        filepath.Join(tdx.DefaultDatabaseDir, Kline),
	})

	cr := cron.New(cron.WithSeconds())
	cr.AddFunc("0 20 15 * * *", func() {
		logs.PrintErr(this.Update(p))
	})

	logs.PrintErr(this.Update(p))

	cr.Start()
}

func (this *Data) Update(p *extend.PullKline) error {
	if updated, _ := this.Updated.Updated(Kline); !updated {
		err := p.Update(this.Manage)
		if err != nil {
			return err
		}
		return this.Updated.Update(Kline)
	}
	return nil
}

//// updateDayKline 更新日线数据
//func (this *Data) updateDayKlineAll() error {
//	updated, err := this.Updated.Updated(DayKline)
//	if err != nil {
//		return err
//	}
//	if updated {
//		return nil
//	}
//	codes := this.Codes.GetStockCodes()
//	b := bar.NewCoroutine(len(codes), this.Goroutines)
//	defer b.Close()
//	for i := range codes {
//		code := codes[i]
//		b.Go(func() {
//			err := this.updateDayKline(code)
//			if err != nil {
//				b.Log("[ERR]", err)
//				b.Flush()
//			}
//		})
//	}
//	b.Wait()
//	return this.Updated.Update(DayKline)
//}

//func (this *Data) updateDayKline(code string) error {
//	code = protocol.AddPrefix(code)
//	filename := this.dayKlineFilename(code)
//
//	db, err := sqlite.NewXorm(filename)
//	if err != nil {
//		return err
//	}
//	defer db.Close()
//	err = db.Sync2(new(protocol.Kline))
//	if err != nil {
//		return err
//	}
//
//	//读取最后的数据
//	last := new(protocol.Kline)
//	_, err = db.Get(last)
//	if err != nil {
//		return err
//	}
//
//	//拉取数据
//	var resp *protocol.KlineResp
//	err = g.Retry(func() error {
//		return this.Do(func(c *tdx.Client) error {
//			resp, err = c.GetKlineDayUntil(code, func(k *protocol.Kline) bool {
//				return k.Time.Unix() <= last.Time.Unix()
//			})
//			return err
//		})
//	}, this.Retry)
//	if err != nil {
//		return err
//	}
//
//	return db.SessionFunc(func(session *xorm.Session) error {
//
//		//方便日内更新
//		_, err = session.Where("Time>=?", last.Time).Delete(new(protocol.Kline))
//		if err != nil {
//			return err
//		}
//
//		for _, v := range resp.List {
//			if v.Time.Unix() < last.Time.Unix() {
//				continue
//			}
//			_, err = session.Insert(v)
//			if err != nil {
//				return err
//			}
//		}
//
//		return nil
//	})
//
//}

/*



 */

func NewUpdated(filename string) (*Updated, error) {
	db, err := sqlite.NewXorm(filename)
	if err != nil {
		return nil, err
	}
	err = db.Sync2(new(tdx.UpdateModel))
	return &Updated{db: db, hour: 15}, err
}

type Updated struct {
	db     *xorms.Engine
	hour   int
	minute int
}

func (this *Updated) Update(key string) error {
	_, err := this.db.Where("`Key`=?", key).Update(&tdx.UpdateModel{Time: time.Now().Unix()})
	return err
}

func (this *Updated) Updated(key string) (bool, error) {
	update := new(tdx.UpdateModel)
	{ //查询或者插入一条数据
		has, err := this.db.Where("`Key`=?", key).Get(update)
		if err != nil {
			return true, err
		} else if !has {
			update.Key = key
			if _, err = this.db.Insert(update); err != nil {
				return true, err
			}
			return false, nil
		}
	}
	{ //判断是否更新过,更新过则不更新
		now := time.Now()
		node := time.Date(now.Year(), now.Month(), now.Day(), this.hour, this.minute, 0, 0, time.Local)
		updateTime := time.Unix(update.Time, 0)
		if now.Sub(node) > 0 {
			//当前时间在9点之后,且更新时间在9点之前,需要更新
			if updateTime.Sub(node) < 0 {
				return false, nil
			}
		} else {
			//当前时间在9点之前,且更新时间在上个节点之前
			if updateTime.Sub(node.Add(-time.Hour*24)) < 0 {
				return false, nil
			}
		}
	}
	return true, nil
}
