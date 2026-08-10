// Package runtimetest holds the reusable contract test suite that every
// Runtime implementation must pass.
//
// Layer L1. It may import internal/runtime, testing, and the standard
// library, and nothing else. It must never import a backend: the moment
// this package knows Docker exists, the abstraction has failed.
//
// Only a _test.go file may import this package. It imports testing, so
// linking it into the product binary would be a bug.
//
// The suite assumes exactly three things about the sandbox image, and
// nothing more:
//
//	/bin/sh exists and understands POSIX redirection
//	/bin/bash exists and supports the /dev/tcp redirection form
//	a user named learner exists, with a home directory at /home/learner
//
// Anything beyond those three is backend knowledge and does not belong
// here.
package runtimetest
