package lnmux

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/fortytw2/leaktest"
	"github.com/shubhang93/lnmux/connmatch"
	"github.com/shubhang93/lnmux/matchers"
	"io"
	"net"
	"net/http"
	"os"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestListener_Serve(t *testing.T) {

	t.Run("panics on invalid ConnMatcherFunc", func(t *testing.T) {
		defer leaktest.CheckTimeout(t, time.Millisecond*5000)()
		ln, err := net.Listen("tcp", ":8080")
		if err != nil {
			t.Errorf("error creating a listener:%v", err)
			return
		}
		defer ln.Close()
		muxLn := &Listener{Root: ln, ConnReadTimeout: 500 * time.Millisecond}

		shouldPanic(t, func() {
			_ = muxLn.ListenFor("efg", nil)
		})

	})

	t.Run("listener should stop listening when context is cancelled", func(t *testing.T) {
		defer leaktest.CheckTimeout(t, time.Millisecond*5000)()
		ln, err := net.Listen("tcp", ":8080")
		if err != nil {
			t.Errorf("error creating a listener:%v", err)
			return
		}

		defer ln.Close()

		muxLn := &Listener{Root: ln, ConnReadTimeout: 500 * time.Millisecond}

		ctx, cancel := context.WithTimeout(context.Background(), 1000*time.Millisecond)
		defer cancel()

		abcLis := muxLn.ListenFor("abcd", matchers.MatchHTTPOne(10))

		go func() {
			time.Sleep(2000 * time.Millisecond)
			_ = abcLis.Close()
		}()

		t.Log("starting server")

		done := make(chan struct{})
		go func() {
			defer close(done)
			closeErr := muxLn.Serve(ctx)
			if IsAbnormalTermination(closeErr) {
				t.Errorf("listener error:%s", closeErr.Error())
			}
		}()
		time.Sleep(500 * time.Millisecond)
		if !dialed(":8080") {
			t.Errorf("dial failed")
			return
		}

		t.Logf("dial done")
		<-done

		if dialed(":8080") {
			t.Errorf("dial should have failed")
		}
	})

	t.Run("listener should match HC1 connections", func(t *testing.T) {
		defer leaktest.CheckTimeout(t, time.Millisecond*5000)()
		ln, err := net.Listen("tcp", ":8080")
		if err != nil {
			t.Errorf("error creating a listener:%v", err)
			return
		}
		defer ln.Close()

		muxLn := &Listener{Root: ln}
		ctx, cacnel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cacnel()

		HTTPOneLis := muxLn.ListenFor("HTTP_1_1", matchers.MatchHTTPFast(64))
		lisDone := make(chan struct{})

		serverDone := make(chan struct{})
		go func(list net.Listener) {
			_ = startHTTPServer(ctx, list, func(writer http.ResponseWriter, request *http.Request) {
				fmt.Fprintf(writer, "OK")
			})
			close(serverDone)
		}(HTTPOneLis)

		go func() {
			_ = muxLn.Serve(ctx)
			close(lisDone)
		}()

		cl := &http.Client{}
		body, err := getRequest(cl, "http://localhost:8080", nil)
		if err != nil {
			t.Errorf("error making get call:%v", err)
		}

		if body != "OK" {
			t.Errorf("got body %s expected %s\n", body, "OK")
		}

		<-serverDone
		<-lisDone

	})

	t.Run("listener should match next matcher if match fails", func(t *testing.T) {
		defer leaktest.CheckTimeout(t, time.Millisecond*5000)()
		ln, err := net.Listen("tcp", ":8080")
		if err != nil {
			t.Errorf("error creating a listener:%v", err)
			return
		}
		defer ln.Close()

		muxLn := &Listener{Root: ln, ConnReadTimeout: 100 * time.Millisecond}
		ctx, cacnel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cacnel()

		HTTPJSONLis := muxLn.ListenFor("content-type:json", matchers.MatchHeader(64, "Accept: application/json"))
		HTTPFastLis := muxLn.ListenFor("http/1.1", matchers.MatchHTTPFast(64))
		HTTPOneLis := muxLn.ListenFor("http/1.0", matchers.MatchHTTPOne(64))

		var wg sync.WaitGroup
		wg.Add(3)
		go func(list net.Listener) {
			defer wg.Done()
			_ = startHTTPServer(ctx, list, func(writer http.ResponseWriter, request *http.Request) {
				fmt.Fprintf(writer, "HTTP_FAST_OK")
			})
		}(HTTPFastLis)

		go func(lis net.Listener) {
			defer wg.Done()
			_ = startHTTPServer(ctx, lis, func(writer http.ResponseWriter, request *http.Request) {
				fmt.Fprintf(writer, "HTTP_OK")
			})
		}(HTTPOneLis)

		go func(lis net.Listener) {
			defer wg.Done()
			_ = startHTTPServer(ctx, lis, func(writer http.ResponseWriter, request *http.Request) {
				fmt.Fprintf(writer, "JSON_OK")
			})
		}(HTTPJSONLis)

		lisDone := make(chan struct{})
		go func() {
			_ = muxLn.Serve(ctx)
			close(lisDone)
		}()

		defer func() {
			wg.Wait()
			<-lisDone
		}()

		data := "GET / HTTP/1.0\r\nHost: example.org\r\n\r\n"
		conn, err := net.Dial("tcp", "localhost:8080")
		buffcon := bufio.NewReader(conn)
		if err != nil {
			t.Errorf("error dialing:%v", err)
		}
		_, err = conn.Write([]byte(data))
		if err != nil {
			t.Errorf("conn write error:%v", err)
		}

		t.Log("waiting for resp")
		resp, err := http.ReadResponse(buffcon, nil)
		if err != nil {
			t.Errorf("error making resp:%v", err)
			return
		}
		if resp.StatusCode != 200 {
			t.Errorf("got resp code %d want %d", resp.StatusCode, 200)
		}

		bb, err := io.ReadAll(resp.Body)
		if err != nil {
			t.Errorf("error reading body:%v", err)
		}

		if string(bb) != "HTTP_OK" {
			t.Errorf("want body %s got %s", "HTTP_OK", string(bb))
		}

	})

	t.Run("match the first listener and break", func(t *testing.T) {
		defer leaktest.CheckTimeout(t, time.Millisecond*5000)()
		ln, err := net.Listen("tcp", ":8080")
		if err != nil {
			t.Errorf("listener create error:%v", err)
			return
		}
		defer ln.Close()

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		muxLn := Listener{
			Root:            ln,
			ConnReadTimeout: 200 * time.Millisecond,
		}
		AppJSONLis := muxLn.ListenFor("ApplicationJSON", headerMatcher)
		HTTPOneLis := muxLn.ListenFor("HTTPOne", matchers.MatchHTTPOne(64))

		jsonServerDone := make(chan struct{})
		go func(listener net.Listener) {
			t.Log("starting json server")
			_ = startHTTPServer(ctx, listener, func(writer http.ResponseWriter, request *http.Request) {
				fmt.Fprintf(writer, "JSON_OK")
			})
			close(jsonServerDone)
		}(AppJSONLis)

		textServerDone := make(chan struct{})
		go func(listener net.Listener) {
			t.Log("starting text server")
			_ = startHTTPServer(ctx, listener, func(writer http.ResponseWriter, request *http.Request) {
				fmt.Fprintf(writer, "TEXT_OK")
			})
			close(textServerDone)
		}(HTTPOneLis)

		listenerDone := make(chan struct{})
		go func(listener *Listener) {
			err := listener.Serve(ctx)
			t.Logf("listner error:%v\n", err)
			close(listenerDone)
		}(&muxLn)

		getCallCount := 10000

		sem := make(chan struct{}, 100)

		time.Sleep(500 * time.Millisecond)
		cl := &http.Client{
			Transport: &http.Transport{
				MaxIdleConns: 100,
			},
		}

		var successCount atomic.Int32
		var wg sync.WaitGroup
		for i := range getCallCount {
			wg.Add(1)
			sem <- struct{}{}
			go func(c int) {
				defer func() {
					<-sem
					wg.Done()
				}()
				// introduce a small delay to prevent the
				// server from closing connections
				//https://stackoverflow.com/questions/37774624/go-http-get-concurrency-and-connection-reset-by-peer
				time.Sleep(5 * time.Millisecond)
				body, err := getRequest(cl, "http://localhost:8080", map[string]string{"Content-Type": "application/json"})
				if err != nil {
					t.Errorf("error making %d http call:%v\n", c, err)
					return
				}
				if body != "JSON_OK" {
					t.Errorf("body expected %s got %s\n", "JSON_OK", body)
				} else {
					successCount.Add(1)
				}
			}(i)
		}

		wg.Wait()
		t.Log("done all requests")
		cancel()

		<-textServerDone
		t.Log("text server done")
		<-jsonServerDone
		t.Log("json server done")
		<-listenerDone
		t.Log("listener done")

		sc := successCount.Load()
		if sc != int32(getCallCount) {
			t.Errorf("succees count %d, get call count %d\n", sc, getCallCount)
		}

	})

	t.Run("root listener should wait for closure for virtual listeners", func(t *testing.T) {
		defer leaktest.CheckTimeout(t, time.Millisecond*5000)()
		ln, err := net.Listen("tcp", ":8080")
		if err != nil {
			t.Error("error creating listener", err.Error())
			return
		}
		defer ln.Close()

		lnmux := Listener{Root: ln}

		vl1 := lnmux.ListenFor("http-1", func(pkr connmatch.Peeker) (bool, error) {
			return true, nil
		})
		vl2 := lnmux.ListenFor("http-header-matcher", func(pkr connmatch.Peeker) (bool, error) {
			return true, nil
		})
		vl3 := lnmux.ListenFor("GRPC", func(pkr connmatch.Peeker) (bool, error) {
			return true, nil
		})

		cancelContextAfter := 1000 * time.Millisecond
		closeVirtualListenersAfter := 1500 * time.Millisecond
		ctx, cancel := context.WithTimeout(context.Background(), cancelContextAfter)
		defer cancel()

		closedAllVirtListeners := make(chan struct{})
		go func() {
			defer close(closedAllVirtListeners)
			time.Sleep(closeVirtualListenersAfter)
			listeners := []net.Listener{vl1, vl2, vl3}
			for _, l := range listeners {
				_ = l.Close()
			}
		}()

		rootClosed := make(chan struct{})
		go func() {
			defer func() {
				defer close(rootClosed)
			}()
			_ = lnmux.Serve(ctx)
		}()

		select {
		case <-ctx.Done():
			select {
			case <-rootClosed:
				t.Errorf("root closed without waiting for virtual listeners")
			default:
				select {
				case <-closedAllVirtListeners:
					<-rootClosed
					t.Logf("root closed after virtual listeners")
				}

			}

		}

	})

	t.Run("virtual listener can be closed concurrently more than once without panicking", func(t *testing.T) {
		defer leaktest.CheckTimeout(t, time.Millisecond*5000)()
		shouldNotPanic(t, func() {
			ln, err := net.Listen("tcp", ":8080")
			if err != nil {
				t.Error("error creating listener", err.Error())
				return
			}
			defer ln.Close()

			lnmux := Listener{Root: ln}

			virtLis := lnmux.ListenFor("HTTPOne", func(pkr connmatch.Peeker) (bool, error) {
				return true, nil
			})

			var wg sync.WaitGroup
			for i := 0; i < 1000; i++ {
				wg.Add(1)
				go func(i int) {
					defer wg.Done()
					_ = virtLis.Close()
				}(i)
			}

			wg.Wait()

			_ = ln.Close()
		})
	})

	t.Run("listener should close when http server does not terminate gracefully", func(t *testing.T) {
		defer leaktest.CheckTimeout(t, time.Millisecond*5000)()
		ln, err := net.Listen("tcp", ":8080")
		if err != nil {
			t.Error("error creating listener", err.Error())
			return
		}
		defer ln.Close()

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		muxLis := Listener{
			Root:            ln,
			ConnReadTimeout: 100 * time.Millisecond,
		}
		httpOneLis := muxLis.ListenFor("http_1", matchers.MatchHTTPFast(64))

		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			err := startHTTPServerWOShutdown(ctx, httpOneLis, func(writer http.ResponseWriter, request *http.Request) {
				_, _ = fmt.Fprintf(writer, "OK")
			})
			if IsAbnormalTermination(err) {
				t.Errorf("server abnormally terminated:%v\n", err)
			}
		}()

		wg.Add(1)
		go func() {
			defer wg.Done()
			err := muxLis.Serve(ctx)
			if IsAbnormalTermination(err) {
				t.Errorf("listener abnormally terminated:%v\n", err)
			}
		}()

		time.Sleep(500 * time.Millisecond)
		var getCallWG sync.WaitGroup

		maxGoroutines := 2
		sem := make(chan struct{}, maxGoroutines)

		callCount := atomic.Int32{}
		for i := 0; i < 100; i++ {
			getCallWG.Add(1)
			sem <- struct{}{}
			go func(i int) {
				defer getCallWG.Done()
				resp, err := http.Get("http://localhost:8080")
				if err != nil {
					t.Errorf("error making a get call:%v", err)
				} else {
					consumeResp(resp)
					callCount.Add(1)
				}
				<-sem
			}(i)
		}

		getCallWG.Wait()
		cancel()
		if callCount.Load() != 100 {
			t.Errorf("call count not equal to 100, got:%d", callCount.Load())
		}

		wg.Wait()
	})

	t.Run("test closure notifier for matcher errors", func(t *testing.T) {
		defer leaktest.CheckTimeout(t, 5000*time.Millisecond)()
		ln, err := net.Listen("tcp", ":8080")
		if err != nil {
			t.Errorf("error creating lis:%v", err)
			return
		}
		defer ln.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 2000*time.Millisecond)
		defer cancel()

		connCount := 10
		var cntMU sync.Mutex
		errCounter := map[string]int{}
		expectedErrCounter := map[string]int{
			"EOF":     connCount,
			"Timeout": connCount,
		}

		muxLn := Listener{
			Root:            ln,
			ConnReadTimeout: 100 * time.Millisecond,
			ConnClosureNotifier: func(cause error) {
				cntMU.Lock()
				defer cntMU.Unlock()
				if errors.Is(cause, io.EOF) {
					errCounter["EOF"]++
					return
				}

				if errors.Is(cause, os.ErrDeadlineExceeded) {
					errCounter["Timeout"]++
				}
			},
		}
		// set sniffN to a high value
		// to simulate a timeout
		httpOneLis := muxLn.ListenFor("http-1", matchers.MatchHTTPOne(1024))
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = startHTTPServer(ctx, httpOneLis, func(writer http.ResponseWriter, request *http.Request) {
				_, _ = writer.Write([]byte("OK"))
			})
		}()

		wg.Add(1)
		go func() {
			defer wg.Done()
			err = muxLn.Serve(ctx)
			if IsAbnormalTermination(err) {
				t.Errorf("error running mux:%v", err)
			}
		}()

		func() {
			for i := 0; i < connCount; i++ {
				conn, err := net.Dial("tcp", ":8080")
				if err != nil {
					t.Errorf("[%d]: dail error:%v", i, err)
					return
				}
				_ = conn.Close()
			}

			for i := 0; i < connCount; i++ {
				conn, err := net.Dial("tcp", ":8080")
				if err != nil {
					t.Errorf("[%d]: dail error:%v", i, err)
					return
				}
				_, _ = conn.Write([]byte("HEAD"))
				// sleep for more than listener.ConnReadTimeout
				time.Sleep(200 * time.Millisecond)
				_ = conn.Close()
			}
		}()

		wg.Wait()
		if !reflect.DeepEqual(expectedErrCounter, errCounter) {
			expectedJSON, _ := json.MarshalIndent(expectedErrCounter, "", " ")
			gotJSON, _ := json.MarshalIndent(errCounter, "", " ")
			t.Errorf("Want %s\nGot %s", string(expectedJSON), string(gotJSON))
		}
	})

	t.Run("worker limit is exceeded", func(t *testing.T) {
		defer leaktest.CheckTimeout(t, 10000*time.Millisecond)()
		ln, err := net.Listen("tcp", ":8080")
		if err != nil {
			t.Error("error creating lis:", err.Error())
			return
		}
		defer ln.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		muxLn := Listener{
			Root:            ln,
			ConnReadTimeout: 100 * time.Millisecond,
			workerConf: &workerConfig{
				maxWorkers:  3,
				maxAttempts: 10,
				sleepFor:    1 * time.Millisecond,
				calcJitter: func(attempt int) int {
					return attempt + 1
				},
			},
			WorkerLimitBreachNotifier: func(attempt int) (exitListener bool) {
				t.Logf("attempt:%d\n", attempt)
				return attempt == 2
			},
		}

		allLis := muxLn.ListenFor("allow-all-traffic", allowAllTraffic)
		// do not consume from listener to block senders for

		connsDone := make(chan struct{})

		var wg sync.WaitGroup

		wg.Add(3)
		go func() {
			defer wg.Done()
			<-connsDone
			_ = allLis.Close()
		}()

		go func() {
			defer wg.Done()
			<-connsDone
			cancel()
		}()

		go func() {
			defer wg.Done()
			err := muxLn.Serve(ctx)
			if !errors.Is(err, ErrSendWorkersBlocked) {
				t.Errorf("expected error:%v got %v", ErrSendWorkersBlocked, err)
			}
		}()

		connCount := 4
		var conns []net.Conn
		for range connCount {
			conn, err := net.Dial("tcp", ":8080")
			if err != nil {
				t.Errorf("error creating conn:%v", err)
				return
			}
			conns = append(conns, conn)
		}

		time.Sleep(5000 * time.Millisecond)
		close(connsDone)
		for _, c := range conns {
			_ = c.Close()
		}
		wg.Wait()

	})

	t.Run("worker limit is within bounds", func(t *testing.T) {
		defer leaktest.CheckTimeout(t, 10000*time.Millisecond)()
		ln, err := net.Listen("tcp", ":8080")
		if err != nil {
			t.Error("error creating lis:", err.Error())
			return
		}
		defer ln.Close()

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		muxLn := Listener{
			Root:            ln,
			ConnReadTimeout: 100 * time.Millisecond,
			workerConf: &workerConfig{
				maxWorkers:  25,
				maxAttempts: 10,
				sleepFor:    1 * time.Millisecond,
				calcJitter: func(attempt int) int {
					return attempt + 1
				},
			},
			WorkerLimitBreachNotifier: func(attempt int) (exitListener bool) {
				t.Logf("attempt:%d\n", attempt)
				return attempt == 2
			},
		}

		allLis := muxLn.ListenFor("allow-all-traffic", allowAllTraffic)
		// do not consume from listener to block senders for

		connsDone := make(chan struct{})

		var wg sync.WaitGroup

		wg.Add(3)
		go func() {
			defer wg.Done()
			<-connsDone
			_ = allLis.Close()
		}()

		go func() {
			defer wg.Done()
			<-connsDone
			cancel()
		}()

		go func() {
			defer wg.Done()
			err := muxLn.Serve(ctx)
			if errors.Is(err, ErrSendWorkersBlocked) {
				t.Error("got a worker limit error")
			}
		}()

		connCount := 5
		var conns []net.Conn
		for range connCount {
			conn, err := net.Dial("tcp", ":8080")
			if err != nil {
				t.Errorf("error creating conn:%v", err)
				return
			}
			conns = append(conns, conn)
		}

		time.Sleep(5000 * time.Millisecond)
		close(connsDone)
		for _, c := range conns {
			_ = c.Close()
		}
		wg.Wait()

	})

	t.Run("serve should exit when all listeners are closed", func(t *testing.T) {
		defer leaktest.CheckTimeout(t, 5000*time.Millisecond)()
		ln, err := net.Listen("tcp", ":8080")
		if err != nil {
			t.Error("error creating lis:", err.Error())
			return
		}
		defer ln.Close()

		muxLn := Listener{
			Root:            ln,
			ConnReadTimeout: 100 * time.Millisecond,
			workerConf: &workerConfig{
				maxWorkers:  25,
				maxAttempts: 10,
				sleepFor:    1 * time.Millisecond,
				calcJitter: func(attempt int) int {
					return attempt + 1
				},
			},
		}

		allLis := muxLn.ListenFor("allow-all-traffic", allowAllTraffic)
		allLis2 := muxLn.ListenFor("allow-all-traffic", allowAllTraffic)

		broadcastClose := make(chan struct{})
		tmr := time.AfterFunc(1000*time.Millisecond, func() {
			close(broadcastClose)
		})
		defer tmr.Stop()

		go func() {
			<-broadcastClose
			t.Log("closing lis1")
			_ = allLis.Close()
		}()

		go func() {
			<-broadcastClose
			t.Log("closing lis2")
			_ = allLis2.Close()
		}()

		ctx, cancel := context.WithTimeout(context.Background(), 5000*time.Millisecond)
		defer cancel()
		err = muxLn.Serve(ctx)
		if !errors.Is(err, ErrVirtualListenersClosed) {
			t.Errorf("expected error %v got %v", ErrVirtualListenersClosed, err)
		}
	})

}

