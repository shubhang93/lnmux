# lnmux

## Listener multiplexing for serving multiple protocols over the same port (HTTP, HTTP/2)

### Description

- This package is useful if you want to server HTTP, GRPC, HTTP/2 traffic on the same port
- The package lets you define connection matcher functions which can be used to sniff a few bytes out of a `net.Conn`
  and route it to the right server

  