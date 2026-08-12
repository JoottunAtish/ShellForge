package main

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
)

// TestCommandTableIsWellFormed keeps `shellforge help` honest. Every registered
// verb needs a summary and a usage line, and no verb may be registered twice.
func TestCommandTableIsWellFormed(t *testing.T) {
	seen := make(map[string]bool, len(commands))
	groups := make(map[string]bool, len(groupOrder))
	for _, g := range groupOrder {
		groups[g] = true
	}

	for _, c := range commands {
		if c.name == "" {
			t.Error("a command has an empty name")
			continue
		}
		if seen[c.name] {
			t.Errorf("command %q is registered more than once", c.name)
		}
		seen[c.name] = true

		if c.summary == "" {
			t.Errorf("command %q has no summary; it would print blank in help", c.name)
		}
		if c.usage == "" {
			t.Errorf("command %q has no usage line", c.name)
		}
		if c.run == nil {
			t.Errorf("command %q has a nil run function", c.name)
		}
		if !groups[c.group] {
			t.Errorf("command %q is in group %q, which is not in groupOrder so it would never print", c.name, c.group)
		}
		if !strings.HasPrefix(c.usage, "shellforge "+c.name) {
			t.Errorf("command %q usage should start with %q, got %q", c.name, "shellforge "+c.name, c.usage)
		}
	}
}

// TestEveryVerbFromTheBriefIsRegistered guards against a verb quietly
// disappearing during the build week. The list comes from the scope table in
// docs/design/PROJECT-BRIEF.md.
func TestEveryVerbFromTheBriefIsRegistered(t *testing.T) {
	required := []string{
		"doctor", "init", "play", "run", "check", "hint",
		"reset", "skip", "map", "stats", "sandbox", "author",
	}
	for _, name := range required {
		if _, ok := lookup(name); !ok {
			t.Errorf("verb %q from the project brief is not registered", name)
		}
	}
}

func TestLookupRejectsUnknown(t *testing.T) {
	if _, ok := lookup("definitely-not-a-command"); ok {
		t.Error("lookup accepted a command that does not exist")
	}
}

func TestVersionCommandPrintsBuildInfo(t *testing.T) {
	if err := cmdVersion(context.Background(), nil); err != nil {
		t.Fatalf("version command failed: %v", err)
	}
}

// TestUnimplementedVerbsExplainThemselves checks that a stub fails through the
// user-facing error path rather than printing a bare Go error.
func TestUnimplementedVerbsExplainThemselves(t *testing.T) {
	c, ok := lookup("play")
	if !ok {
		t.Fatal("play is not registered")
	}
	err := c.run(context.Background(), nil)
	if err == nil {
		t.Fatal("expected an error from an unimplemented verb")
	}
	if !strings.Contains(err.Error(), "not built yet") {
		t.Errorf("error should say the verb is not built yet, got: %v", err)
	}
}

func TestUsageListsEveryCommand(t *testing.T) {
	var buf bytes.Buffer
	printUsage(&buf)
	out := buf.String()

	for _, c := range commands {
		if !strings.Contains(out, c.name) {
			t.Errorf("usage output does not mention command %q", c.name)
		}
	}
	for _, g := range groupOrder {
		if !strings.Contains(out, g) {
			t.Errorf("usage output does not mention group %q", g)
		}
	}
}

func TestRunWithNoArgsPrintsUsageAndSucceeds(t *testing.T) {
	if err := run(context.Background(), nil); err != nil {
		t.Errorf("run with no args should print usage and succeed, got: %v", err)
	}
}

func TestRunRejectsUnknownCommandWithUsageError(t *testing.T) {
	err := run(context.Background(), []string{"nope"})
	if err == nil {
		t.Fatal("expected a usage error for an unknown command")
	}
	if !errors.Is(err, errUsage) {
		t.Errorf("expected errUsage, got %v", err)
	}
}
