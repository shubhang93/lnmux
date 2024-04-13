package connmatch

// Func is the function signatures for connection matchers
type Func func(connPkr Peeker) (matched bool, matchErr error)
