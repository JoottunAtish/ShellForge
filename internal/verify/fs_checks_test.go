package verify

import (
	"context"
	"testing"

	"github.com/JoottunAtish/ShellForge/internal/runtime"
	"github.com/JoottunAtish/ShellForge/internal/verify/verifytest"
)

func init() {
	markCovered("file_exists", "file_absent", "file_content", "dir_exists",
		"dir_tree", "file_mode", "file_owner", "symlink_target")
}

func mustFactory(t *testing.T, typeName string, spec Spec) Check {
	t.Helper()
	f, ok := Lookup(typeName)
	if !ok {
		t.Fatalf("no factory registered for %q", typeName)
	}
	spec.Type = typeName
	c, err := f(spec)
	if err != nil {
		t.Fatalf("building %s check: %v", typeName, err)
	}
	return c
}

func runCheck(c Check, sess *verifytest.Session) Result {
	return c.Run(context.Background(), Env{Session: sess})
}

// statExists is a stat -c '%F|%a|%U|%G' success line.
func statExists(fileType, mode, owner, group string) runtime.ExecResult {
	return runtime.ExecResult{ExitCode: 0, Stdout: []byte(fileType + "|" + mode + "|" + owner + "|" + group + "\n")}
}

func statMissing(path string) runtime.ExecResult {
	return runtime.ExecResult{ExitCode: 1, Stderr: []byte("stat: cannot statx '" + path + "': No such file or directory\n")}
}

func statPermissionDenied(path string) runtime.ExecResult {
	return runtime.ExecResult{ExitCode: 1, Stderr: []byte("stat: cannot statx '" + path + "': Permission denied\n")}
}

func TestFileExistsCheck(t *testing.T) {
	spec := Spec{ID: "obj1", OnFail: "no report.txt"}

	t.Run("pass", func(t *testing.T) {
		c := mustFactory(t, "file_exists", Spec{ID: "obj1", OnFail: "x", Params: map[string]any{"path": "/a"}})
		sess := verifytest.NewSession(verifytest.Const(statExists("regular file", "644", "learner", "learner"), nil))
		r := runCheck(c, sess)
		if r.Status != StatusPass {
			t.Fatalf("got %+v", r)
		}
	})

	t.Run("fail: missing target", func(t *testing.T) {
		spec.Params = map[string]any{"path": "/a"}
		c := mustFactory(t, "file_exists", spec)
		sess := verifytest.NewSession(verifytest.Const(statMissing("/a"), nil))
		r := runCheck(c, sess)
		if r.Status != StatusFail || r.Message != spec.OnFail {
			t.Fatalf("got %+v", r)
		}
	})

	t.Run("error: permission denied while probing", func(t *testing.T) {
		spec.Params = map[string]any{"path": "/a"}
		c := mustFactory(t, "file_exists", spec)
		sess := verifytest.NewSession(verifytest.Const(statPermissionDenied("/a"), nil))
		r := runCheck(c, sess)
		if r.Status != StatusError {
			t.Fatalf("got %+v, want StatusError", r)
		}
	})

	t.Run("error: Exec fails outright", func(t *testing.T) {
		spec.Params = map[string]any{"path": "/a"}
		c := mustFactory(t, "file_exists", spec)
		sess := verifytest.NewSession(verifytest.Const(runtime.ExecResult{}, context.Canceled))
		r := runCheck(c, sess)
		if r.Status != StatusError {
			t.Fatalf("got %+v, want StatusError", r)
		}
	})

	t.Run("factory: missing path param", func(t *testing.T) {
		if _, err := newFileExistsCheck(Spec{ID: "x", Params: map[string]any{}}); err == nil {
			t.Fatal("expected an error for a missing path param")
		}
	})
}

