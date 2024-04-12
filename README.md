# connmatch
--
    import "."


## Usage

#### type Func

```go
type Func func(connPkr lnmuxio.ConnPeeker) (matched bool, matchErr error)
```

Func is the function signatures for connection matchers
