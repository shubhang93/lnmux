package lnmux

import (
	"errors"
	"fmt"
)

var ErrRootListenerClosed = errors.New("root listener closed")
var ErrVlisClosed = errors.New("virtual listener closed")
var ErrSendWorkersBlocked = errors.New("send workers blocked")
var ErrVirtualListenersClosed = errors.New("all virtual listeners closed")

type noMatchErr struct {
	err   error
	outer string
}

func (n noMatchErr) Error() string {
	if n.err == nil {
		return "conn did not match any of the matchers"
	}
	return fmt.Sprintf("conn did not match any of the matchers:%s", n.err.Error())
}

func (n noMatchErr) Unwrap() error {
	return n.err
}

func isTimeoutError(err error) bool {
	var te interface{ Timeout() bool }
	return errors.As(err, &te) && te.Timeout()
}

func isFatal(err error) bool {
	fe, ok := err.(fatalError)
	return ok && fe.Fatal()
}

type fatalError interface {
	Fatal() bool
}
