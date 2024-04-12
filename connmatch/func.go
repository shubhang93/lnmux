package connmatch

import lnmuxio "github.com/shubhang93/lnmux/io"

type Func func(connPkr lnmuxio.ConnPeeker) (bool, error)