func TestFileAbsentCheck(t *testing.T) {
	t.Run("pass: does not exist", func(t *testing.T) {
		c := mustFactory(t, "file_absent", Spec{ID: "o", OnFail: "x", Params: map[string]any{"path": "/scratch.tmp"}})
		sess := verifytest.NewSession(verifytest.Const(statMissing("/scratch.tmp"), nil))
		r := runCheck(c, sess)
		if r.Status != StatusPass {
			t.Fatalf("got %+v", r)
		}
	})

	t.Run("fail: exists when it should not", func(t *testing.T) {
		c := mustFactory(t, "file_absent", Spec{ID: "o", OnFail: "still there", Params: map[string]any{"path": "/scratch.tmp"}})
		sess := verifytest.NewSession(verifytest.Const(statExists("regular file", "644", "learner", "learner"), nil))
		r := runCheck(c, sess)
		if r.Status != StatusFail || r.Message != "still there" {
			t.Fatalf("got %+v", r)
		}
	})

	t.Run("error: exists but cannot be stated, must not read as pass", func(t *testing.T) {
		c := mustFactory(t, "file_absent", Spec{ID: "o", OnFail: "x", Params: map[string]any{"path": "/scratch.tmp"}})
		sess := verifytest.NewSession(verifytest.Const(statPermissionDenied("/scratch.tmp"), nil))
		r := runCheck(c, sess)
		if r.Status != StatusError {
			t.Fatalf("a permission failure while probing must be StatusError, not StatusPass: got %+v", r)
		}
	})
}

func TestFileContentCheck(t *testing.T) {
	newCheck := func(t *testing.T, params map[string]any) Check {
		t.Helper()
		return mustFactory(t, "file_content", Spec{ID: "o", OnFail: "wrong content", Params: params})
	}

	t.Run("exact pass", func(t *testing.T) {
		c := newCheck(t, map[string]any{"path": "/f", "match": "exact", "value": "147"})
		sess := verifytest.NewSession(verifytest.Const(runtime.ExecResult{ExitCode: 0, Stdout: []byte("147")}, nil))
		if r := runCheck(c, sess); r.Status != StatusPass {
			t.Fatalf("got %+v", r)
		}
	})

	t.Run("exact fail", func(t *testing.T) {
		c := newCheck(t, map[string]any{"path": "/f", "match": "exact", "value": "147"})
		sess := verifytest.NewSession(verifytest.Const(runtime.ExecResult{ExitCode: 0, Stdout: []byte("148")}, nil))
		if r := runCheck(c, sess); r.Status != StatusFail {
			t.Fatalf("got %+v", r)
		}
	})

	t.Run("trimmed_equals ignores surrounding whitespace", func(t *testing.T) {
		c := newCheck(t, map[string]any{"path": "/f", "match": "trimmed_equals", "value": "147"})
		sess := verifytest.NewSession(verifytest.Const(runtime.ExecResult{ExitCode: 0, Stdout: []byte("  147\n")}, nil))
		if r := runCheck(c, sess); r.Status != StatusPass {
			t.Fatalf("got %+v", r)
		}
	})

	t.Run("contains", func(t *testing.T) {
		c := newCheck(t, map[string]any{"path": "/f", "match": "contains", "value": "ERROR"})
		sess := verifytest.NewSession(verifytest.Const(runtime.ExecResult{ExitCode: 0, Stdout: []byte("147 ERROR lines")}, nil))
		if r := runCheck(c, sess); r.Status != StatusPass {
			t.Fatalf("got %+v", r)
		}
	})

	t.Run("regex", func(t *testing.T) {
		c := newCheck(t, map[string]any{"path": "/f", "match": "regex", "value": `^\d+$`})
		sess := verifytest.NewSession(verifytest.Const(runtime.ExecResult{ExitCode: 0, Stdout: []byte("147")}, nil))
		if r := runCheck(c, sess); r.Status != StatusPass {
			t.Fatalf("got %+v", r)
		}
	})

	t.Run("sha256", func(t *testing.T) {
		// sha256("hello") = 2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824
		c := newCheck(t, map[string]any{"path": "/f", "match": "sha256", "value": "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"})
		sess := verifytest.NewSession(verifytest.Const(runtime.ExecResult{ExitCode: 0, Stdout: []byte("hello")}, nil))
		if r := runCheck(c, sess); r.Status != StatusPass {
			t.Fatalf("got %+v", r)
		}
	})

	t.Run("line_count", func(t *testing.T) {
		c := newCheck(t, map[string]any{"path": "/f", "match": "line_count", "value": "3"})
		sess := verifytest.NewSession(verifytest.Const(runtime.ExecResult{ExitCode: 0, Stdout: []byte("a\nb\nc\n")}, nil))
		if r := runCheck(c, sess); r.Status != StatusPass {
			t.Fatalf("got %+v", r)
		}
	})

	t.Run("ignore_case", func(t *testing.T) {
		c := newCheck(t, map[string]any{"path": "/f", "match": "exact", "value": "OK", "ignore_case": true})
		sess := verifytest.NewSession(verifytest.Const(runtime.ExecResult{ExitCode: 0, Stdout: []byte("ok")}, nil))
		if r := runCheck(c, sess); r.Status != StatusPass {
			t.Fatalf("got %+v", r)
		}
	})

	t.Run("fail: missing target", func(t *testing.T) {
		c := newCheck(t, map[string]any{"path": "/f", "match": "exact", "value": "147"})
		sess := verifytest.NewSession(verifytest.Const(runtime.ExecResult{
			ExitCode: 1, Stderr: []byte("cat: /f: No such file or directory\n"),
		}, nil))
		r := runCheck(c, sess)
		if r.Status != StatusFail {
			t.Fatalf("got %+v", r)
		}
	})

	t.Run("error: permission denied reading", func(t *testing.T) {
		c := newCheck(t, map[string]any{"path": "/f", "match": "exact", "value": "147"})
		sess := verifytest.NewSession(verifytest.Const(runtime.ExecResult{
			ExitCode: 1, Stderr: []byte("cat: /f: Permission denied\n"),
		}, nil))
		r := runCheck(c, sess)
		if r.Status != StatusError {
			t.Fatalf("got %+v", r)
		}
	})

	t.Run("factory: unknown match mode", func(t *testing.T) {
		if _, err := newFileContentCheck(Spec{ID: "o", Params: map[string]any{"path": "/f", "match": "sparkles", "value": "x"}}); err == nil {
			t.Fatal("expected an error for an unknown match mode")
		}
	})

	t.Run("factory: bad regex", func(t *testing.T) {
		if _, err := newFileContentCheck(Spec{ID: "o", Params: map[string]any{"path": "/f", "match": "regex", "value": "("}}); err == nil {
			t.Fatal("expected an error for an invalid regex")
		}
	})

	t.Run("factory: non-integer line_count value", func(t *testing.T) {
		if _, err := newFileContentCheck(Spec{ID: "o", Params: map[string]any{"path": "/f", "match": "line_count", "value": "three"}}); err == nil {
			t.Fatal("expected an error for a non-integer line_count value")
		}
	})
}

