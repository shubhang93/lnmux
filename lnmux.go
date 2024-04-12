package lnmux

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"github.com/shubhang93/lnmux/connmatch"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/shubhang93/lnmux/io"
)

type semToken struct{}

const maxSendWorkers = 1 << 12

type workerConfig struct {
	maxAttempts int
	maxWorkers  int
	sleepFor    time.Duration
	calcJitter  func(attempt int) int
}

// Listener
// is used to create and configure a multiplexed listener
//
// - Root - Set the root listener created using [net.Listen]. panics on nil
//
// - ConnReadTimeout - The read timeout for a connection, this is honored by the matcher functions
//
// - MatchErrHandler - A function that receives the match error, matched flag and the matcher name, return a true to close the connection and stop the match process
//
// - WorkerLimitBreachNotifier The max number of workers to send connections to virtual listeners is 4096, it is already a very high number.
//
// If you have reached this limit, we retry for 6000 times and exit the serve method. This function notifies the retry attempt, return a true to stop mux-ing
//
// ConnClosureNotifier Whenever fatal errors occur during matching process the connection is closed, this function notifies a connection closure
type Listener struct {
	Root                      net.Listener
	ConnReadTimeout           time.Duration
	MatchErrHandler           func(err error, matched bool, matcher string) (closeConn bool)
	WorkerLimitBreachNotifier func(retryAttempt int) (exitListener bool)
	ConnClosureNotifier       func(cause error)

	serving atomic.Bool
	wg      sync.WaitGroup

	// mutex is required if someone
	// calls ListenFor after
	// serve is invoked from another thread
	mu           sync.Mutex
	connMatchers []connMatcher

	workerConf *workerConfig
	sem        chan semToken
}

type connMatcher struct {
	VirtLis       *VirtualListener
	Name          string
	ConnMatchFunc connmatch.Func
}

// Serve starts serving the connections to each of the virtual listeners
//
// registered using the ListenFor method
//
// Serve is context aware and can be cancelled any time
func (ln *Listener) Serve(ctx context.Context) error {

	ln.mustInit()
	ln.serving.Store(true)

	ln.mu.Lock()
	matchers := ln.connMatchers
	ln.mu.Unlock()

	ln.wg.Add(1)
	allListenersClosed := make(chan struct{})
	go func() {
		// wait sequentially because we only care about
		//all virtual listeners closing
		// not individual
		defer ln.wg.Done()
		for _, m := range matchers {
			<-m.VirtLis.closeNotifier
		}
		close(allListenersClosed)
	}()

	ln.wg.Add(1)
	go func() {
		defer ln.wg.Done()
		select {
		case <-ctx.Done():
		case <-allListenersClosed:
		}
		_ = ln.Root.Close()
	}()

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("root listener context done:%w", ctx.Err())
		default:
			conn, err := ln.Root.Accept()
			if isTimeoutError(err) {
				continue
			}

			if err != nil {
				select {
				case <-allListenersClosed:
					return fmt.Errorf("root listener accept error:%w", ErrVirtualListenersClosed)
				default:
					return fmt.Errorf("root listener accept error:%w", err)
				}
			}

			ln.mu.Lock()
			connMatchers := ln.connMatchers
			ln.mu.Unlock()

			select {
			case <-ctx.Done():
				_ = conn.Close()
				return fmt.Errorf("conn accepted but context is done:%w", ctx.Err())
			default:

			}

			buffConn := io.BufferedConn{Conn: conn, BuffRdr: bufio.NewReader(conn)}
			deadline := time.Now().Add(ln.ConnReadTimeout)

			if ln.ConnReadTimeout > 0 {
				_ = buffConn.Conn.SetReadDeadline(deadline)
			}

			jitterFunc := ln.workerConf.calcJitter
			sleepFor := ln.workerConf.sleepFor
			maxAttempts := ln.workerConf.maxAttempts
			// this will let us go upto a minute
			// in steps of 10ms for 6000 times
			attempts := 1
			spin := true
			for spin {
				select {
				// try to acquire sem
				case <-allListenersClosed:
					return ErrVirtualListenersClosed
				case <-ctx.Done():
					return ctx.Err()
				case ln.sem <- semToken{}:
					spin = false
				default:
					// sleep for 10ms*jitter and retry
					if attempts >= maxAttempts {
						_ = buffConn.Close()
						ln.ConnClosureNotifier(ErrSendWorkersBlocked)
						return ErrSendWorkersBlocked
					}
					if shouldExit := ln.WorkerLimitBreachNotifier(attempts); shouldExit {
						_ = buffConn.Close()
						ln.ConnClosureNotifier(ErrSendWorkersBlocked)
						return ErrSendWorkersBlocked
					}
					jitter := jitterFunc(attempts)
					time.Sleep(time.Duration(jitter) * sleepFor)
					attempts++
				}
			}

			ln.wg.Add(1)
			go func() {
				defer func() {
					<-ln.sem
					ln.wg.Done()
				}()
				ln.handleConnection(ctx, buffConn, deadline, connMatchers)
			}()

		}
	}
}

