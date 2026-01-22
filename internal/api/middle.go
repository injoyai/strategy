package api

import (
	"errors"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"strings"

	"github.com/injoyai/conv"
	"github.com/injoyai/frame/fbr"
)

// WithFS 加载文件
func WithFS(e fs.FS, sub ...string) fbr.Handler {

	if len(sub) == 0 {
		entries, err := fs.ReadDir(e, ".")
		if err != nil {
			panic(err)
		}
		// 只有一个顶层目录且是目录，自动去掉前缀
		if len(entries) == 1 && entries[0].IsDir() {
			sub = []string{entries[0].Name()}
		}
	}

	var err error
	subDir := path.Join(sub...)
	if len(subDir) > 0 {
		e, err = fs.Sub(e, subDir)
		if err != nil {
			panic(err)
		}
	}

	return func(c fbr.Ctx) {
		filename, _ := strings.CutPrefix(c.Path(), c.Route().Path)
		filename = conv.Select(filename == "/" || filename == "", "index.html", filename)
		f, err := e.Open(filename)
		if os.IsNotExist(err) || errors.Is(err, os.ErrNotExist) {
			c.Next()
			return
		}
		c.CheckErr(err)
		defer f.Close()
		h := http.Header{}
		ext := strings.ToLower(path.Ext(filename))
		h.Set("Content-Type", mime.TypeByExtension(ext))
		c.Custom200(f, h)
	}
}
