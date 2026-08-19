// Package wsl implements internal/runtime.Runtime by shelling out to
// wsl.exe. It is the Windows backend: the one a learner without Docker
// Desktop uses, checked against the same runtimetest contract suite the
// docker sibling runs.
//
// Layer L1. This package may import only the standard library,
// github.com/creack/pty (already approved, already imported by the docker
// sibling), internal/platform (L0), internal/platform/ux (L0), and
// internal/runtime (L1, the interface package). It must never import
// internal/pty, internal/content, internal/verify, internal/game, or
// internal/runtime/docker: internal/archtest enforces the layer map, and
// crossing to a sibling backend would defeat the point of the interface at
// L1, which is that dropping WSL support is a one-day decision rather than
// a rewrite.
//
// Every wsl.exe invocation is exec.CommandContext with an argv vector.
// There is no sh -c on the host and no fmt.Sprintf building a command
// line. See the security skill for why that distinction matters.
//
// WARNING: running `go test ./internal/runtime/wsl/...` on a real Windows
// machine with WSL2 registers and unregisters a real distribution named
// "shellforge-contracttest" (internal/runtime/runtimetest.SandboxName).
// That is the only distribution this package's tests ever touch. It is
// never the production "shellforge-sandbox" distribution, and it is never
// a distribution named by an environment variable or by anything a test
// can otherwise influence. On any other platform, including this project's
// own Linux CI legs and every host without wsl.exe on PATH, the contract
// test skips before provisioning anything.
package wsl
