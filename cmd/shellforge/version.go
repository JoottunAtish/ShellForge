package main

import (
	"fmt"
	"io"
	goruntime "runtime"

	"github.com/spf13/cobra"
)

// VersionInfo is the build metadata the linker fills in through the ldflags
// in Makefile and make.ps1.
type VersionInfo struct {
	Version   string
	Commit    string
	BuildDate string
}

// newVersionCommand returns `shellforge version`.
func newVersionCommand(v VersionInfo) *cobra.Command {
	return &cobra.Command{
		Use:     "version",
		GroupID: groupMeta,
		Short:   "Print version and build information",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			printVersion(cmd.OutOrStdout(), v)
			return nil
		},
	}
}

func printVersion(w io.Writer, v VersionInfo) {
	fmt.Fprintf(w, "shellforge %s\n", v.Version)
	fmt.Fprintf(w, "  commit:   %s\n", v.Commit)
	fmt.Fprintf(w, "  built:    %s\n", v.BuildDate)
	fmt.Fprintf(w, "  go:       %s\n", goruntime.Version())
	fmt.Fprintf(w, "  platform: %s/%s\n", goruntime.GOOS, goruntime.GOARCH)
}
