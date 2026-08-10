package runtime_test

import (
	"context"
	"io/fs"
	"reflect"
	"testing"
	"time"

	"github.com/JoottunAtish/ShellForge/internal/runtime"
)

// fakeRuntime, fakeSession, and fakePTY are minimal conformance fakes. They
// hold no logic. Their only purpose is to prove the interfaces are
// implementable; the contract test suite ticket covers behavioural testing.

type fakeRuntime struct{}

func (*fakeRuntime) Provision(ctx context.Context, spec runtime.ImageSpec) error { return nil }
func (*fakeRuntime) Destroy(ctx context.Context) error                           { return nil }
func (*fakeRuntime) Status(ctx context.Context) (runtime.Status, error) {
	return runtime.Status{}, nil
}
func (*fakeRuntime) StartSession(ctx context.Context, spec runtime.SessionSpec) (runtime.Session, error) {
	return nil, nil
}
func (*fakeRuntime) Capabilities() runtime.Caps { return runtime.Caps{} }

type fakeSession struct{}

func (*fakeSession) Exec(ctx context.Context, argv []string, opts runtime.ExecOpts) (runtime.ExecResult, error) {
	return runtime.ExecResult{}, nil
}
func (*fakeSession) Attach(ctx context.Context, opts runtime.AttachOpts) (runtime.PTY, error) {
	return nil, nil
}
func (*fakeSession) PushFiles(ctx context.Context, m runtime.FileManifest) error { return nil }
func (*fakeSession) PullFile(ctx context.Context, path string) ([]byte, error)   { return nil, nil }
func (*fakeSession) Close() error                                                { return nil }

type fakePTY struct{}

func (*fakePTY) Read(p []byte) (int, error)     { return 0, nil }
func (*fakePTY) Write(p []byte) (int, error)    { return 0, nil }
func (*fakePTY) Close() error                   { return nil }
func (*fakePTY) Resize(rows, cols uint16) error { return nil }
func (*fakePTY) Wait() error                    { return nil }

var (
	_ runtime.Runtime = (*fakeRuntime)(nil)
	_ runtime.Session = (*fakeSession)(nil)
	_ runtime.PTY     = (*fakePTY)(nil)
)

// TestFakesImplementBothInterfaces restates the compile-time assertions above
// so that a reader who greps for "func Test" finds this file. This is the
// real test for an interface-only package: it proves the interface is
// implementable.
//
// It does not compare the interface values above to nil: a typed nil pointer
// stored in an interface value is never itself a nil interface, so
// "r == nil" can never be false here and that assertion could not have
// failed no matter what the fakes did. Instead it calls a method on each
// fake and checks the exact zero value the fakes promise to return, which
// does fail if a fake stops doing what it says.
func TestFakesImplementBothInterfaces(t *testing.T) {
	var r runtime.Runtime = (*fakeRuntime)(nil)
	if caps := r.Capabilities(); caps != (runtime.Caps{}) {
		t.Errorf("fakeRuntime.Capabilities() = %+v, want the zero value", caps)
	}
	if status, err := r.Status(context.Background()); err != nil || status != (runtime.Status{}) {
		t.Errorf("fakeRuntime.Status() = (%+v, %v), want (zero value, nil)", status, err)
	}

	var s runtime.Session = (*fakeSession)(nil)
	if content, err := s.PullFile(context.Background(), "/x"); err != nil || content != nil {
		t.Errorf("fakeSession.PullFile() = (%v, %v), want (nil, nil)", content, err)
	}

	var p runtime.PTY = (*fakePTY)(nil)
	if err := p.Wait(); err != nil {
		t.Errorf("fakePTY.Wait() = %v, want nil", err)
	}
}

