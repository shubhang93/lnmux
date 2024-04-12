package io

// ConnPeeker
//
// # Allows you peek into the connection
//
// Peek into n bytes of the connection without consuming the socket stream
type ConnPeeker interface {
	Peek(n int) ([]byte, error)
}