func (ln *Listener) handleConnection(ctx context.Context, buffConn io.BufferedConn, readDeadline time.Time, connMatchers []connMatcher) {
	var connAlreadyClosed bool
	var match bool
	var matchErr error
	for _, connMtchr := range connMatchers {
		match = false
		matchErr = nil

		match, matchErr = connMtchr.ConnMatchFunc(buffConn)

		if matchErr != nil && ln.MatchErrHandler(matchErr, match, connMtchr.Name) {
			ln.ConnClosureNotifier(matchErr)
			_ = buffConn.Close()
			connAlreadyClosed = true
			break
		}

		if match {
			// reset the deadline to infinite
			_ = buffConn.SetReadDeadline(time.Time{})
			ln.sendConn(ctx, connMtchr.VirtLis, buffConn, connMtchr.Name)
			break
		}

		// reset the deadline to the
		// timeout provided
		// to perform the next match
		// this ensures that a fair deadline
		// is provided for every matcher
		if ln.ConnReadTimeout > 0 {
			ln.increaseConnDeadline(&buffConn, readDeadline)
		}

	}

	if !match && !connAlreadyClosed {
		ln.ConnClosureNotifier(noMatchErr{err: matchErr})
		_ = buffConn.Close()
	}
	connAlreadyClosed = false
}

func (ln *Listener) mustInit() {

	if ln.workerConf == nil {
		ln.workerConf = &workerConfig{
			maxWorkers:  maxSendWorkers,
			maxAttempts: 6000,
			sleepFor:    10 * time.Millisecond,
			calcJitter: func(attempt int) int {
				return attempt
			},
		}
	}

	if ln.Root == nil {
		panic("root listener is nil")
	}

	if ln.MatchErrHandler == nil {
		ln.MatchErrHandler = defaultMatchErrHandler
	}

	if ln.ConnClosureNotifier == nil {
		ln.ConnClosureNotifier = func(err error) {}
	}

	if ln.sem == nil {
		ln.sem = make(chan semToken, ln.workerConf.maxWorkers)
	}

	ln.mu.Lock()
	if len(ln.connMatchers) < 1 {
		ln.mu.Unlock()
		panic("no matchers registered")
	}
	ln.mu.Unlock()
}

func (ln *Listener) increaseConnDeadline(bc *io.BufferedConn, deadline time.Time) {
	remainingTime := deadline.Sub(time.Now())
	if remainingTime <= 0 {
		// deadline has exceeded fully
		// increase by `ln.ConnReadTimeout`
		newDeadline := deadline.Add(ln.ConnReadTimeout)
		_ = bc.SetReadDeadline(newDeadline)
	} else {
		// there is still some
		// time remaining
		delta := ln.ConnReadTimeout - remainingTime
		newDeadline := deadline.Add(delta)
		_ = bc.SetReadDeadline(newDeadline)
	}

}

