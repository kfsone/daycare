// Package daycare is a utility for allowing out-of-order registration and lookup of
// parent<->child relations.
package daycare

import (
	"errors"
)

const (
	stateClosed int32 = iota
	stateOpening
	stateOpen
)

// ErrRunning is returned if you attempt to access the shared data while
// with GetData() while the manageris still running; call Stop() first.
var ErrRunning = errors.New("registry has not been close()d")

// ErrRegistrationClosed is returned by Register() if the underlying request channel
// gets closed while waiting for a response from the manager.
var ErrRegistrationClosed = errors.New("registration closed unexpectedly")

// ErrLookupClosed is returned by Lookup() if the underlying request channel
// gets closed while waiting for a response from the manager.
var ErrLookupClosed = errors.New("lookup closed unexpectedly")
