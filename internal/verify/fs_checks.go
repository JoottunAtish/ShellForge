package verify

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/JoottunAtish/ShellForge/internal/runtime"
)

func init() {
	Register("file_exists", newFileExistsCheck)
	Register("file_absent", newFileAbsentCheck)
	Register("file_content", newFileContentCheck)
	Register("dir_exists", newDirExistsCheck)
	Register("dir_tree", newDirTreeCheck)
	Register("file_mode", newFileModeCheck)
	Register("file_owner", newFileOwnerCheck)
	Register("symlink_target", newSymlinkTargetCheck)
}

// statInfo is what one `stat -c '%F|%a|%U|%G' <path>` call reports: file
// type in GNU stat's own words ("regular file", "directory", "symbolic
// link", ...), permission bits, owner, and group.
type statInfo struct {
	Type  string
	Mode  uint32
	Owner string
	Group string
}

// statPath probes path with a single Exec call. missing reports that the
// path does not exist, distinguished from every other failure by stat's own
// "No such file or directory" message. Any other non-zero exit, and any Go
// error from Exec itself, is returned as err: a probe that cannot tell
// whether the path exists (a permission failure walking a parent directory,
// for example) must never be reported as "does not exist", because a check
// built on that would tell a learner they solved file_absent when the real
// state is unknown.
func statPath(ctx context.Context, sess runtime.Session, path string) (info statInfo, missing bool, err error) {
	res, execErr := sess.Exec(ctx, []string{"stat", "-c", "%F|%a|%U|%G", path}, runtime.ExecOpts{})
	if execErr != nil {
		return statInfo{}, false, execErr
	}
	if res.ExitCode != 0 {
		stderr := string(res.Stderr)
		if strings.Contains(strings.ToLower(stderr), "no such file or directory") {
			return statInfo{}, true, nil
		}
		return statInfo{}, false, fmt.Errorf("stat %s: %s", path, strings.TrimSpace(stderr))
	}

	fields := strings.SplitN(strings.TrimRight(string(res.Stdout), "\n"), "|", 4)
	if len(fields) != 4 {
		return statInfo{}, false, fmt.Errorf("stat %s: unexpected output %q", path, res.Stdout)
	}
	mode, perr := strconv.ParseUint(fields[1], 8, 32)
	if perr != nil {
		return statInfo{}, false, fmt.Errorf("stat %s: parse mode %q: %w", path, fields[1], perr)
	}
	return statInfo{Type: fields[0], Mode: uint32(mode), Owner: fields[2], Group: fields[3]}, false, nil
}

// probeError builds the Status: error Result for a probe that could not
// determine an answer at all.
func probeError(id, op, path string, err error) Result {
	return Result{ID: id, Status: StatusError, Message: fmt.Sprintf("could not %s %s: %s", op, path, err)}
}

// --- file_exists ---

type fileExistsCheck struct {
	id, path, onFail string
}

func newFileExistsCheck(s Spec) (Check, error) {
	path, err := paramString(s.Params, "path", true)
	if err != nil {
		return nil, factoryError("file_exists", s.ID, err)
	}
	return &fileExistsCheck{id: s.ID, path: path, onFail: s.OnFail}, nil
}

func (c *fileExistsCheck) ID() string { return c.id }

func (c *fileExistsCheck) Run(ctx context.Context, env Env) Result {
	start := time.Now()
	_, missing, err := statPath(ctx, env.Session, c.path)
	if err != nil {
		r := probeError(c.id, "check whether", c.path, err)
		r.Duration = time.Since(start)
		return r
	}
	if missing {
		return Result{ID: c.id, Status: StatusFail, Message: c.onFail, Duration: time.Since(start)}
	}
	return Result{ID: c.id, Status: StatusPass, Duration: time.Since(start)}
}

// --- file_absent ---

type fileAbsentCheck struct {
	id, path, onFail string
}

func newFileAbsentCheck(s Spec) (Check, error) {
	path, err := paramString(s.Params, "path", true)
	if err != nil {
		return nil, factoryError("file_absent", s.ID, err)
	}
	return &fileAbsentCheck{id: s.ID, path: path, onFail: s.OnFail}, nil
}

