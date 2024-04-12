package io

import (
	"bufio"
	"net"
)

type BufferedConn struct {
	net.Conn
	BuffRdr *bufio.Reader
}

func (b BufferedConn) Peek(n int) ([]byte, error) {
	return b.BuffRdr.Peek(n)
}

func (b BufferedConn) Read(p []byte) (int, error) {
	return b.BuffRdr.Read(p)
}
