package main

import (
	"fmt"

	"github.com/injoyai/conv/cfg"
	"github.com/injoyai/frame"
	"github.com/injoyai/goutil/oss/shell"
	"github.com/injoyai/goutil/oss/tray"
	"github.com/injoyai/strategy/internal/api"
	"github.com/injoyai/strategy/internal/common"
)

func main() {

	port := cfg.GetInt("port", frame.DefaultPort)

	tray.Run(
		tray.WithHint("Strategy"),
		func(s *tray.Tray) {
			err := common.Init()
			if err != nil {
				s.SetHint(err.Error())
				return
			}
			common.Data.Start()
			go func() {
				err = api.Run(port)
				if err != nil {
					s.SetHint(err.Error())
					return
				}
			}()
		},
		tray.WithShow(func(m *tray.Menu) {
			shell.OpenBrowser(fmt.Sprintf("http://localhost:%d", port))
		}),
		tray.WithSeparator(),
		tray.WithExit(),
	)

}