func dialed(port string) bool {
	_, err := net.Dial("tcp", port)
	if err != nil {
		return false
	}
	return true
}

func getRequest(cl *http.Client, url string, headers map[string]string) (string, error) {
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}

	for k, v := range headers {
		req.Header.Set(k, v)
	}

	resp, err := cl.Do(req)
	if err != nil {
		return "", err
	}

	defer resp.Body.Close()

	bb, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(bb), nil
}

func allowAllTraffic(pkr connmatch.Peeker) (bool, error) {
	return true, nil
}

func headerMatcher(peeker connmatch.Peeker) (bool, error) {
	// an example implementation
	// not performant
	// only used for testing
	peeked, err := peeker.Peek(100)
	if err != nil && !os.IsTimeout(err) {
		return false, err
	}

	lines := bytes.Split(peeked, []byte{'\n'})
	for _, line := range lines {
		if strings.Contains(string(line), "application/json") {
			return true, nil
		}
	}
	return false, nil
}

func startHTTPServer(ctx context.Context, ln net.Listener, h http.HandlerFunc) error {
	server := &http.Server{Handler: h}

	done := make(chan struct{})
	go func() {
		<-ctx.Done()

		shutdownCtx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
		defer cancel()
		_ = server.Shutdown(shutdownCtx)
		close(done)
	}()

	err := server.Serve(ln)
	<-done

	return err
}

func startHTTPServerWOShutdown(ctx context.Context, ln net.Listener, h http.HandlerFunc) error {
	server := &http.Server{Handler: h}

	done := make(chan struct{})
	go func() {
		defer close(done)
		<-ctx.Done()
		_ = server.Close()
	}()
	err := server.Serve(ln)
	<-done
	return err
}

func shouldPanic(t *testing.T, f func()) {
	defer func() { recover() }()
	f()
	t.Errorf("should have panicked")
}

func shouldNotPanic(t *testing.T, f func()) {
	defer func() {
		if recover() != nil {
			t.Errorf("should not have panicked")
		}
	}()
	f()

}

func consumeResp(resp *http.Response) {
	if resp != nil {
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, resp.Body)
	}
}
