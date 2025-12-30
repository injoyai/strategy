package lib

import (
	_ "github.com/injoyai/bar"
	_ "github.com/injoyai/base/chans"
	_ "github.com/injoyai/conv"
	_ "github.com/injoyai/frame/fbr"
	_ "github.com/injoyai/ios"
	_ "github.com/injoyai/logs"
	"github.com/traefik/yaegi/interp"
)

var Symbols = interp.Exports{}

///go:generate go install github.com/traefik/yaegi/cmd/yaegi@latest

//go:generate yaegi extract github.com/injoyai/ios
//go:generate yaegi extract github.com/injoyai/ios/client
//go:generate yaegi extract github.com/injoyai/ios/client/dial
//go:generate yaegi extract github.com/injoyai/ios/client/frame
//go:generate yaegi extract github.com/injoyai/ios/client/frame/v2
//go:generate yaegi extract github.com/injoyai/ios/client/redial
//go:generate yaegi extract github.com/injoyai/ios/module/common
//go:generate yaegi extract github.com/injoyai/ios/module/memory
///go:generate yaegi extract github.com/injoyai/ios/module/mqtt
//go:generate yaegi extract github.com/injoyai/ios/module/rabbitmq
//go:generate yaegi extract github.com/injoyai/ios/module/serial
//go:generate yaegi extract github.com/injoyai/ios/module/sse
//go:generate yaegi extract github.com/injoyai/ios/module/ssh
//go:generate yaegi extract github.com/injoyai/ios/module/tcp
//go:generate yaegi extract github.com/injoyai/ios/module/unix
//go:generate yaegi extract github.com/injoyai/ios/module/websocket
//go:generate yaegi extract github.com/injoyai/ios/server
//go:generate yaegi extract github.com/injoyai/ios/server/listen
//go:generate yaegi extract github.com/injoyai/ios/split

//go:generate yaegi extract github.com/injoyai/conv
//go:generate yaegi extract github.com/injoyai/conv/cfg
//go:generate yaegi extract github.com/injoyai/conv/codec
//go:generate yaegi extract github.com/injoyai/conv/codec/ini
//go:generate yaegi extract github.com/injoyai/conv/codec/json
//go:generate yaegi extract github.com/injoyai/conv/codec/toml
//go:generate yaegi extract github.com/injoyai/conv/codec/xml
//go:generate yaegi extract github.com/injoyai/conv/codec/yaml

//go:generate yaegi extract github.com/injoyai/base/chans
//go:generate yaegi extract github.com/injoyai/base/coding
//go:generate yaegi extract github.com/injoyai/base/coding/json
//go:generate yaegi extract github.com/injoyai/base/crypt
//go:generate yaegi extract github.com/injoyai/base/crypt/aes
//go:generate yaegi extract github.com/injoyai/base/crypt/crc
//go:generate yaegi extract github.com/injoyai/base/crypt/des
//go:generate yaegi extract github.com/injoyai/base/crypt/gzip
//go:generate yaegi extract github.com/injoyai/base/crypt/md5
//go:generate yaegi extract github.com/injoyai/base/crypt/sha
//go:generate yaegi extract github.com/injoyai/base/crypt/tls
//go:generate yaegi extract github.com/injoyai/base/maps
//go:generate yaegi extract github.com/injoyai/base/maps/timeout
//go:generate yaegi extract github.com/injoyai/base/maps/wait
//go:generate yaegi extract github.com/injoyai/base/safe
//go:generate yaegi extract github.com/injoyai/base/str
//go:generate yaegi extract github.com/injoyai/base/types

//go:generate yaegi extract github.com/injoyai/frame
//go:generate yaegi extract github.com/injoyai/frame/fbr
//go:generate yaegi extract github.com/injoyai/frame/gins
//go:generate yaegi extract github.com/injoyai/frame/middle/easy_user
//go:generate yaegi extract github.com/injoyai/frame/middle/in
//go:generate yaegi extract github.com/injoyai/frame/middle/swagger

//go:generate yaegi extract github.com/injoyai/logs
//go:generate yaegi extract github.com/injoyai/bar
