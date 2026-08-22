package reconciler

import "errors"

// ErrHostKeyMismatch means the host's presented key does not match the pinned
// fingerprint: a possible man-in-the-middle. Every operation to that host
// aborts until an operator re-pins it.
var ErrHostKeyMismatch = errors.New("host key fingerprint does not match the pinned value")

// Target describes one worker host's fully-materialized execution
// environment: a per-host DOCKER_CONFIG holding a docker context and a
// managed HOME whose .ssh pins the host key. Nothing global (~/.docker,
// ~/.ssh) is ever touched, so concurrent deploys to different hosts cannot
// race. A nil *Target on Docker means the control plane itself.
//
// Built by internal/hostmgr; consumed here opaquely as extra env vars.
type Target struct {
	HostID   string
	Endpoint string // ssh://user@host:port
	Env      []string
}
