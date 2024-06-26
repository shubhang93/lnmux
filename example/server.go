//go:build ignore

package main

import (
	"context"
	"fmt"
	"github.com/shubhang93/lnmux"
	"github.com/shubhang93/lnmux/matchers"
	"golang.org/x/net/http2"
	"net"
	"net/http"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

func main() {
	ln, err := net.Listen("tcp", ":8080")
	if err != nil {
		panic(err)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer cancel()

	mux := lnmux.Listener{Root: ln, ConnReadTimeout: 100 * time.Millisecond}

	jsonListener := mux.ListenFor("content-type:json", matchers.AutoMatchHeader("Content-Type: application/json"))
	httpFastListener := mux.ListenFor("http-fast-listener", matchers.AutoMatchHTTPFast)
	HTTP2Listener := mux.ListenFor("grpc", matchers.MatchHTTP2Preface)
	// same matcher can be used for GRPC as well

	var wg sync.WaitGroup

	wg.Add(3)
	go func() {
		defer wg.Done()
		startServer(jsonListener, func(writer http.ResponseWriter, request *http.Request) {
			_, _ = fmt.Fprintf(writer, "OK")
		}, false)
	}()

	go func() {
		defer wg.Done()
		startServer(httpFastListener, func(writer http.ResponseWriter, request *http.Request) {
			_, _ = fmt.Fprintf(writer, "JSON_OK")
		}, false)
	}()

	go func() {
		startServer(HTTP2Listener, func(writer http.ResponseWriter, request *http.Request) {
			_, _ = fmt.Fprintf(writer, "HTTP2_OK")
		}, false)
	}()

	if err := mux.Serve(ctx); err != nil {
		fmt.Println(err)
	}

	wg.Wait()
	fmt.Println("terminated")
}

func startServer(ln net.Listener, h http.HandlerFunc, useHTTP2 bool) {

	// shutdown server after 10s

	ctx, cancel := context.WithTimeout(context.Background(), time.Second*10)
	defer cancel()
	srvr := &http.Server{
		Handler: h,
	}

	if useHTTP2 {
		_ = http2.ConfigureServer(srvr, &http2.Server{})
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		<-ctx.Done()
		srvr.Shutdown(ctx)
	}()

	_ = srvr.Serve(ln)
	<-done

}
