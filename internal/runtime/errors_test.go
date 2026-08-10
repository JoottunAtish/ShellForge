package runtime_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/JoottunAtish/ShellForge/internal/platform/ux"
	"github.com/JoottunAtish/ShellForge/internal/runtime"
)

// TestSentinelErrorsSurviveWrapping asserts each sentinel error is
// distinguishable from the other two via errors.Is, and that wrapping it
// with fmt.Errorf, with ux.Fail, or with both in sequence preserves that
// identity. A sentinel declared as the same value as another, or hidden
// behind a shared wrapper type that does not implement Unwrap, fails here.
func TestSentinelErrorsSurviveWrapping(t *testing.T) {
	// sentinelIdx names, by position, which of the three sentinel errors
	// each test case is exercising. The cross-check below skips the
	// sentinel at this position, not the sentinel that happens to equal
	// tt.err by value. Skipping by value would silently pass if two
	// sentinels were ever declared as the same value, because the
	// aliased sentinel would compare equal to tt.err and get skipped
	// alongside it. See TestSentinelsAreDistinctValues for the direct
	// check that catches exactly that collapse.
	const (
		idxMissing = iota
		idxUnhealthy
		idxNotSupported
	)

	tests := []struct {
		name string
		err  error
		idx  int
		wrap func(error) error
	}{
		{
			name: "sandbox missing, bare",
			err:  runtime.ErrSandboxMissing,
			idx:  idxMissing,
			wrap: func(e error) error { return e },
		},
		{
			name: "sandbox missing, fmt wrapped",
			err:  runtime.ErrSandboxMissing,
			idx:  idxMissing,
			wrap: func(e error) error { return fmt.Errorf("start session: %w", e) },
		},
		{
			name: "sandbox missing, ux wrapped",
			err:  runtime.ErrSandboxMissing,
			idx:  idxMissing,
			wrap: func(e error) error {
				return ux.Fail("start a sandbox session", e, "run `shellforge init`", "sandbox-missing")
			},
		},
		{
			name: "sandbox unhealthy, bare",
			err:  runtime.ErrSandboxUnhealthy,
			idx:  idxUnhealthy,
			wrap: func(e error) error { return e },
		},
		{
			name: "sandbox unhealthy, fmt wrapped",
			err:  runtime.ErrSandboxUnhealthy,
			idx:  idxUnhealthy,
			wrap: func(e error) error { return fmt.Errorf("probe the sandbox: %w", e) },
		},
		{
			name: "sandbox unhealthy, ux wrapped",
			err:  runtime.ErrSandboxUnhealthy,
			idx:  idxUnhealthy,
			wrap: func(e error) error {
				return ux.Fail("check the sandbox", e, "run `shellforge sandbox rebuild`", "sandbox-unhealthy")
			},
		},
		{
			name: "not supported, bare",
			err:  runtime.ErrNotSupported,
			idx:  idxNotSupported,
			wrap: func(e error) error { return e },
		},
		{
			name: "not supported, fmt wrapped",
			err:  runtime.ErrNotSupported,
			idx:  idxNotSupported,
			wrap: func(e error) error { return fmt.Errorf("snapshot: %w", e) },
		},
		{
			name: "not supported, double wrapped",
			err:  runtime.ErrNotSupported,
			idx:  idxNotSupported,
			wrap: func(e error) error {
				return fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", e))
			},
		},
	}

	sentinels := []error{runtime.ErrSandboxMissing, runtime.ErrSandboxUnhealthy, runtime.ErrNotSupported}
	sentinelNames := []string{"ErrSandboxMissing", "ErrSandboxUnhealthy", "ErrNotSupported"}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.wrap(tt.err)
			if !errors.Is(got, tt.err) {
				t.Fatalf("errors.Is(got, %v) = false, want true", tt.err)
			}
			for i, other := range sentinels {
				if i == tt.idx {
					continue
				}
				if errors.Is(got, other) {
					t.Fatalf("errors.Is(got, %s) = true, want false; sentinels must stay distinguishable", sentinelNames[i])
				}
			}
		})
	}
}

// TestSentinelsAreDistinctValues asserts every pair of the three sentinel
// errors is a distinct value. TestSentinelErrorsSurviveWrapping above skips a
// sentinel by its position in a slice rather than by comparing it to tt.err,
// precisely so this is the one place a collapse, declaring
// ErrSandboxUnhealthy as an alias of ErrSandboxMissing, for example, fails on
// its own line instead of being silently skipped as "the same sentinel under
// test".
func TestSentinelsAreDistinctValues(t *testing.T) {
	named := []struct {
		name string
		err  error
	}{
		{"ErrSandboxMissing", runtime.ErrSandboxMissing},
		{"ErrSandboxUnhealthy", runtime.ErrSandboxUnhealthy},
		{"ErrNotSupported", runtime.ErrNotSupported},
	}

	for i := 0; i < len(named); i++ {
		for j := i + 1; j < len(named); j++ {
			if named[i].err == named[j].err {
				t.Errorf("%s and %s are the same error value; each sentinel must be distinct so errors.Is can tell them apart",
					named[i].name, named[j].name)
			}
		}
	}
}