func TestDirExistsCheck(t *testing.T) {
	t.Run("pass", func(t *testing.T) {
		c := mustFactory(t, "dir_exists", Spec{ID: "o", OnFail: "x", Params: map[string]any{"path": "/logs"}})
		sess := verifytest.NewSession(verifytest.Const(statExists("directory", "755", "learner", "learner"), nil))
		if r := runCheck(c, sess); r.Status != StatusPass {
			t.Fatalf("got %+v", r)
		}
	})

	t.Run("fail: exists but is a file, not a directory", func(t *testing.T) {
		c := mustFactory(t, "dir_exists", Spec{ID: "o", OnFail: "not a dir", Params: map[string]any{"path": "/logs"}})
		sess := verifytest.NewSession(verifytest.Const(statExists("regular file", "644", "learner", "learner"), nil))
		if r := runCheck(c, sess); r.Status != StatusFail || r.Message != "not a dir" {
			t.Fatalf("got %+v", r)
		}
	})

	t.Run("fail: missing target", func(t *testing.T) {
		c := mustFactory(t, "dir_exists", Spec{ID: "o", OnFail: "x", Params: map[string]any{"path": "/logs"}})
		sess := verifytest.NewSession(verifytest.Const(statMissing("/logs"), nil))
		if r := runCheck(c, sess); r.Status != StatusFail {
			t.Fatalf("got %+v", r)
		}
	})

	t.Run("error: permission denied", func(t *testing.T) {
		c := mustFactory(t, "dir_exists", Spec{ID: "o", OnFail: "x", Params: map[string]any{"path": "/logs"}})
		sess := verifytest.NewSession(verifytest.Const(statPermissionDenied("/logs"), nil))
		if r := runCheck(c, sess); r.Status != StatusError {
			t.Fatalf("got %+v", r)
		}
	})
}

