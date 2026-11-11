package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"turtle-soup/internal/telemetry"
)

// 测试使用真实 TCP 连接，验证缓慢请求体不会无限占用服务端连接。
func TestSlowRequestBodyTimesOut(t *testing.T) {
	readDone := make(chan error, 1)
	address := serveWithReadTimeout(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.ReadAll(r.Body)
		readDone <- err
	}), 100*time.Millisecond)

	conn, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	// 声明两个字节却只发一个字节，触发请求体读取截止时间。
	request := "POST / HTTP/1.1\r\nHost: localhost\r\nContent-Length: 2\r\n\r\na"
	if _, err := io.WriteString(conn, request); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-readDone:
		var networkErr net.Error
		if !errors.As(err, &networkErr) || !networkErr.Timeout() {
			t.Fatalf("缓慢请求体应因读取超时中断，实际错误：%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("请求体读取未在预期时间内结束")
	}
}

// HTTP 读取期限只约束升级握手，不应截断已经建立的房间实时连接。
func TestWebSocketSurvivesRequestReadTimeout(t *testing.T) {
	monitor, err := telemetry.NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer monitor.Close()
	router := newRouter(monitor)
	serverDone := make(chan error, 1)
	router.GET("/live-test", func(c *gin.Context) {
		conn, err := websocket.Accept(c.Writer, c.Request, nil)
		if err != nil {
			serverDone <- err
			return
		}
		defer conn.CloseNow()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		kind, data, err := conn.Read(ctx)
		if err == nil {
			err = conn.Write(ctx, kind, data)
		}
		serverDone <- err
	})
	address := serveWithReadTimeout(t, router, 100*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws://"+address+"/live-test", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	// 等待超过 HTTP 读取期限，再验证已升级连接仍能双向传输。
	time.Sleep(250 * time.Millisecond)
	if err := conn.Write(ctx, websocket.MessageText, []byte("房间仍在线")); err != nil {
		t.Fatal(err)
	}
	_, data, err := conn.Read(ctx)
	if err != nil || string(data) != "房间仍在线" {
		t.Fatalf("升级后的连接过早断开：数据=%q 错误=%v", data, err)
	}
	if err := <-serverDone; err != nil {
		t.Fatal(err)
	}
}

func serveWithReadTimeout(t *testing.T, handler http.Handler, timeout time.Duration) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := newHTTPServer(handler)
	server.ReadTimeout = timeout
	done := make(chan error, 1)
	go func() { done <- server.Serve(listener) }()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			t.Error(err)
		}
		if err := <-done; !errors.Is(err, http.ErrServerClosed) {
			t.Errorf("测试服务异常退出：%v", err)
		}
	})
	return listener.Addr().String()
}
