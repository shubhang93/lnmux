package connmatch

import lnmuxio "github.com/shubhang93/lnmux/io"

// Func is the function signatures for connection matchers
type Func func(connPkr lnmuxio.ConnPeeker) (matched bool, matchErr error)