func TestDirTreeCheck(t *testing.T) {
	t.Run("names_only pass", func(t *testing.T) {
		c := mustFactory(t, "dir_tree", Spec{ID: "o", OnFail: "mismatch", Params: map[string]any{
			"path": "/config.bak", "compare_to": "/config", "mode": "names_only",
		}})
		sess := verifytest.NewSession(
			verifytest.Const(runtime.ExecResult{ExitCode: 0, Stdout: []byte("a.txt\nsub\nsub/b.txt\n")}, nil),
			verifytest.Const(runtime.ExecResult{ExitCode: 0, Stdout: []byte("sub/b.txt\na.txt\nsub\n")}, nil),
		)
		if r := runCheck(c, sess); r.Status != StatusPass {
			t.Fatalf("got %+v", r)
		}
	})

	t.Run("names_only fail: a file is missing", func(t *testing.T) {
		c := mustFactory(t, "dir_tree", Spec{ID: "o", OnFail: "mismatch", Params: map[string]any{
			"path": "/config.bak", "compare_to": "/config", "mode": "names_only",
		}})
		sess := verifytest.NewSession(
			verifytest.Const(runtime.ExecResult{ExitCode: 0, Stdout: []byte("a.txt\n")}, nil),
			verifytest.Const(runtime.ExecResult{ExitCode: 0, Stdout: []byte("a.txt\nb.txt\n")}, nil),
		)
		r := runCheck(c, sess)
		if r.Status != StatusFail || r.Message != "mismatch" {
			t.Fatalf("got %+v", r)
		}
	})

	t.Run("names_and_hashes pass", func(t *testing.T) {
		c := mustFactory(t, "dir_tree", Spec{ID: "o", OnFail: "mismatch", Params: map[string]any{
			"path": "/config.bak", "compare_to": "/config", "mode": "names_and_hashes",
		}})
		sess := verifytest.NewSession(
			verifytest.Const(runtime.ExecResult{ExitCode: 0, Stdout: []byte("a.txt\n")}, nil),
			verifytest.Const(runtime.ExecResult{ExitCode: 0, Stdout: []byte("a.txt\n")}, nil),
			verifytest.Const(runtime.ExecResult{ExitCode: 0, Stdout: []byte("deadbeef  /config.bak/a.txt\n")}, nil),
			verifytest.Const(runtime.ExecResult{ExitCode: 0, Stdout: []byte("deadbeef  /config/a.txt\n")}, nil),
		)
		if r := runCheck(c, sess); r.Status != StatusPass {
			t.Fatalf("got %+v", r)
		}
	})

	t.Run("names_and_hashes fail: same names, different content", func(t *testing.T) {
		c := mustFactory(t, "dir_tree", Spec{ID: "o", OnFail: "mismatch", Params: map[string]any{
			"path": "/config.bak", "compare_to": "/config", "mode": "names_and_hashes",
		}})
		sess := verifytest.NewSession(
			verifytest.Const(runtime.ExecResult{ExitCode: 0, Stdout: []byte("a.txt\n")}, nil),
			verifytest.Const(runtime.ExecResult{ExitCode: 0, Stdout: []byte("a.txt\n")}, nil),
			verifytest.Const(runtime.ExecResult{ExitCode: 0, Stdout: []byte("deadbeef  /config.bak/a.txt\n")}, nil),
			verifytest.Const(runtime.ExecResult{ExitCode: 0, Stdout: []byte("cafebabe  /config/a.txt\n")}, nil),
		)
		if r := runCheck(c, sess); r.Status != StatusFail {
			t.Fatalf("got %+v", r)
		}
	})

	t.Run("error: missing compare_to target", func(t *testing.T) {
		c := mustFactory(t, "dir_tree", Spec{ID: "o", OnFail: "mismatch", Params: map[string]any{
			"path": "/config.bak", "compare_to": "/config", "mode": "names_only",
		}})
		sess := verifytest.NewSession(
			verifytest.Const(runtime.ExecResult{ExitCode: 0, Stdout: []byte("a.txt\n")}, nil),
			verifytest.Const(runtime.ExecResult{ExitCode: 1, Stderr: []byte("find: '/config': No such file or directory\n")}, nil),
		)
		if r := runCheck(c, sess); r.Status != StatusError {
			t.Fatalf("got %+v", r)
		}
	})

	t.Run("factory: unknown mode", func(t *testing.T) {
		if _, err := newDirTreeCheck(Spec{ID: "o", Params: map[string]any{"path": "/a", "compare_to": "/b", "mode": "bogus"}}); err == nil {
			t.Fatal("expected an error for an unknown mode")
		}
	})
}

