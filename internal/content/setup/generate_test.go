package setup

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"testing"
)

// TestLogLinesIsDeterministicForAFixedSeed pins criterion 3: two calls with
// the same seed and line count produce byte-identical output.
func TestLogLinesIsDeterministicForAFixedSeed(t *testing.T) {
	a, err := Generate("loglines", 91731, 4000)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	b, err := Generate("loglines", 91731, 4000)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	if sha256.Sum256(a) != sha256.Sum256(b) {
		t.Error("two calls with the same seed and line count produced different output")
	}
}

// TestLogLinesMatchesItsGoldenDigest pins the exact output with a hex
// constant, which is what makes a platform-dependent generator fail here
// rather than only showing up as a level whose asset file differs between a
// Linux and a Windows run of the same seed.
func TestLogLinesMatchesItsGoldenDigest(t *testing.T) {
	const wantDigest = "d85270dd2cd59ee9df90aecee02a1fb91bfcc8d4f397d273a7c45f5e3b06f50a"

	got, err := Generate("loglines", 91731, 4000)
	if err != nil {
		t.Fatalf("Generate: %v", err)
	}
	sum := sha256.Sum256(got)
	gotDigest := hex.EncodeToString(sum[:])
	if gotDigest != wantDigest {
		t.Errorf("sha256(loglines(91731, 4000)) = %s, want %s (pinned golden digest)", gotDigest, wantDigest)
	}
}

// TestGeneratorsUseNoClockAndNoFilesystem parses generate.go and fails on an
// import of os, net, net/http, or math/rand/v2, and on any call to
// time.Now. Importing "time" itself is fine and expected: a generator may
// use a fixed time.Date stamp, which is deterministic, but never read the
// wall clock. It also asserts loglines is registered under exactly that
// name, and that an unknown kind refuses through ErrInvalidLevelPack.
func TestGeneratorsUseNoClockAndNoFilesystem(t *testing.T) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "generate.go", nil, parser.AllErrors)
	if err != nil {
		t.Fatalf("parse generate.go: %v", err)
	}

	bannedImports := map[string]bool{
		"os": true, "net": true, "net/http": true, "math/rand/v2": true,
	}
	for _, imp := range f.Imports {
		path := imp.Path.Value
		for banned := range bannedImports {
			if path == `"`+banned+`"` {
				t.Errorf("generate.go imports %s, which reads the clock, the filesystem, or the network", path)
			}
		}
	}

	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		pkgIdent, ok := sel.X.(*ast.Ident)
		if !ok {
			return true
		}
		if pkgIdent.Name == "time" && sel.Sel.Name == "Now" {
			t.Error("generate.go calls time.Now, which breaks determinism across runs")
		}
		return true
	})

	if _, err := Generate("loglines", 1, 1); err != nil {
		t.Errorf("Generate(%q, ...): %v, want the kind registered", "loglines", err)
	}
	if _, err := Generate("no-such-kind", 1, 1); err == nil {
		t.Error("Generate with an unregistered kind: want an error")
	} else if !errors.Is(err, ErrInvalidLevelPack) {
		t.Errorf("Generate with an unregistered kind: err = %v, want it to wrap ErrInvalidLevelPack", err)
	}
}

// TestLogLinesRefusesAbsurdLineCounts pins the resource-limit refusal:
// loglines refuses rather than truncates.
func TestLogLinesRefusesAbsurdLineCounts(t *testing.T) {
	for _, lines := range []int{0, -1, 200001} {
		if _, err := Generate("loglines", 91731, lines); err == nil {
			t.Errorf("Generate(loglines, _, %d): want an error", lines)
		}
	}
	if _, err := Generate("loglines", 91731, 200000); err != nil {
		t.Errorf("Generate(loglines, _, 200000): %v, want the upper bound accepted", err)
	}
}