// TestValueTypeFieldSets pins the field set of every value type in the
// ticket. reflect.Type.NumField catches a field silently added or removed;
// FieldByName plus a type comparison catches a rename or a retype, such as
// FileEntry.Mode changing from fs.FileMode to uint32, that NumField alone
// would miss because the count would stay the same.
func TestValueTypeFieldSets(t *testing.T) {
	tests := []struct {
		name   string
		typ    reflect.Type
		fields map[string]reflect.Type
	}{
		{
			name: "ImageSpec",
			typ:  reflect.TypeOf(runtime.ImageSpec{}),
			fields: map[string]reflect.Type{
				"Name":      reflect.TypeOf(""),
				"Tag":       reflect.TypeOf(""),
				"Reference": reflect.TypeOf(""),
				"SHA256":    reflect.TypeOf(""),
			},
		},
		{
			name: "SessionSpec",
			typ:  reflect.TypeOf(runtime.SessionSpec{}),
			fields: map[string]reflect.Type{
				"User":       reflect.TypeOf(""),
				"WorkDir":    reflect.TypeOf(""),
				"Env":        reflect.TypeOf(map[string]string(nil)),
				"StateDir":   reflect.TypeOf(""),
				"Networking": reflect.TypeOf(false),
			},
		},
		{
			name: "ExecOpts",
			typ:  reflect.TypeOf(runtime.ExecOpts{}),
			fields: map[string]reflect.Type{
				"User":    reflect.TypeOf(""),
				"WorkDir": reflect.TypeOf(""),
				"Env":     reflect.TypeOf(map[string]string(nil)),
				"Stdin":   reflect.TypeOf([]byte(nil)),
				"Timeout": reflect.TypeOf(time.Duration(0)),
			},
		},
		{
			name: "ExecResult",
			typ:  reflect.TypeOf(runtime.ExecResult{}),
			fields: map[string]reflect.Type{
				"Stdout":   reflect.TypeOf([]byte(nil)),
				"Stderr":   reflect.TypeOf([]byte(nil)),
				"ExitCode": reflect.TypeOf(0),
				"Duration": reflect.TypeOf(time.Duration(0)),
				"TimedOut": reflect.TypeOf(false),
			},
		},
		{
			name: "AttachOpts",
			typ:  reflect.TypeOf(runtime.AttachOpts{}),
			fields: map[string]reflect.Type{
				"User":    reflect.TypeOf(""),
				"WorkDir": reflect.TypeOf(""),
				"Env":     reflect.TypeOf(map[string]string(nil)),
				"Command": reflect.TypeOf([]string(nil)),
			},
		},
		{
			name: "FileManifest",
			typ:  reflect.TypeOf(runtime.FileManifest{}),
			fields: map[string]reflect.Type{
				"Files": reflect.TypeOf([]runtime.FileEntry(nil)),
			},
		},
		{
			name: "FileEntry",
			typ:  reflect.TypeOf(runtime.FileEntry{}),
			fields: map[string]reflect.Type{
				"Path":    reflect.TypeOf(""),
				"Content": reflect.TypeOf([]byte(nil)),
				"Mode":    reflect.TypeOf(fs.FileMode(0)),
				"Owner":   reflect.TypeOf(""),
			},
		},
		{
			name: "Caps",
			typ:  reflect.TypeOf(runtime.Caps{}),
			fields: map[string]reflect.Type{
				"Networking":   reflect.TypeOf(false),
				"Systemd":      reflect.TypeOf(false),
				"MultiUser":    reflect.TypeOf(false),
				"Snapshotting": reflect.TypeOf(false),
				"Privileged":   reflect.TypeOf(false),
			},
		},
		{
			name: "Status",
			typ:  reflect.TypeOf(runtime.Status{}),
			fields: map[string]reflect.Type{
				"Provisioned": reflect.TypeOf(false),
				"Running":     reflect.TypeOf(false),
				"Backend":     reflect.TypeOf(""),
				"Detail":      reflect.TypeOf(""),
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got, want := tt.typ.NumField(), len(tt.fields); got != want {
				t.Fatalf("%s has %d fields, want %d; the field set must match the ticket exactly", tt.name, got, want)
			}
			for name, wantType := range tt.fields {
				f, ok := tt.typ.FieldByName(name)
				if !ok {
					t.Errorf("%s has no field %s", tt.name, name)
					continue
				}
				if f.Type != wantType {
					t.Errorf("%s.%s is %s, want %s", tt.name, name, f.Type, wantType)
				}
			}
		})
	}
}

var (
	ctxType = reflect.TypeOf((*context.Context)(nil)).Elem()
	errType = reflect.TypeOf((*error)(nil)).Elem()
	rtType  = reflect.TypeOf((*runtime.Runtime)(nil)).Elem()
	sesType = reflect.TypeOf((*runtime.Session)(nil)).Elem()
	ptyType = reflect.TypeOf((*runtime.PTY)(nil)).Elem()
)

