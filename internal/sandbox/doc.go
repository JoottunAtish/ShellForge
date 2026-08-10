// Package sandbox owns the image specification and provisioning policy: which
// image to build, which rootfs artifact to import, and how to verify it.
//
// Layer L1. May import internal/runtime and below.
//
// Downloaded artifacts are sha256-verified BEFORE use, never after. On a
// mismatch, refuse the artifact, delete the download, and say so.
package sandbox
