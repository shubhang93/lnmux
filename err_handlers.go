package lnmux

import (
	"errors"
	"io"
	"net"
)

func defaultMatchErrHandler(err error, matched bool, _ string) (closeConn bool) {
	if matched && isFatalError(err) {
		return true
	}

	if matched {
		return false
	}

	return isFatalError(err)
}

func isFatalError(err error) bool {
	switch {
	case errors.Is(err, net.ErrClosed),
		errors.Is(err, io.EOF):
		return true
	case isFatal(err):
		return true
	case isTimeoutError(err):
		return false
	default:
		return false
	}
}