// TestInterfaceMethodSignatures asserts every Runtime, Session, and PTY
// method has exactly the parameter and result types the ticket specifies. A
// reflect.Type comparison here discriminates a renamed type, a reordered
// parameter, or a widened type such as a string in place of []string.
func TestInterfaceMethodSignatures(t *testing.T) {
	tests := []struct {
		name   string
		iface  reflect.Type
		method string
		in     []reflect.Type
		out    []reflect.Type
	}{
		{
			name:   "Runtime.Provision",
			iface:  rtType,
			method: "Provision",
			in:     []reflect.Type{ctxType, reflect.TypeOf(runtime.ImageSpec{})},
			out:    []reflect.Type{errType},
		},
		{
			name:   "Runtime.Destroy",
			iface:  rtType,
			method: "Destroy",
			in:     []reflect.Type{ctxType},
			out:    []reflect.Type{errType},
		},
		{
			name:   "Runtime.Status",
			iface:  rtType,
			method: "Status",
			in:     []reflect.Type{ctxType},
			out:    []reflect.Type{reflect.TypeOf(runtime.Status{}), errType},
		},
		{
			name:   "Runtime.StartSession",
			iface:  rtType,
			method: "StartSession",
			in:     []reflect.Type{ctxType, reflect.TypeOf(runtime.SessionSpec{})},
			out:    []reflect.Type{sesType, errType},
		},
		{
			name:   "Runtime.Capabilities",
			iface:  rtType,
			method: "Capabilities",
			in:     nil,
			out:    []reflect.Type{reflect.TypeOf(runtime.Caps{})},
		},
		{
			name:   "Session.Exec",
			iface:  sesType,
			method: "Exec",
			in:     []reflect.Type{ctxType, reflect.TypeOf([]string(nil)), reflect.TypeOf(runtime.ExecOpts{})},
			out:    []reflect.Type{reflect.TypeOf(runtime.ExecResult{}), errType},
		},
		{
			name:   "Session.Attach",
			iface:  sesType,
			method: "Attach",
			in:     []reflect.Type{ctxType, reflect.TypeOf(runtime.AttachOpts{})},
			out:    []reflect.Type{ptyType, errType},
		},
		{
			name:   "Session.PushFiles",
			iface:  sesType,
			method: "PushFiles",
			in:     []reflect.Type{ctxType, reflect.TypeOf(runtime.FileManifest{})},
			out:    []reflect.Type{errType},
		},
		{
			name:   "Session.PullFile",
			iface:  sesType,
			method: "PullFile",
			in:     []reflect.Type{ctxType, reflect.TypeOf("")},
			out:    []reflect.Type{reflect.TypeOf([]byte(nil)), errType},
		},
		{
			name:   "Session.Close",
			iface:  sesType,
			method: "Close",
			in:     nil,
			out:    []reflect.Type{errType},
		},
		{
			name:   "PTY.Read",
			iface:  ptyType,
			method: "Read",
			in:     []reflect.Type{reflect.TypeOf([]byte(nil))},
			out:    []reflect.Type{reflect.TypeOf(0), errType},
		},
		{
			name:   "PTY.Write",
			iface:  ptyType,
			method: "Write",
			in:     []reflect.Type{reflect.TypeOf([]byte(nil))},
			out:    []reflect.Type{reflect.TypeOf(0), errType},
		},
		{
			name:   "PTY.Close",
			iface:  ptyType,
			method: "Close",
			in:     nil,
			out:    []reflect.Type{errType},
		},
		{
			name:   "PTY.Resize",
			iface:  ptyType,
			method: "Resize",
			in:     []reflect.Type{reflect.TypeOf(uint16(0)), reflect.TypeOf(uint16(0))},
			out:    []reflect.Type{errType},
		},
		{
			name:   "PTY.Wait",
			iface:  ptyType,
			method: "Wait",
			in:     nil,
			out:    []reflect.Type{errType},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m, ok := tt.iface.MethodByName(tt.method)
			if !ok {
				t.Fatalf("%s: method %q not found on interface", tt.name, tt.method)
			}
			if got, want := m.Type.NumIn(), len(tt.in); got != want {
				t.Fatalf("%s: %d parameters, want %d", tt.name, got, want)
			}
			for i, want := range tt.in {
				if got := m.Type.In(i); got != want {
					t.Errorf("%s: parameter %d is %s, want %s", tt.name, i, got, want)
				}
			}
			if got, want := m.Type.NumOut(), len(tt.out); got != want {
				t.Fatalf("%s: %d results, want %d", tt.name, got, want)
			}
			for i, want := range tt.out {
				if got := m.Type.Out(i); got != want {
					t.Errorf("%s: result %d is %s, want %s", tt.name, i, got, want)
				}
			}
		})
	}
}
