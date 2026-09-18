//go:build !unix

package main

// run has no implementation off unix. sf-ptyhost is built into the Linux
// sandbox image and never shipped to a host, but this package still has to
// compile for every target CI builds, which includes windows/amd64.
func run(string, []string) (int, error) {
	return 1, errUnsupported
}
