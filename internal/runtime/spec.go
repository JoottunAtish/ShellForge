package runtime

// ImageSpec describes the sandbox image or distribution to provision.
type ImageSpec struct {
	// Name is the container image name or the WSL distribution name.
	//
	// The caller must allowlist-validate it against ^[a-zA-Z0-9_-]+$ and
	// reject anything else rather than sanitizing it, because this value
	// reaches a docker or wsl.exe argv. A destroy path must not trust it:
	// compare against a compile-time constant before removing anything.
	// See the destructive-safety skill, item 1.
	Name string

	// Tag is the image tag. It is empty for a WSL distribution.
	Tag string

	// Reference is the pinned digest for a container image, or the rootfs
	// URL for a WSL distribution.
	Reference string

	// SHA256 is the expected hex digest of a downloaded artifact. It is
	// required whenever Reference names something fetched, and it is
	// checked before the artifact is used. Checking after an import is not
	// verification.
	SHA256 string
}

// SessionSpec describes one sandbox session.
type SessionSpec struct {
	// User is the sandbox user to run as, normally "learner". The caller
	// must allowlist-validate it against ^[a-zA-Z0-9_-]+$ before it reaches
	// an argv.
	User string

	// WorkDir is the absolute starting directory inside the sandbox.
	WorkDir string

	// Env is the environment for the session.
	Env map[string]string

	// StateDir is an absolute path inside the sandbox that becomes
	// SF_STATE, where the shell instrumentation writes the journal and the
	// environment snapshot.
	StateDir string

	// Networking requests a network for the session. False, the default,
	// means no network at all. A level opts in by declaring
	// requires: [networking].
	Networking bool
}
