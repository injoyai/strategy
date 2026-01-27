package api

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/injoyai/frame/fbr"
	"github.com/injoyai/logs"
)

// LSPHandler 处理 LSP WebSocket 连接
func LSPHandler(c fbr.Ctx) {
	c.Websocket(func(conn *fbr.Websocket) {
		//logs.Info("LSP WebSocket connected")
		//defer logs.Info("LSP WebSocket disconnected")

		firstMsg, err := conn.ReadMessage()
		if err != nil {
			logs.Err("ReadMessage error:", err)
			return
		}

		type initializeMessage struct {
			Method string `json:"method"`
			Params struct {
				RootURI  string `json:"rootUri"`
				RootPath string `json:"rootPath"`
			} `json:"params"`
		}

		workDir := ""
		var initMsg initializeMessage
		if err := json.Unmarshal(firstMsg, &initMsg); err == nil && initMsg.Method == "initialize" {
			rootURI := strings.TrimSpace(initMsg.Params.RootURI)
			if rootURI != "" {
				if u, err := url.Parse(rootURI); err == nil && u.Scheme == "file" {
					if u.Host != "" {
						pathPart := filepath.FromSlash(u.Path)
						workDir = `\\` + u.Host + pathPart
					} else {
						pathPart := u.Path
						if len(pathPart) >= 3 && pathPart[0] == '/' && pathPart[2] == ':' {
							pathPart = pathPart[1:]
						}
						workDir = filepath.FromSlash(pathPart)
					}
				}
			}
			if workDir == "" {
				workDir = strings.TrimSpace(initMsg.Params.RootPath)
			}
		}

		createdTemp := false
		if workDir == "" {
			tmpDir, err := os.MkdirTemp("", "strategy-lsp")
			if err != nil {
				logs.Err(err)
				return
			}
			workDir = tmpDir
			createdTemp = true
		} else {
			if err := os.MkdirAll(workDir, 0755); err != nil {
				logs.Err(err)
				return
			}
		}
		if createdTemp {
			defer os.RemoveAll(workDir)
		}

		goModPath := filepath.Join(workDir, "go.mod")
		if _, err := os.Stat(goModPath); os.IsNotExist(err) {
			if err := os.WriteFile(goModPath, []byte(`module strategy

go 1.25.0

require (
	github.com/injoyai/bar v0.0.11
	github.com/injoyai/base v1.2.20
	github.com/injoyai/conv v1.2.5
	github.com/injoyai/frame v0.0.16
	github.com/injoyai/goutil v1.2.27
	github.com/injoyai/ios v1.2.5
	github.com/injoyai/logs v1.0.12
	github.com/injoyai/tdx v0.0.75
)

`), 0644); err != nil {
				logs.Err(err)
				return
			}
		} else if err != nil {
			logs.Err(err)
			return
		}

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// 启动 gopls
		cmd := exec.CommandContext(ctx, "gopls", "serve")
		cmd.Dir = workDir
		cmd.Env = os.Environ()

		inPipe, err := cmd.StdinPipe()
		if err != nil {
			logs.Err(err)
			return
		}
		outPipe, err := cmd.StdoutPipe()
		if err != nil {
			logs.Err(err)
			return
		}
		// 可以在终端看到 gopls 的错误日志
		cmd.Stderr = os.Stderr

		if err := cmd.Start(); err != nil {
			logs.Err(err)
			return
		}
		defer cmd.Process.Kill()

		// 2. 启动转发 goroutine

		// WS -> gopls (Stdin)
		go func(first []byte) {
			defer inPipe.Close()
			if err := writeToGopls(inPipe, first); err != nil {
				logs.Err("Write to gopls error:", err)
				return
			}
			for {
				// 假设 conn.ReadMessage 返回 (messageType, []byte, error)
				// 如果 fbr.Websocket 是 *websocket.Conn (fasthttp)
				msg, err := conn.ReadMessage()
				if err != nil {
					//logs.Err("ReadMessage error:", err)
					return
				}
				// monaco-languageclient 通过 WebSocket 发送的是 JSON-RPC 消息体
				// gopls (stdio) 期望的是带 Content-Length header 的消息
				if err := writeToGopls(inPipe, msg); err != nil {
					logs.Err("Write to gopls error:", err)
					return
				}
			}
		}(firstMsg)

		// gopls (Stdout) -> WS
		// gopls 输出带 header 的消息，我们需要解析并提取 body 发送给 WS
		reader := bufio.NewReader(outPipe)
		for {
			// 读取 Header
			var contentLength int
			for {
				line, err := reader.ReadString('\n')
				if err != nil {
					if err != io.EOF {
						logs.Err("Read from gopls error:", err)
					}
					return
				}
				// logs.Debug("gopls header:", line) // 过于详细，暂时注释
				line = strings.TrimSpace(line)
				if line == "" {
					break // Header 结束
				}
				if strings.HasPrefix(line, "Content-Length: ") {
					fmt.Sscanf(line, "Content-Length: %d", &contentLength)
				}
			}

			if contentLength == 0 {
				continue
			}

			// 读取 Body
			body := make([]byte, contentLength)
			if _, err := io.ReadFull(reader, body); err != nil {
				return
			}

			// 发送给 WS
			// 1 = TextMessage
			if err := conn.WriteMessage(1, body); err != nil {
				return
			}
		}
	})
}

func writeToGopls(w io.Writer, msg []byte) error {
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(msg))
	_, err := w.Write(append([]byte(header), msg...))
	return err
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
