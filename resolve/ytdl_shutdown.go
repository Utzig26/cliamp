package resolve

import "github.com/bjarneo/cliamp/internal/workgroup"

// pendingYTDL owns every yt-dlp process the resolver starts, including those
// started by UI commands rather than providers, through process exit and
// private cookie copy removal.
var pendingYTDL workgroup.Group

// ShutdownYTDL cancels in-flight resolver yt-dlp requests, refuses new ones,
// and waits until their processes have exited and their private cookie copies
// are removed. Call it before exit, without holding UI or player locks.
func ShutdownYTDL() { pendingYTDL.Close() }
