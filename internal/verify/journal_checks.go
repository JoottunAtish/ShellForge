package verify

import (
	"context"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

func init() {
	Register("command_matched", newCommandMatchedCheck, "pattern", "scope")
	Register("command_not_matched", newCommandNotMatchedCheck, "pattern", "scope")
}

// journalCheck backs both command_matched and command_not_matched. Only
// wantMatch differs: command_matched passes when a command in scope matches
// pattern, command_not_matched passes when none does.
type journalCheck struct {
	id, onFail string
	pattern    *regexp.Regexp
	scope      Scope
	wantMatch  bool
}

func newJournalCheck(typeName string, wantMatch bool, s Spec) (Check, error) {
	patternRaw, err := paramString(s.Params, "pattern", true)
	if err != nil {
		return nil, factoryError(typeName, s.ID, err)
	}
	scopeRaw, err := paramString(s.Params, "scope", false)
	if err != nil {
		return nil, factoryError(typeName, s.ID, err)
	}
	re, err := regexp.Compile(patternRaw)
	if err != nil {
		return nil, factoryError(typeName, s.ID, fmt.Errorf("param %q is not a valid regex: %w", "pattern", err))
	}
	scope, err := parseScope(scopeRaw)
	if err != nil {
		return nil, factoryError(typeName, s.ID, err)
	}
	return &journalCheck{id: s.ID, onFail: s.OnFail, pattern: re, scope: scope, wantMatch: wantMatch}, nil
}

func newCommandMatchedCheck(s Spec) (Check, error) {
	return newJournalCheck("command_matched", true, s)
}

func newCommandNotMatchedCheck(s Spec) (Check, error) {
	return newJournalCheck("command_not_matched", false, s)
}

func (c *journalCheck) ID() string { return c.id }

func (c *journalCheck) Run(ctx context.Context, env Env) Result {
	start := time.Now()
	commands := env.Journal.Commands(c.scope)

	matched := false
	for _, cmd := range commands {
		if c.pattern.MatchString(cmd) {
			matched = true
			break
		}
	}

	pass := matched == c.wantMatch
	if pass {
		return Result{ID: c.id, Status: StatusPass, Duration: time.Since(start)}
	}
	return Result{ID: c.id, Status: StatusFail, Message: c.onFail, Duration: time.Since(start)}
}

// parseScope parses the level YAML's scope string into a Scope. The empty
// string, which paramString returns for an omitted optional param, means
// "level", the default per the check catalogue.
func parseScope(raw string) (Scope, error) {
	switch {
	case raw == "" || raw == "level":
		return Scope{Kind: ScopeLevel}, nil
	case raw == "last":
		return Scope{Kind: ScopeLast}, nil
	case strings.HasPrefix(raw, "last_n:"):
		n, err := strconv.Atoi(strings.TrimPrefix(raw, "last_n:"))
		if err != nil || n <= 0 {
			return Scope{}, fmt.Errorf("param %q value %q: last_n must be followed by a positive integer", "scope", raw)
		}
		return Scope{Kind: ScopeLastN, N: n}, nil
	default:
		return Scope{}, fmt.Errorf("param %q value %q is not one of level, last, or last_n:N", "scope", raw)
	}
}
