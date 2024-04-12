package matchers

import (
	"bytes"
	"github.com/shubhang93/lnmux/connmatch"
	lnmuxio "github.com/shubhang93/lnmux/io"
	"net/http"
	"os"
	"strings"
)

const http2ReqLine = "HTTP/2"

// copied from
// "golang.org/x/net/http2"
// copied it to avoid adding a dependency for a single constant
// highly unlikely to change
const http2ConnPreface = "PRI * HTTP/2.0\n\nSM\n\n"

// Func takes a lnmuxio.ConnPeeker
// peeks into the connection without consuming bytes from the underlying connection
// return a true if match succeeded and an error to signal an error

func MatchHeader(sniffN int, header string) connmatch.Func {
	return func(pkr lnmuxio.ConnPeeker) (bool, error) {
		peeked, err := pkr.Peek(sniffN)

		var timeoutErr error
		if err != nil && !os.IsTimeout(err) {
			return false, err
		} else {
			timeoutErr = err
		}

		lines := bytes.Split(peeked, []byte{'\n'})
		if len(lines) > 2 {
			first := string(lines[0])
			lines = lines[1:]
			if parseHTTPFast(first) || parseHTTPZero(first) {
				for _, line := range lines {
					if strings.Contains(string(line), header) {
						return true, timeoutErr
					}
				}
			}
		}
		return false, timeoutErr
	}
}

// MatchHTTPOne matches HTTP/1.0 connections
func MatchHTTPOne(sniffN int) connmatch.Func {
	return func(rp lnmuxio.ConnPeeker) (bool, error) {
		return matchHTTPVersion(sniffN, 1, rp)
	}
}

// MatchHTTPFast matches HTTP/1.1 connections
func MatchHTTPFast(sniffN int) connmatch.Func {
	return func(connPkr lnmuxio.ConnPeeker) (bool, error) {
		return matchHTTPVersion(sniffN, 11, connPkr)
	}
}

// MatchHTTPTwoCURL matches HTTP/2.0 connections as sent with curl --prior-knowledge flag
func MatchHTTPTwoCURL(sniffN int) connmatch.Func {
	return func(pkr lnmuxio.ConnPeeker) (bool, error) {
		return matchHTTPVersion(sniffN, 2, pkr)
	}
}

// MatchHTTP2Preface can be used for connections which contain the
// HTTP/2 connection preface `PRI * HTTP/2.0\n\nSM\n\n`
// GRPC connections can also be matched with this
func MatchHTTP2Preface() connmatch.Func {
	return func(pkr lnmuxio.ConnPeeker) (bool, error) {
		bs, err := pkr.Peek(len(http2ConnPreface))

		var timeoutErr error
		if err != nil && !os.IsTimeout(err) {
			return false, err
		} else {
			timeoutErr = err
		}

		lines := bytes.Split(bs, []byte{'\n'})
		if len(lines) > 0 {
			first := lines[0]
			return strings.HasPrefix(http2ConnPreface, string(first)), timeoutErr
		}
		return false, timeoutErr
	}
}

func matchHTTPVersion(sniffN int, version int, pkr lnmuxio.ConnPeeker) (bool, error) {
	peeked, err := pkr.Peek(sniffN)
	var timeoutErr error
	if err != nil && !os.IsTimeout(err) {
		return false, err
	} else {
		timeoutErr = err
	}
	lines := bytes.Split(peeked, []byte{'\n'})
	if len(lines) > 0 {
		first := string(lines[0])
		switch version {
		case 11:
			return parseHTTPFast(first), timeoutErr
		case 2:
			return parseHTTPTwo(first), timeoutErr
		case 1:
			return parseHTTPZero(first), timeoutErr
		}
	}

	return false, timeoutErr

}

func parseHTTPTwo(text string) bool {
	startIndex := strings.Index(text, "HTTP/")
	if startIndex == -1 {
		return false
	}
	httpVersionFragment := strings.TrimSpace(text[startIndex:])
	return httpVersionFragment == http2ReqLine
}

func parseHTTPFast(text string) bool {
	maj, minr, ok := parseHTTP1(text)
	if ok {
		return maj == 1 && minr == 1
	}
	return false
}

func parseHTTPZero(text string) bool {
	maj, minr, ok := parseHTTP1(text)
	if ok {
		return maj == 1 && minr == 0
	}
	return false
}

func parseHTTP1(text string) (int, int, bool) {
	startIndex := strings.Index(text, "HTTP/")
	if startIndex == -1 {
		return 0, 0, false
	}
	httpVersionFragment := text[startIndex:]
	return http.ParseHTTPVersion(strings.TrimSpace(httpVersionFragment))
}