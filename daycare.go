// Package daycare is a utility for allowing out-of-order registration and lookup of
// parent<->child relations.
package daycare

import (
	"errors"
)

// ErrRunning is returned if you attempt to access the shared data while
// with Values() while the manageris still running; call Stop() first.
var ErrRunning = errors.New("registry has not been close()d")

// ErrClosed is returned by Register() or Lookup() if the underlying request
// channel gets closed while waiting for a response from the manager.
var ErrClosed = errors.New("channel closed unexpectedly")