func (c *fileAbsentCheck) ID() string { return c.id }

func (c *fileAbsentCheck) Run(ctx context.Context, env Env) Result {
	start := time.Now()
	_, missing, err := statPath(ctx, env.Session, c.path)
	if err != nil {
		// Exists but cannot be stated: a naive implementation would treat
		// this non-zero exit as "does not exist" and report the learner
		// solved it, which is wrong. Report that the answer is unknown
		// instead of guessing pass.
		r := probeError(c.id, "check whether", c.path, err)
		r.Duration = time.Since(start)
		return r
	}
	if missing {
		return Result{ID: c.id, Status: StatusPass, Duration: time.Since(start)}
	}
	return Result{ID: c.id, Status: StatusFail, Message: c.onFail, Duration: time.Since(start)}
}

// --- file_content ---

type contentMatch string

const (
	matchExact         contentMatch = "exact"
	matchTrimmedEquals contentMatch = "trimmed_equals"
	matchContains      contentMatch = "contains"
	matchRegex         contentMatch = "regex"
	matchSHA256        contentMatch = "sha256"
	matchLineCount     contentMatch = "line_count"
)

type fileContentCheck struct {
	id, path, onFail string
	match            contentMatch
	value            string
	ignoreCase       bool
	regex            *regexp.Regexp // set only when match == matchRegex
}

func newFileContentCheck(s Spec) (Check, error) {
	path, err := paramString(s.Params, "path", true)
	if err != nil {
		return nil, factoryError("file_content", s.ID, err)
	}
	matchRaw, err := paramString(s.Params, "match", true)
	if err != nil {
		return nil, factoryError("file_content", s.ID, err)
	}
	value, err := paramString(s.Params, "value", true)
	if err != nil {
		return nil, factoryError("file_content", s.ID, err)
	}
	ignoreCase, err := paramBool(s.Params, "ignore_case")
	if err != nil {
		return nil, factoryError("file_content", s.ID, err)
	}

	m := contentMatch(matchRaw)
	var re *regexp.Regexp
	switch m {
	case matchExact, matchTrimmedEquals, matchContains, matchSHA256, matchLineCount:
		// no further validation
	case matchRegex:
		pattern := value
		if ignoreCase {
			pattern = "(?i)" + pattern
		}
		re, err = regexp.Compile(pattern)
		if err != nil {
			return nil, factoryError("file_content", s.ID, fmt.Errorf("param %q is not a valid regex: %w", "value", err))
		}
	default:
		return nil, factoryError("file_content", s.ID, fmt.Errorf("unknown match mode %q: want exact, trimmed_equals, contains, regex, sha256, or line_count", matchRaw))
	}
	if m == matchLineCount {
		if _, err := strconv.Atoi(value); err != nil {
			return nil, factoryError("file_content", s.ID, fmt.Errorf("param %q must be an integer for match: line_count, got %q", "value", value))
		}
	}

	return &fileContentCheck{
		id: s.ID, path: path, onFail: s.OnFail,
		match: m, value: value, ignoreCase: ignoreCase, regex: re,
	}, nil
}

func (c *fileContentCheck) ID() string { return c.id }

func (c *fileContentCheck) Run(ctx context.Context, env Env) Result {
	start := time.Now()
	res, err := env.Session.Exec(ctx, []string{"cat", c.path}, runtime.ExecOpts{})
	if err != nil {
		r := probeError(c.id, "read", c.path, err)
		r.Duration = time.Since(start)
		return r
	}
	if res.ExitCode != 0 {
		stderr := string(res.Stderr)
		if strings.Contains(strings.ToLower(stderr), "no such file or directory") {
			return Result{ID: c.id, Status: StatusFail, Message: c.onFail, Duration: time.Since(start)}
		}
		r := probeError(c.id, "read", c.path, fmt.Errorf("%s", strings.TrimSpace(stderr)))
		r.Duration = time.Since(start)
		return r
	}

	if c.matches(res.Stdout) {
		return Result{ID: c.id, Status: StatusPass, Duration: time.Since(start)}
	}
	return Result{ID: c.id, Status: StatusFail, Message: c.onFail, Duration: time.Since(start)}
}

