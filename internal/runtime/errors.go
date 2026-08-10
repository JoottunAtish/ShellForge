package runtime

import "errors"

// Sentinel errors returned by every Runtime implementation.
//
// Compare them with errors.Is rather than by string. An implementation wraps
// one of these with %w so that the backend detail travels alongside the
// classification, and the CLI turns the classification into a ux.Fail
// carrying a remediation and a doc anchor.
var (
	// ErrSandboxMissing reports that no sandbox has been provisioned.
	ErrSandboxMissing = errors.New("sandbox is not provisioned")

	// ErrSandboxUnhealthy reports that the sandbox exists but does not
	// respond.
	ErrSandboxUnhealthy = errors.New("sandbox exists but does not respond")

	// ErrNotSupported reports that this backend cannot perform the
	// operation. Read Capabilities first rather than probing for this
	// error.
	ErrNotSupported = errors.New("operation not supported by this runtime")
)
