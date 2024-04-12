# lnmux
--
    import "."

Package lnmux provides a multiplexed listener which sends connections to several
virtual listeners which listen to a specific type of [net.Conn]

## Usage

```go
var ErrRootListenerClosed = errors.New("root listener closed")
```

```go
var ErrSendWorkersBlocked = errors.New("send workers blocked")
```

```go
var ErrVirtualListenersClosed = errors.New("all virtual listeners closed")
```

```go
var ErrVlisClosed = errors.New("virtual listener closed")
```

#### func  IsAbnormalTermination

```go
func IsAbnormalTermination(err error) bool
```
IsAbnormalTermination whether the listener terminated abnormally

#### type Listener

```go
type Listener struct {
	Root                      net.Listener
	ConnReadTimeout           time.Duration
	MatchErrHandler           func(err error, matched bool, matcher string) (closeConn bool)
	WorkerLimitBreachNotifier func(retryAttempt int) (exitListener bool)
	ConnClosureNotifier       func(cause error)
}
```

Listener is used to create and configure a multiplexed listener

- Root - Set the root listener created using [net.Listen]. panics on nil

- ConnReadTimeout - The read timeout for a connection, this is honored by the
matcher functions

- MatchErrHandler - A function that receives the match error, matched flag and
the matcher name, return a true to close the connection and stop the match
process

- WorkerLimitBreachNotifier The max number of workers to send connections to
virtual listeners is 4096, it is already a very high number.

If you have reached this limit, we retry for 6000 times and exit the serve
method. This function notifies the retry attempt, return a true to stop mux-ing

ConnClosureNotifier Whenever fatal errors occur during matching process the
connection is closed, this function notifies a connection closure

#### func (*Listener) ListenFor

```go
func (ln *Listener) ListenFor(name string, cmf connmatch.Func) net.Listener
```
ListenFor registers a connection matcher function which can be used to

register a new connection matcher

it returns a VirtualListener back to the caller

#### func (*Listener) Serve

```go
func (ln *Listener) Serve(ctx context.Context) error
```
Serve starts serving the connections to each of the virtual listeners

registered using the ListenFor method

Serve is context aware and can be cancelled any time

#### type VirtualListener

```go
type VirtualListener struct {
}
```

VirtualListener is a listener which

listens to connections from the root listener

virtual listener only receives connections

which pass a certain match condition as dictated by the matcher functions
registered

it implements the [net.Listener]

#### func (*VirtualListener) Accept

```go
func (vlis *VirtualListener) Accept() (net.Conn, error)
```
Accept accepts matched connections

#### func (*VirtualListener) Addr

```go
func (vlis *VirtualListener) Addr() net.Addr
```
Addr returns the network address

#### func (*VirtualListener) Close

```go
func (vlis *VirtualListener) Close() error
```
Close closes the virtual listener and any downstream using this listener will
stop receiving connections the method is idempotent