func (c *fileContentCheck) matches(content []byte) bool {
	body := string(content)
	fold := func(s string) string {
		if c.ignoreCase {
			return strings.ToLower(s)
		}
		return s
	}

	switch c.match {
	case matchExact:
		return fold(body) == fold(c.value)
	case matchTrimmedEquals:
		return fold(strings.TrimSpace(body)) == fold(strings.TrimSpace(c.value))
	case matchContains:
		return strings.Contains(fold(body), fold(c.value))
	case matchRegex:
		return c.regex.MatchString(body)
	case matchSHA256:
		sum := sha256.Sum256(content)
		return fold(hex.EncodeToString(sum[:])) == fold(strings.TrimSpace(c.value))
	case matchLineCount:
		want, _ := strconv.Atoi(c.value) // validated in the Factory
		return countLines(body) == want
	default:
		return false
	}
}

// countLines counts newline-terminated lines the way a learner would think
// of "how many lines", so a file ending in a single trailing newline is not
// counted as having one extra empty line.
func countLines(body string) int {
	if body == "" {
		return 0
	}
	return strings.Count(strings.TrimRight(body, "\n"), "\n") + 1
}

// --- dir_exists ---

type dirExistsCheck struct {
	id, path, onFail string
}

func newDirExistsCheck(s Spec) (Check, error) {
	path, err := paramString(s.Params, "path", true)
	if err != nil {
		return nil, factoryError("dir_exists", s.ID, err)
	}
	return &dirExistsCheck{id: s.ID, path: path, onFail: s.OnFail}, nil
}

func (c *dirExistsCheck) ID() string { return c.id }

func (c *dirExistsCheck) Run(ctx context.Context, env Env) Result {
	start := time.Now()
	info, missing, err := statPath(ctx, env.Session, c.path)
	if err != nil {
		r := probeError(c.id, "check whether", c.path, err)
		r.Duration = time.Since(start)
		return r
	}
	if missing || info.Type != "directory" {
		return Result{ID: c.id, Status: StatusFail, Message: c.onFail, Duration: time.Since(start)}
	}
	return Result{ID: c.id, Status: StatusPass, Duration: time.Since(start)}
}

// --- dir_tree ---

type dirTreeMode string

const (
	dirTreeNamesOnly      dirTreeMode = "names_only"
	dirTreeNamesAndHashes dirTreeMode = "names_and_hashes"
)

type dirTreeCheck struct {
	id, path, compareTo, onFail string
	mode                        dirTreeMode
}

func newDirTreeCheck(s Spec) (Check, error) {
	path, err := paramString(s.Params, "path", true)
	if err != nil {
		return nil, factoryError("dir_tree", s.ID, err)
	}
	compareTo, err := paramString(s.Params, "compare_to", true)
	if err != nil {
		return nil, factoryError("dir_tree", s.ID, err)
	}
	modeRaw, err := paramString(s.Params, "mode", true)
	if err != nil {
		return nil, factoryError("dir_tree", s.ID, err)
	}
	mode := dirTreeMode(modeRaw)
	if mode != dirTreeNamesOnly && mode != dirTreeNamesAndHashes {
		return nil, factoryError("dir_tree", s.ID, fmt.Errorf("unknown mode %q: want names_only or names_and_hashes", modeRaw))
	}
	return &dirTreeCheck{id: s.ID, path: path, compareTo: compareTo, onFail: s.OnFail, mode: mode}, nil
}

func (c *dirTreeCheck) ID() string { return c.id }

func (c *dirTreeCheck) Run(ctx context.Context, env Env) Result {
	start := time.Now()

	namesA, err := listRelativeEntries(ctx, env.Session, c.path)
	if err != nil {
		r := probeError(c.id, "list", c.path, err)
		r.Duration = time.Since(start)
		return r
	}
	namesB, err := listRelativeEntries(ctx, env.Session, c.compareTo)
	if err != nil {
		r := probeError(c.id, "list", c.compareTo, err)
		r.Duration = time.Since(start)
		return r
	}

	if !equalStringSets(namesA, namesB) {
		return Result{ID: c.id, Status: StatusFail, Message: c.onFail, Duration: time.Since(start)}
	}

	if c.mode == dirTreeNamesAndHashes {
		hashesA, err := hashRelativeFiles(ctx, env.Session, c.path)
		if err != nil {
			r := probeError(c.id, "hash files under", c.path, err)
			r.Duration = time.Since(start)
			return r
		}
		hashesB, err := hashRelativeFiles(ctx, env.Session, c.compareTo)
		if err != nil {
			r := probeError(c.id, "hash files under", c.compareTo, err)
			r.Duration = time.Since(start)
			return r
		}
		if !equalStringMaps(hashesA, hashesB) {
			return Result{ID: c.id, Status: StatusFail, Message: c.onFail, Duration: time.Since(start)}
		}
	}

	return Result{ID: c.id, Status: StatusPass, Duration: time.Since(start)}
}

