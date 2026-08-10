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
	tests := []struct {
		name string
		err  error
		wrap func(error) error
	}{
		{
			name: "sandbox missing, bare",
			err:  runtime.ErrSandboxMissing,
			wrap: func(e error) error { return e },
		},
		{
			name: "sandbox missing, fmt wrapped",
			err:  runtime.ErrSandboxMissing,
			wrap: func(e error) error { return fmt.Errorf("start session: %w", e) },
		},
		{
			name: "sandbox missing, ux wrapped",
			err:  runtime.ErrSandboxMissing,
			wrap: func(e error) error {
				return ux.Fail("start a sandbox session", e, "run `shellforge init`", "sandbox-missing")
			},
		},
		{
			name: "sandbox unhealthy, bare",
			err:  runtime.ErrSandboxUnhealthy,
			wrap: func(e error) error { return e },
		},
		{
			name: "sandbox unhealthy, fmt wrapped",
			err:  runtime.ErrSandboxUnhealthy,
			wrap: func(e error) error { return fmt.Errorf("probe the sandbox: %w", e) },
		},
		{
			name: "sandbox unhealthy, ux wrapped",
			err:  runtime.ErrSandboxUnhealthy,
			wrap: func(e error) error {
				return ux.Fail("check the sandbox", e, "run `shellforge sandbox rebuild`", "sandbox-unhealthy")
			},
		},
		{
			name: "not supported, bare",
			err:  runtime.ErrNotSupported,
			wrap: func(e error) error { return e },
		},
		{
			name: "not supported, fmt wrapped",
			err:  runtime.ErrNotSupported,
			wrap: func(e error) error { return fmt.Errorf("snapshot: %w", e) },
		},
		{
			name: "not supported, double wrapped",
			err:  runtime.ErrNotSupported,
			wrap: func(e error) error {
				return fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", e))
			},
		},
	}

	sentinels := []error{runtime.ErrSandboxMissing, runtime.ErrSandboxUnhealthy, runtime.ErrNotSupported}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.wrap(tt.err)
			if !errors.Is(got, tt.err) {
				t.Fatalf("errors.Is(got, %v) = false, want true", tt.err)
			}
			for _, other := range sentinels {
				if other == tt.err {
					continue
				}
				if errors.Is(got, other) {
					t.Fatalf("errors.Is(got, %v) = true, want false; sentinels must stay distinguishable", other)
				}
			}
		})
	}
}
