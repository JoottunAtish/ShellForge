package runtime

// Caps reports what a backend supports.
//
// The content layer filters out any level whose requires list names
// something the current backend cannot provide, so a learner sees a locked
// level with a reason rather than a mysterious failure. This is also the
// reason no code above L1 ever needs a backend type.
type Caps struct {
	// Networking reports that a session can be given a network.
	Networking bool

	// Systemd reports that the sandbox runs systemd as PID 1.
	Systemd bool

	// MultiUser reports that the sandbox has more than one usable account,
	// which the permissions levels need.
	MultiUser bool

	// Snapshotting reports that the backend can checkpoint and restore. No
	// v0.1 caller uses it: reset wipes the scratch directory instead.
	Snapshotting bool

	// Privileged reports that the sandbox runs with elevated privileges.
	// Shellforge never asks for that. The field exists so a backend can
	// report it and the game can refuse to run.
	Privileged bool
}

// Status is a snapshot of what a backend currently holds.
type Status struct {
	// Provisioned reports that the sandbox exists.
	Provisioned bool

	// Running reports that the sandbox is up and answering.
	Running bool

	// Backend names the implementation, "docker" or "wsl". It is for
	// display only. No decision anywhere may branch on it; that is what
	// Caps is for.
	Backend string

	// Detail is a short note for the status output, such as the container
	// id or the distribution state.
	Detail string
}