// listRelativeEntries lists every entry under root, relative to root, using
// `find`'s own %P: the path with root's own prefix removed. The root itself
// prints as an empty string and is dropped.
func listRelativeEntries(ctx context.Context, sess runtime.Session, root string) ([]string, error) {
	res, err := sess.Exec(ctx, []string{"find", root, "-printf", "%P\n"}, runtime.ExecOpts{})
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("find %s: %s", root, strings.TrimSpace(string(res.Stderr)))
	}
	var out []string
	for _, line := range strings.Split(strings.TrimRight(string(res.Stdout), "\n"), "\n") {
		if line == "" {
			continue
		}
		out = append(out, line)
	}
	return out, nil
}

// hashRelativeFiles sha256-hashes every regular file under root and returns
// a map from the file's path relative to root to its hex digest.
func hashRelativeFiles(ctx context.Context, sess runtime.Session, root string) (map[string]string, error) {
	res, err := sess.Exec(ctx, []string{"find", root, "-type", "f", "-exec", "sha256sum", "{}", "+"}, runtime.ExecOpts{})
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("find %s -exec sha256sum: %s", root, strings.TrimSpace(string(res.Stderr)))
	}
	out := make(map[string]string)
	for _, line := range strings.Split(strings.TrimRight(string(res.Stdout), "\n"), "\n") {
		if line == "" {
			continue
		}
		fields := strings.SplitN(line, "  ", 2)
		if len(fields) != 2 {
			continue
		}
		hash, full := fields[0], fields[1]
		rel := strings.TrimPrefix(full, root+"/")
		out[rel] = hash
	}
	return out, nil
}

func equalStringSets(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	sa, sb := append([]string(nil), a...), append([]string(nil), b...)
	sort.Strings(sa)
	sort.Strings(sb)
	for i := range sa {
		if sa[i] != sb[i] {
			return false
		}
	}
	return true
}

