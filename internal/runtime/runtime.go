package runtime

import "context"

// Runtime is a backend that can host the sandbox, such as Docker or WSL.
//
// Every method that can block takes a context and must honour cancellation.
// Ctrl-C during provisioning has to leave no orphan container and no half
// imported distribution.
//
// Implementations live in subpackages and never escape L1. Code above L1
// sees this interface and Caps, never a backend type.
type Runtime interface {
	// Provision prepares the sandbox described by spec. It is idempotent:
	// calling it on an already provisioned sandbox succeeds and changes
	// nothing.
	Provision(ctx context.Context, spec ImageSpec) error

	// Destroy removes the sandbox this Runtime owns. It is idempotent,
	// mirroring Provision: calling it when nothing has been provisioned
	// succeeds and returns nil rather than an error, so a caller does not
	// need to check Status first just to avoid an error on a clean
	// teardown.
	//
	// An implementation compares the target against a compile-time
	// constant before acting. See ImageSpec.Name.
	Destroy(ctx context.Context) error

	// Status reports whether the sandbox is provisioned and running.
	Status(ctx context.Context) (Status, error)

	// StartSession opens a session on the provisioned sandbox. The caller
	// closes the returned Session.
	//
	// It returns an error wrapping ErrSandboxMissing when nothing has been
	// provisioned yet.
	StartSession(ctx context.Context, spec SessionSpec) (Session, error)

	// Capabilities reports what this backend supports, so the content
	// layer can filter levels that declare a requirement the backend
	// cannot meet. It does not block and takes no context.
	Capabilities() Caps
}
