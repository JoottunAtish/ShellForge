package setup

import (
	"bytes"
	"fmt"
	"math/rand"
	"sync"
	"time"
)

// Generator produces deterministic content for a fixed seed and line count.
// It must read no clock, no filesystem, and no network: the same seed must
// produce byte-identical output on every machine, since setup must be
// deterministic and offline.
type Generator func(seed int64, lines int) ([]byte, error)

var (
	generatorsMu sync.RWMutex
	generators   = map[string]Generator{}
)

// RegisterGenerator adds a named generator to the package-level registry.
// It panics on an empty kind or a duplicate registration: both are package
// initialization bugs, the sanctioned panic-outside-main case in the
// go-style skill, matching database/sql's own driver registry.
func RegisterGenerator(kind string, g Generator) {
	if kind == "" {
		panic("setup: RegisterGenerator called with an empty kind")
	}
	generatorsMu.Lock()
	defer generatorsMu.Unlock()
	if _, exists := generators[kind]; exists {
		panic(fmt.Sprintf("setup: generator kind %q already registered", kind))
	}
	generators[kind] = g
}

// Generate looks up kind in the registry and calls it. An unregistered kind
// wraps ErrInvalidLevelPack, since it is a pack authoring mistake, not a
// generator failure.
func Generate(kind string, seed int64, lines int) ([]byte, error) {
	generatorsMu.RLock()
	g, ok := generators[kind]
	generatorsMu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("generator kind %q is not registered: %w", kind, ErrInvalidLevelPack)
	}
	data, err := g(seed, lines)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidLevelPack, err)
	}
	return data, nil
}

func init() {
	RegisterGenerator("loglines", loglines)
}

// Bounds on lines, refused rather than truncated: staging happens in host
// memory, so an unbounded generator is a host memory problem driven by pack
// data.
const (
	minLogLines = 1
	maxLogLines = 200000
)

var logLevels = []string{"INFO", "DEBUG", "WARN"}

var logServices = []string{"api", "worker", "billing"}

var logMessages = []string{
	"request completed",
	"cache hit for key",
	"connection pool resized",
	"retry scheduled",
	"heartbeat acknowledged",
	"config reloaded from disk",
	"queue depth check",
	"session token refreshed",
	"batch flushed",
	"health probe ok",
}

// loglines generates deterministic, plausible-looking log lines: a
// timestamp, a service name, a level, and a message, one per line.
//
// #nosec G404 -- this is a deterministic content generator, not a security
// decision. A cryptographic source would defeat the point: the same seed
// must produce byte-identical output on every machine. rand.New with an
// explicit rand.NewSource is documented stable across Go versions and
// platforms, unlike the top-level math/rand functions.
func loglines(seed int64, lines int) ([]byte, error) {
	if lines < minLogLines || lines > maxLogLines {
		return nil, fmt.Errorf("loglines: lines must be between %d and %d, got %d", minLogLines, maxLogLines, lines)
	}

	rng := rand.New(rand.NewSource(seed))

	// A fixed wall-clock start, never time.Now: no learner and no test ever
	// sees a timestamp that depends on when setup ran.
	stamp := time.Date(2026, time.March, 14, 2, 0, 0, 0, time.UTC)

	var buf bytes.Buffer
	for i := 0; i < lines; i++ {
		stamp = stamp.Add(time.Duration(1+rng.Intn(20)) * time.Second)
		level := logLevels[rng.Intn(len(logLevels))]
		service := logServices[rng.Intn(len(logServices))]
		message := logMessages[rng.Intn(len(logMessages))]
		fmt.Fprintf(&buf, "%s %s %s %s\n", stamp.Format("2006-01-02T15:04:05Z"), service, level, message)
	}
	return buf.Bytes(), nil
}
