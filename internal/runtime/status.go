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

	// InteractiveShell reports that this backend can give the learner a
	// real interactive shell.
	//
	// It exists because the CLI used to answer this question with
	// `goruntime.GOOS != "windows"`, which is issue #77: the OS was
	// standing in for the backend, and it was standing in badly. The
	// answer was no on Windows for both backends, because both allocated
	// the pseudo terminal on the host with creack/pty, which has no
	// Windows implementation. It is yes on both now: the pseudo terminal
	// is allocated inside the sandbox by cmd/sf-ptyhost and the host only
	// moves bytes over pipes.
	//
	// A backend that cannot do it says so here rather than having the CLI
	// guess from the operating system.
	InteractiveShell bool

	// Privileged reports that the sandbox runs with elevated privileges.
	// Shellforge never asks for that. The field exists so a backend can
	// report it and the game can refuse to run.
	Privileged bool
}

// Status is a snapshot of what a backend currently holds.
//
// Status carries no sandbox identity on purpose. A destroy path must not ask
// "is this sandbox mine" by reading a name off Status; it derives its target
// from a compile-time constant instead. Adding a Name field is a real option,
// but it lands only with the destroy path and its refusal tests, never before.
// See the destructive-safety skill.
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
