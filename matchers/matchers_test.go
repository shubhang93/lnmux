package matchers

import (
	"bufio"
	"context"
	"github.com/shubhang93/lnmux"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestMatchers(t *testing.T) {
	t.Run("connection read times out", func(t *testing.T) {
		ln, err := net.Listen("tcp", ":8080")
		if err != nil {
			t.Errorf("error creating listener:%v", err)
			return
		}

		done := make(chan error)
		go dialWithData(500*time.Millisecond, "", done)

		if conn, err := ln.Accept(); err != nil {
			t.Errorf("error accepting conn:%v", err)
			return
		} else {
			_ = conn.SetDeadline(time.Now().Add(1 * time.Millisecond))
			buffConn := bufio.NewReader(conn)
			sniffer := MatchHTTPOne(128)
			match, err := sniffer(buffConn)

			if err != nil && !os.IsTimeout(err) {
				t.Errorf("got non timeout error:%v", err)
			}

			if match {
				t.Errorf("expected to timeout")
			}
		}

		if err := <-done; err != nil {
			t.Errorf("dail error:%v", err)
		}
		_ = ln.Close()
	})

	t.Run("matches HTTP/1.1 request", func(t *testing.T) {
		ln, err := net.Listen("tcp", ":8080")
		if err != nil {
			t.Errorf("error creating listener:%v", err)
			return
		}

		done := make(chan error)
		data := `GET /echo HTTP/1.1
Host: reqbin.com`
		go dialWithData(500*time.Millisecond, data, done)

		if conn, err := ln.Accept(); err != nil {
			t.Errorf("error accepting conn:%v", err)
			return
		} else {
			_ = conn.SetDeadline(time.Now().Add(100 * time.Millisecond))
			buffConn := bufio.NewReader(conn)
			sniffer := MatchHTTPFast(128)
			match, err := sniffer(buffConn)
			if err != nil && !os.IsTimeout(err) {
				t.Errorf("got non timeout match error:%v", err)
			}
			if !match {
				t.Errorf("expected to match")
			}
		}

		if err := <-done; err != nil {
			t.Errorf("dail error:%v", err)
		}
		_ = ln.Close()
	})

	t.Run("matches HTTP/2 request", func(t *testing.T) {
		ln, err := net.Listen("tcp", ":8080")
		if err != nil {
			t.Errorf("error creating listener:%v", err)
			return
		}

		done := make(chan error)
		data := `GET / HTTP/2
Host: localhost:1010
User-Agent: curl/7.65.0
Accept: */*`
		go dialWithData(500*time.Millisecond, data, done)

		if conn, err := ln.Accept(); err != nil {
			t.Errorf("error accepting conn:%v", err)
			return
		} else {
			_ = conn.SetDeadline(time.Now().Add(100 * time.Millisecond))
			buffConn := bufio.NewReader(conn)
			sniffer := MatchHTTPTwoCURL(128)
			match, err := sniffer(buffConn)
			if err != nil && !os.IsTimeout(err) {
				t.Errorf("got non timeout match error:%v", err)
			}
			if !match {
				t.Errorf("expected to match")
			}
		}

		if err := <-done; err != nil {
			t.Errorf("dail error:%v", err)
		}
		_ = ln.Close()
	})

	t.Run("matches Content-Type: application/json header", func(t *testing.T) {
		ln, err := net.Listen("tcp", ":8080")
		if err != nil {
			t.Errorf("error creating listener:%v", err)
			return
		}

		done := make(chan error)
		data := `GET /echo/get/json HTTP/1.1
Host: reqbin.com
Accept: application/json`
		go dialWithData(500*time.Millisecond, data, done)

		if conn, err := ln.Accept(); err != nil {
			t.Errorf("error accepting conn:%v", err)
			return
		} else {
			_ = conn.SetDeadline(time.Now().Add(100 * time.Millisecond))
			buffConn := bufio.NewReader(conn)
			sniffer := MatchHeader(256, "Accept: application/json")
			match, err := sniffer(buffConn)
			if err != nil && !os.IsTimeout(err) {
				t.Errorf("got match error:%v", err)
			}
			if !match {
				t.Errorf("expected to match")
			}
		}

		if err := <-done; err != nil {
			t.Errorf("dail error:%v", err)
		}
		_ = ln.Close()
	})

	t.Run("matches GRPC connections", func(t *testing.T) {
		ln, err := net.Listen("tcp", ":8080")
		if err != nil {
			t.Errorf("error creating listener:%v", err)
			return
		}

		data := `PRI * HTTP/2.0

`
		done := make(chan error)
		go dialWithData(500*time.Millisecond, data, done)

		conn, err := ln.Accept()
		if err != nil {
			t.Errorf("accept error:%v", err)
		}
		_ = conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		buffConn := bufio.NewReader(conn)
		sniffer := MatchHTTP2Preface
		match, err := sniffer(buffConn)
		if err != nil && !os.IsTimeout(err) {
			t.Errorf("match error:%v", err)
		}
		if !match {
			t.Errorf("expected to match")
		}
		if err := <-done; err != nil {
			t.Errorf("dial error:%v", err)
		}
		_ = ln.Close()

	})

	t.Run("match succeeds with a non EOF error", func(t *testing.T) {
		ln, err := net.Listen("tcp", ":8080")
		if err != nil {
			t.Errorf("listener setup error:%v", err)
			return
		}

		errorNotifCalled := atomic.Bool{}
		ctx, cancel := context.WithTimeout(context.Background(), 2000*time.Millisecond)
		defer cancel()

		muxLis := lnmux.Listener{
			Root:            ln,
			ConnReadTimeout: 100 * time.Millisecond,
			MatchErrHandler: func(err error, matched bool, matcher string) bool {
				errorNotifCalled.CompareAndSwap(false, true)
				if matcher != "http_one" {
					t.Errorf("expected matcher to be %s got %s", "http_one", matcher)
				}
				if !matched {
					t.Errorf("expected match to be %v", true)
				}

				if !os.IsTimeout(err) {
					t.Errorf("expected a timeout error got %v", err)
				}
				return true
			},
		}

		httpOneLis := muxLis.ListenFor("http_one", MatchHTTPFast(1024))

		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			time.Sleep(1000 * time.Millisecond)
			_ = httpOneLis.Close()
		}()

		go func() {
			defer wg.Done()
			_ = muxLis.Serve(ctx)
		}()

		done := make(chan error)
		go dialWithData(500*time.Millisecond, `GET /echo/get/json HTTP/1.1
Host: reqbin.com
Accept: application/json`, done)

		if err := <-done; err != nil {
			t.Errorf("dial error:%v", err)
		}

		if !errorNotifCalled.Load() {
			t.Errorf("error notifier not called")
		}

		wg.Wait()
	})

}

func dialWithData(delay time.Duration, data string, done chan error) {
	time.Sleep(delay)
	defer close(done)
	conn, err := net.Dial("tcp", ":8080")
	if err != nil {
		done <- err
		return
	}
	if data != "" {
		_, _ = conn.Write([]byte(data))
	}
	time.Sleep(delay)
	_ = conn.Close()
}