func equalStringMaps(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// --- file_mode ---

type fileModeCheck struct {
	id, path, onFail string
	anyOf            []uint32
}

func newFileModeCheck(s Spec) (Check, error) {
	path, err := paramString(s.Params, "path", true)
	if err != nil {
		return nil, factoryError("file_mode", s.ID, err)
	}

	mode, hasMode, err := paramStringPresence(s.Params, "mode")
	if err != nil {
		return nil, factoryError("file_mode", s.ID, err)
	}
	anyOfRaw, err := paramStringSlice(s.Params, "any_of")
	if err != nil {
		return nil, factoryError("file_mode", s.ID, err)
	}

	var candidates []string
	switch {
	case len(anyOfRaw) > 0:
		candidates = anyOfRaw
	case hasMode:
		candidates = []string{mode}
	default:
		return nil, factoryError("file_mode", s.ID, fmt.Errorf("either %q or %q is required", "mode", "any_of"))
	}

	anyOf := make([]uint32, 0, len(candidates))
	for _, c := range candidates {
		v, err := strconv.ParseUint(c, 8, 32)
		if err != nil {
			return nil, factoryError("file_mode", s.ID, fmt.Errorf("mode %q is not a valid octal permission: %w", c, err))
		}
		anyOf = append(anyOf, uint32(v))
	}

	return &fileModeCheck{id: s.ID, path: path, onFail: s.OnFail, anyOf: anyOf}, nil
}

func (c *fileModeCheck) ID() string { return c.id }

func (c *fileModeCheck) Run(ctx context.Context, env Env) Result {
	start := time.Now()
	info, missing, err := statPath(ctx, env.Session, c.path)
	if err != nil {
		r := probeError(c.id, "check the mode of", c.path, err)
		r.Duration = time.Since(start)
		return r
	}
	if missing {
		return Result{ID: c.id, Status: StatusFail, Message: c.onFail, Duration: time.Since(start)}
	}
	for _, want := range c.anyOf {
		if info.Mode == want {
			return Result{ID: c.id, Status: StatusPass, Duration: time.Since(start)}
		}
	}
	return Result{ID: c.id, Status: StatusFail, Message: c.onFail, Duration: time.Since(start)}
}

// --- file_owner ---

type fileOwnerCheck struct {
	id, path, owner, group, onFail string
	checkGroup                     bool
}

func newFileOwnerCheck(s Spec) (Check, error) {
	path, err := paramString(s.Params, "path", true)
	if err != nil {
		return nil, factoryError("file_owner", s.ID, err)
	}
	owner, err := paramString(s.Params, "owner", true)
	if err != nil {
		return nil, factoryError("file_owner", s.ID, err)
	}
	group, hasGroup, err := paramStringPresence(s.Params, "group")
	if err != nil {
		return nil, factoryError("file_owner", s.ID, err)
	}
	return &fileOwnerCheck{id: s.ID, path: path, owner: owner, group: group, checkGroup: hasGroup, onFail: s.OnFail}, nil
}

func (c *fileOwnerCheck) ID() string { return c.id }

func (c *fileOwnerCheck) Run(ctx context.Context, env Env) Result {
	start := time.Now()
	info, missing, err := statPath(ctx, env.Session, c.path)
	if err != nil {
		r := probeError(c.id, "check the owner of", c.path, err)
		r.Duration = time.Since(start)
		return r
	}
	if missing {
		return Result{ID: c.id, Status: StatusFail, Message: c.onFail, Duration: time.Since(start)}
	}
	if info.Owner != c.owner {
		return Result{ID: c.id, Status: StatusFail, Message: c.onFail, Duration: time.Since(start)}
	}
	if c.checkGroup && info.Group != c.group {
		return Result{ID: c.id, Status: StatusFail, Message: c.onFail, Duration: time.Since(start)}
	}
	return Result{ID: c.id, Status: StatusPass, Duration: time.Since(start)}
}

// --- symlink_target ---

type symlinkTargetCheck struct {
	id, path, target, onFail string
}

func newSymlinkTargetCheck(s Spec) (Check, error) {
	path, err := paramString(s.Params, "path", true)
	if err != nil {
		return nil, factoryError("symlink_target", s.ID, err)
	}
	target, err := paramString(s.Params, "target", true)
	if err != nil {
		return nil, factoryError("symlink_target", s.ID, err)
	}
	return &symlinkTargetCheck{id: s.ID, path: path, target: target, onFail: s.OnFail}, nil
}

func (c *symlinkTargetCheck) ID() string { return c.id }

func (c *symlinkTargetCheck) Run(ctx context.Context, env Env) Result {
	start := time.Now()
	info, missing, err := statPath(ctx, env.Session, c.path)
	if err != nil {
		r := probeError(c.id, "check", c.path, err)
		r.Duration = time.Since(start)
		return r
	}
	if missing || info.Type != "symbolic link" {
		return Result{ID: c.id, Status: StatusFail, Message: c.onFail, Duration: time.Since(start)}
	}

	res, err := env.Session.Exec(ctx, []string{"readlink", c.path}, runtime.ExecOpts{})
	if err != nil {
		r := probeError(c.id, "read the link target of", c.path, err)
		r.Duration = time.Since(start)
		return r
	}
	if res.ExitCode != 0 {
		r := probeError(c.id, "read the link target of", c.path, fmt.Errorf("%s", strings.TrimSpace(string(res.Stderr))))
		r.Duration = time.Since(start)
		return r
	}

	actual := strings.TrimSpace(string(res.Stdout))
	if actual != c.target {
		return Result{ID: c.id, Status: StatusFail, Message: c.onFail, Duration: time.Since(start)}
	}
	return Result{ID: c.id, Status: StatusPass, Duration: time.Since(start)}
}