// ListenFor registers a connection matcher function which can be used to
//
// register a new connection matcher
//
// it returns a VirtualListener back to the caller
func (ln *Listener) ListenFor(name string, cmf connmatch.Func) net.Listener {

	if ln.serving.Load() {
		panic("invalid state: listener is already serving")
	}

	if cmf == nil {
		panic("matcher func cannot be nil")
	}

	ln.mu.Lock()
	defer ln.mu.Unlock()

	virtLis := &VirtualListener{
		rootListener:    ln.Root,
		in:              make(chan io.BufferedConn),
		closureNotifier: ln.ConnClosureNotifier,
		closeNotifier:   make(chan struct{}),
		matcher:         name,
	}
	ln.connMatchers = append(ln.connMatchers, connMatcher{
		VirtLis:       virtLis,
		Name:          name,
		ConnMatchFunc: cmf,
	})
	return virtLis
}

func (ln *Listener) sendConn(ctx context.Context, rcvr *VirtualListener, bc io.BufferedConn, matcher string) {
	ctxDone := ctx.Done()
	select {
	case <-rcvr.closeNotifier:
		_ = bc.Close()
		ln.ConnClosureNotifier(fmt.Errorf("listener for %s closed", matcher))
	case <-ctxDone:
		_ = bc.Close()
		ln.ConnClosureNotifier(ErrRootListenerClosed)
	case rcvr.in <- bc:
	}
}

func (ln *Listener) cleanup() {
	ln.wg.Wait()
	fmt.Println("cleanup done")
	for _, cmatcher := range ln.connMatchers {
		lis := cmatcher.VirtLis
		close(lis.in)
	}

	for _, cmatcher := range ln.connMatchers {
		lis := cmatcher.VirtLis
		<-lis.closeNotifier
	}

}

// VirtualListener is a listener which
//
// listens to connections from the root listener
//
// virtual listener only receives connections
//
// which pass a certain match condition as dictated by the matcher functions registered
//
// it implements the [net.Listener]
type VirtualListener struct {
	rootListener    net.Listener
	in              chan io.BufferedConn
	closureNotifier func(err error)
	once            sync.Once
	closeNotifier   chan struct{}
	matcher         string
}

// Accept accepts matched connections
func (vlis *VirtualListener) Accept() (net.Conn, error) {
	select {
	case newConn, ok := <-vlis.in:
		if !ok {
			return nil, ErrRootListenerClosed
		}
		// check if listener was closed by the server
		// just to be sure
		select {
		case <-vlis.closeNotifier:
			_ = newConn.Close()
			vlis.closureNotifier(fmt.Errorf("vlis closed for %s:%w", vlis.matcher, ErrVlisClosed))
			return nil, ErrVlisClosed
		default:
		}
		return newConn, nil
	case <-vlis.closeNotifier:
		return nil, ErrVlisClosed
	}

}

// Close closes the virtual listener and any downstream
//
// using this listener will stop receiving connections
//
// the method is idempotent
func (vlis *VirtualListener) Close() error {

	/*select {
	case <-vlis.closeNotifier:
			// closed
	default:
		close(vlis.closeNotifier)
	}*/

	/*This is also a correct way to check it,
	but it isn't as clean as using the sync.Once
	*/

	vlis.once.Do(func() {
		close(vlis.closeNotifier)
	})
	return nil
}

// Addr returns the network address
func (vlis *VirtualListener) Addr() net.Addr {
	return vlis.rootListener.Addr()
}

// IsAbnormalTermination whether the listener terminated
// abnormally
func IsAbnormalTermination(err error) bool {
	switch {
	case errors.Is(err, ErrRootListenerClosed),
		errors.Is(err, ErrVlisClosed),
		errors.Is(err, ErrVirtualListenersClosed),
		errors.Is(err, http.ErrServerClosed),
		errors.Is(err, context.DeadlineExceeded),
		errors.Is(err, context.Canceled),
		errors.Is(err, net.ErrClosed):
		return false
	default:
		return true
	}
}