func TestFileModeCheck(t *testing.T) {
	t.Run("exact mode pass", func(t *testing.T) {
		c := mustFactory(t, "file_mode", Spec{ID: "o", OnFail: "wrong mode", Params: map[string]any{"path": "/deploy.sh", "mode": "0700"}})
		sess := verifytest.NewSession(verifytest.Const(statExists("regular file", "700", "learner", "learner"), nil))
		if r := runCheck(c, sess); r.Status != StatusPass {
			t.Fatalf("got %+v", r)
		}
	})

	t.Run("exact mode fail", func(t *testing.T) {
		c := mustFactory(t, "file_mode", Spec{ID: "o", OnFail: "wrong mode", Params: map[string]any{"path": "/deploy.sh", "mode": "0700"}})
		sess := verifytest.NewSession(verifytest.Const(statExists("regular file", "755", "learner", "learner"), nil))
		if r := runCheck(c, sess); r.Status != StatusFail || r.Message != "wrong mode" {
			t.Fatalf("got %+v", r)
		}
	})

	t.Run("any_of pass on the second candidate", func(t *testing.T) {
		c := mustFactory(t, "file_mode", Spec{ID: "o", OnFail: "wrong mode", Params: map[string]any{
			"path": "/deploy.sh", "any_of": []any{"0700", "0750"},
		}})
		sess := verifytest.NewSession(verifytest.Const(statExists("regular file", "750", "learner", "learner"), nil))
		if r := runCheck(c, sess); r.Status != StatusPass {
			t.Fatalf("got %+v", r)
		}
	})

	t.Run("fail: missing target", func(t *testing.T) {
		c := mustFactory(t, "file_mode", Spec{ID: "o", OnFail: "x", Params: map[string]any{"path": "/deploy.sh", "mode": "0700"}})
		sess := verifytest.NewSession(verifytest.Const(statMissing("/deploy.sh"), nil))
		if r := runCheck(c, sess); r.Status != StatusFail {
			t.Fatalf("got %+v", r)
		}
	})

	t.Run("error: permission denied", func(t *testing.T) {
		c := mustFactory(t, "file_mode", Spec{ID: "o", OnFail: "x", Params: map[string]any{"path": "/deploy.sh", "mode": "0700"}})
		sess := verifytest.NewSession(verifytest.Const(statPermissionDenied("/deploy.sh"), nil))
		if r := runCheck(c, sess); r.Status != StatusError {
			t.Fatalf("got %+v", r)
		}
	})

	t.Run("factory: neither mode nor any_of", func(t *testing.T) {
		if _, err := newFileModeCheck(Spec{ID: "o", Params: map[string]any{"path": "/deploy.sh"}}); err == nil {
			t.Fatal("expected an error when neither mode nor any_of is given")
		}
	})

	t.Run("factory: invalid octal mode", func(t *testing.T) {
		if _, err := newFileModeCheck(Spec{ID: "o", Params: map[string]any{"path": "/deploy.sh", "mode": "rwx"}}); err == nil {
			t.Fatal("expected an error for a non-octal mode string")
		}
	})
}

func TestFileOwnerCheck(t *testing.T) {
	t.Run("owner only, pass", func(t *testing.T) {
		c := mustFactory(t, "file_owner", Spec{ID: "o", OnFail: "wrong owner", Params: map[string]any{"path": "/data.db", "owner": "learner"}})
		sess := verifytest.NewSession(verifytest.Const(statExists("regular file", "644", "learner", "learner"), nil))
		if r := runCheck(c, sess); r.Status != StatusPass {
			t.Fatalf("got %+v", r)
		}
	})

	t.Run("owner and group, pass", func(t *testing.T) {
		c := mustFactory(t, "file_owner", Spec{ID: "o", OnFail: "wrong owner", Params: map[string]any{"path": "/data.db", "owner": "learner", "group": "logistics"}})
		sess := verifytest.NewSession(verifytest.Const(statExists("regular file", "644", "learner", "logistics"), nil))
		if r := runCheck(c, sess); r.Status != StatusPass {
			t.Fatalf("got %+v", r)
		}
	})

	t.Run("fail: right owner, wrong group", func(t *testing.T) {
		c := mustFactory(t, "file_owner", Spec{ID: "o", OnFail: "wrong owner", Params: map[string]any{"path": "/data.db", "owner": "learner", "group": "logistics"}})
		sess := verifytest.NewSession(verifytest.Const(statExists("regular file", "644", "learner", "learner"), nil))
		if r := runCheck(c, sess); r.Status != StatusFail {
			t.Fatalf("got %+v", r)
		}
	})

	t.Run("fail: missing target", func(t *testing.T) {
		c := mustFactory(t, "file_owner", Spec{ID: "o", OnFail: "x", Params: map[string]any{"path": "/data.db", "owner": "learner"}})
		sess := verifytest.NewSession(verifytest.Const(statMissing("/data.db"), nil))
		if r := runCheck(c, sess); r.Status != StatusFail {
			t.Fatalf("got %+v", r)
		}
	})

	t.Run("error: permission denied", func(t *testing.T) {
		c := mustFactory(t, "file_owner", Spec{ID: "o", OnFail: "x", Params: map[string]any{"path": "/data.db", "owner": "learner"}})
		sess := verifytest.NewSession(verifytest.Const(statPermissionDenied("/data.db"), nil))
		if r := runCheck(c, sess); r.Status != StatusError {
			t.Fatalf("got %+v", r)
		}
	})
}

func TestSymlinkTargetCheck(t *testing.T) {
	t.Run("pass", func(t *testing.T) {
		c := mustFactory(t, "symlink_target", Spec{ID: "o", OnFail: "wrong target", Params: map[string]any{"path": "/current", "target": "/releases/v2"}})
		sess := verifytest.NewSession(
			verifytest.Const(statExists("symbolic link", "777", "learner", "learner"), nil),
			verifytest.Const(runtime.ExecResult{ExitCode: 0, Stdout: []byte("/releases/v2\n")}, nil),
		)
		if r := runCheck(c, sess); r.Status != StatusPass {
			t.Fatalf("got %+v", r)
		}
	})

	t.Run("fail: points elsewhere", func(t *testing.T) {
		c := mustFactory(t, "symlink_target", Spec{ID: "o", OnFail: "wrong target", Params: map[string]any{"path": "/current", "target": "/releases/v2"}})
		sess := verifytest.NewSession(
			verifytest.Const(statExists("symbolic link", "777", "learner", "learner"), nil),
			verifytest.Const(runtime.ExecResult{ExitCode: 0, Stdout: []byte("/releases/v1\n")}, nil),
		)
		if r := runCheck(c, sess); r.Status != StatusFail || r.Message != "wrong target" {
			t.Fatalf("got %+v", r)
		}
	})

	t.Run("fail: missing target", func(t *testing.T) {
		c := mustFactory(t, "symlink_target", Spec{ID: "o", OnFail: "x", Params: map[string]any{"path": "/current", "target": "/releases/v2"}})
		sess := verifytest.NewSession(verifytest.Const(statMissing("/current"), nil))
		if r := runCheck(c, sess); r.Status != StatusFail {
			t.Fatalf("got %+v", r)
		}
	})

	t.Run("fail: exists but is not a symlink", func(t *testing.T) {
		c := mustFactory(t, "symlink_target", Spec{ID: "o", OnFail: "not a symlink", Params: map[string]any{"path": "/current", "target": "/releases/v2"}})
		sess := verifytest.NewSession(verifytest.Const(statExists("regular file", "644", "learner", "learner"), nil))
		if r := runCheck(c, sess); r.Status != StatusFail || r.Message != "not a symlink" {
			t.Fatalf("got %+v", r)
		}
	})

	t.Run("error: permission denied", func(t *testing.T) {
		c := mustFactory(t, "symlink_target", Spec{ID: "o", OnFail: "x", Params: map[string]any{"path": "/current", "target": "/releases/v2"}})
		sess := verifytest.NewSession(verifytest.Const(statPermissionDenied("/current"), nil))
		if r := runCheck(c, sess); r.Status != StatusError {
			t.Fatalf("got %+v", r)
		}
	})
}
