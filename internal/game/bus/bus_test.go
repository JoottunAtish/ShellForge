package bus

import (
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestPublishDeliversToEverySubscriberInSubscriptionOrder is criterion 1: a
// single Publish call fans out to every subscriber, in the order each one
// subscribed, and has done so completely by the time Publish returns.
func TestPublishDeliversToEverySubscriberInSubscriptionOrder(t *testing.T) {
	b := New()
	var order []string

	b.Subscribe("a", func(context.Context, Event) { order = append(order, "a") })
	b.Subscribe("b", func(context.Context, Event) { order = append(order, "b") })
	b.Subscribe("c", func(context.Context, Event) { order = append(order, "c") })

	b.Publish(context.Background(), LevelStarted{At: time.Now()})

	want := []string{"a", "b", "c"}
	if len(order) != len(want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}
}

// TestUnsubscribeStopsDeliveryAndIsSafeToCallInEveryPosition is criterion 2.
func TestUnsubscribeStopsDeliveryAndIsSafeToCallInEveryPosition(t *testing.T) {
	t.Run("outside", func(t *testing.T) {
		b := New()
		count := 0
		unsub := b.Subscribe("a", func(context.Context, Event) { count++ })
		unsub()
		b.Publish(context.Background(), LevelReset{At: time.Now()})
		if count != 0 {
			t.Fatalf("count = %d, want 0", count)
		}
	})

	t.Run("twice", func(t *testing.T) {
		b := New()
		count := 0
		unsub := b.Subscribe("a", func(context.Context, Event) { count++ })
		unsub()
		unsub() // must not panic
		b.Publish(context.Background(), LevelReset{At: time.Now()})
		if count != 0 {
			t.Fatalf("count = %d, want 0", count)
		}
	})

	t.Run("inside-for-self", func(t *testing.T) {
		b := New()
		count := 0
		var unsub func()
		unsub = b.Subscribe("a", func(context.Context, Event) {
			count++
			unsub()
		})
		b.Publish(context.Background(), LevelReset{At: time.Now()})
		b.Publish(context.Background(), LevelReset{At: time.Now()})
		if count != 1 {
			t.Fatalf("count = %d, want 1", count)
		}
	})

	t.Run("inside-for-later", func(t *testing.T) {
		b := New()
		countB := 0
		var unsubB func()
		b.Subscribe("a", func(context.Context, Event) {
			unsubB()
		})
		unsubB = b.Subscribe("b", func(context.Context, Event) { countB++ })

		b.Publish(context.Background(), LevelReset{At: time.Now()})
		if countB != 1 {
			t.Fatalf("countB after first publish = %d, want 1 (delivered against the snapshot taken before a unsubscribed b)", countB)
		}

		b.Publish(context.Background(), LevelReset{At: time.Now()})
		if countB != 1 {
			t.Fatalf("countB after second publish = %d, want 1 (b must be gone by the next event)", countB)
		}
	})
}

// TestPanickingSubscriberIsContainedAndReportedWithoutLeakingPayload is
// criterion 3.
func TestPanickingSubscriberIsContainedAndReportedWithoutLeakingPayload(t *testing.T) {
	var mu sync.Mutex
	var gotName string
	var gotRecovered any
	var gotStack []byte
	calls := 0

	b := New(WithErrorHandler(func(name string, recovered any, stack []byte) {
		mu.Lock()
		defer mu.Unlock()
		calls++
		gotName = name
		gotRecovered = recovered
		gotStack = stack
	}))

	ranA, ranC := false, false
	b.Subscribe("A", func(context.Context, Event) { ranA = true })
	b.Subscribe("B", func(context.Context, Event) { panic("deliberate test panic") })
	b.Subscribe("C", func(context.Context, Event) { ranC = true })

	b.Publish(context.Background(), CommandExecuted{Raw: "SECRET-TOKEN", At: time.Now()})

	if !ranA || !ranC {
		t.Fatalf("ranA=%v ranC=%v, want both true: a panic in B must not stop the fan-out", ranA, ranC)
	}

	mu.Lock()
	defer mu.Unlock()
	if calls != 1 {
		t.Fatalf("error handler called %d times, want exactly 1", calls)
	}
	if gotName != "B" {
		t.Fatalf("reported subscriber name = %q, want %q", gotName, "B")
	}
	if gotRecovered == nil {
		t.Fatal("recovered value is nil, want the panic value")
	}
	if len(gotStack) == 0 {
		t.Fatal("stack is empty, want a non-empty stack trace")
	}
	if strings.Contains(gotName, "SECRET-TOKEN") ||
		strings.Contains(sprintAny(gotRecovered), "SECRET-TOKEN") ||
		strings.Contains(string(gotStack), "SECRET-TOKEN") {
		t.Fatal("the error report leaks CommandExecuted.Raw, which is secret material")
	}
}

func sprintAny(v any) string {
	if err, ok := v.(error); ok {
		return err.Error()
	}
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// TestNestedPublishDeliveredAfterCurrentFanOutInOrder is criterion 4a.
func TestNestedPublishDeliveredAfterCurrentFanOutInOrder(t *testing.T) {
	b := New()
	var mu sync.Mutex
	var log []string

	record := func(suffix string) Handler {
		return func(_ context.Context, ev Event) {
			mu.Lock()
			log = append(log, string(ev.Kind())+"@"+suffix)
			mu.Unlock()
		}
	}

	b.Subscribe("R", record("R"))
	b.Subscribe("P", func(ctx context.Context, ev Event) {
		mu.Lock()
		log = append(log, string(ev.Kind())+"@P")
		mu.Unlock()
		if ev.Kind() == KindLevelPassed {
			b.Publish(ctx, AchievementUnlocked{Key: "first-pass", At: time.Now()})
		}
	})

	done := make(chan struct{})
	go func() {
		b.Publish(context.Background(), LevelPassed{At: time.Now()})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish deadlocked")
	}

	mu.Lock()
	defer mu.Unlock()
	want := []string{
		"level_passed@R",
		"level_passed@P",
		"achievement_unlocked@R",
		"achievement_unlocked@P",
	}
	if len(log) != len(want) {
		t.Fatalf("log = %v, want %v", log, want)
	}
	for i := range want {
		if log[i] != want[i] {
			t.Fatalf("log = %v, want %v", log, want)
		}
	}
}

// TestRepublishForeverIsBoundedAndReported is criterion 4b.
func TestRepublishForeverIsBoundedAndReported(t *testing.T) {
	var mu sync.Mutex
	var errName string
	var errRecovered any
	errCalls := 0

	b := New(WithErrorHandler(func(name string, recovered any, stack []byte) {
		mu.Lock()
		defer mu.Unlock()
		errCalls++
		errName = name
		errRecovered = recovered
		_ = stack
	}))

	counter := 0
	var counterMu sync.Mutex
	b.Subscribe("H", func(ctx context.Context, ev Event) {
		counterMu.Lock()
		counter++
		counterMu.Unlock()
		b.Publish(ctx, LevelReset{At: time.Now()})
	})

	done := make(chan struct{})
	go func() {
		b.Publish(context.Background(), LevelReset{At: time.Now()})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish deadlocked (or never bounded)")
	}

	counterMu.Lock()
	gotCounter := counter
	counterMu.Unlock()
	if gotCounter != maxDispatchDepth {
		t.Fatalf("H ran %d times, want exactly %d", gotCounter, maxDispatchDepth)
	}

	mu.Lock()
	defer mu.Unlock()
	if errCalls != 1 {
		t.Fatalf("error handler called %d times, want exactly 1", errCalls)
	}
	if errName != overflowSubscriber {
		t.Fatalf("reported name = %q, want %q", errName, overflowSubscriber)
	}
	err, ok := errRecovered.(error)
	if !ok {
		t.Fatalf("recovered value %v is not an error", errRecovered)
	}
	if !errors.Is(err, ErrMaxDispatchDepthExceeded) {
		t.Fatalf("recovered error = %v, want errors.Is match for ErrMaxDispatchDepthExceeded", err)
	}
}

// TestPublishWithNoSubscribersAllocatesNothingAndStartsNoGoroutine is
// criterion 5.
func TestPublishWithNoSubscribersAllocatesNothingAndStartsNoGoroutine(t *testing.T) {
	b := New()
	ev := Event(LevelReset{At: time.Now()})
	ctx := context.Background()

	before := runtime.NumGoroutine()

	allocs := testing.AllocsPerRun(100, func() {
		b.Publish(ctx, ev)
	})

	after := runtime.NumGoroutine()

	if allocs != 0 {
		t.Fatalf("Publish with no subscribers allocated %v times per run, want 0", allocs)
	}
	if before != after {
		t.Fatalf("goroutine count changed from %d to %d, want no goroutine started", before, after)
	}
}

// TestEveryKindHasExactlyOneEventAndEveryEventReturnsItsKind is criterion 6.
// It parses the package's own non-test source with go/parser so the
// completeness assertion never becomes a hand-maintained list that silently
// drifts from event.go.
func TestEveryKindHasExactlyOneEventAndEveryEventReturnsItsKind(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("ParseDir: %v", err)
	}

	kindConstants := map[string]string{}  // const name -> unquoted value
	kindReturnedBy := map[string]string{} // type name -> const name returned by its Kind()
	hasKindMethod := map[string]bool{}
	hasKindCloser := map[string]bool{}

	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			ast.Inspect(file, func(n ast.Node) bool {
				switch decl := n.(type) {
				case *ast.GenDecl:
					if decl.Tok != token.CONST {
						return true
					}
					for _, spec := range decl.Specs {
						vs, ok := spec.(*ast.ValueSpec)
						if !ok {
							continue
						}
						typeIdent, ok := vs.Type.(*ast.Ident)
						if !ok || typeIdent.Name != "Kind" {
							continue
						}
						for i, name := range vs.Names {
							if i >= len(vs.Values) {
								continue
							}
							lit, ok := vs.Values[i].(*ast.BasicLit)
							if !ok || lit.Kind != token.STRING {
								continue
							}
							value, err := strconv.Unquote(lit.Value)
							if err != nil {
								continue
							}
							kindConstants[name.Name] = value
						}
					}
				case *ast.FuncDecl:
					if decl.Recv == nil || len(decl.Recv.List) != 1 {
						return true
					}
					recvType := receiverTypeName(decl.Recv.List[0].Type)
					switch decl.Name.Name {
					case "Kind":
						hasKindMethod[recvType] = true
						if decl.Body == nil || len(decl.Body.List) != 1 {
							return true
						}
						ret, ok := decl.Body.List[0].(*ast.ReturnStmt)
						if !ok || len(ret.Results) != 1 {
							return true
						}
						if ident, ok := ret.Results[0].(*ast.Ident); ok {
							kindReturnedBy[recvType] = ident.Name
						}
					case "kind":
						hasKindCloser[recvType] = true
					}
				}
				return true
			})
		}
	}

	if len(kindConstants) != 7 {
		t.Fatalf("found %d Kind constants, want exactly 7: %v", len(kindConstants), kindConstants)
	}

	if len(hasKindMethod) != len(hasKindCloser) {
		t.Fatalf("%d types have Kind() but %d types have kind(): the sets must be equal", len(hasKindMethod), len(hasKindCloser))
	}
	for typeName := range hasKindMethod {
		if !hasKindCloser[typeName] {
			t.Fatalf("type %s has Kind() but no kind(): it is not a closed Event implementation", typeName)
		}
	}
	for typeName := range hasKindCloser {
		if !hasKindMethod[typeName] {
			t.Fatalf("type %s has kind() but no Kind(): it is not a usable Event implementation", typeName)
		}
	}

	if len(kindReturnedBy) != len(kindConstants) {
		t.Fatalf("%d types return a Kind constant, want %d (one per type)", len(kindReturnedBy), len(kindConstants))
	}

	seenConstants := map[string]string{} // const name -> the one type that returns it
	for typeName, constName := range kindReturnedBy {
		if _, exists := kindConstants[constName]; !exists {
			t.Fatalf("type %s.Kind() returns %s, which is not a declared Kind constant", typeName, constName)
		}
		if other, dup := seenConstants[constName]; dup {
			t.Fatalf("both %s and %s return the constant %s: not a bijection", other, typeName, constName)
		}
		seenConstants[constName] = typeName
	}
	for constName := range kindConstants {
		if _, ok := seenConstants[constName]; !ok {
			t.Fatalf("constant %s is never returned by any type's Kind()", constName)
		}
	}
}

func receiverTypeName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return receiverTypeName(t.X)
	default:
		return ""
	}
}

// TestConcurrentSubscribePublishUnsubscribeIsRaceClean is criterion 7. The
// oracle is go test -race, run in make race and CI; this test asserts
// termination, not a specific delivery count.
func TestConcurrentSubscribePublishUnsubscribeIsRaceClean(t *testing.T) {
	b := New(WithErrorHandler(func(string, any, []byte) {}))

	const iterations = 200
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			count := 0
			unsub := b.Subscribe("churn", func(context.Context, Event) { count++ })
			unsub()
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			b.Publish(context.Background(), LevelReset{At: time.Now()})
		}
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			b.Publish(context.Background(), CheckRun{At: time.Now()})
		}
	}()

	longLived := 0
	unsubLong := b.Subscribe("long-lived", func(context.Context, Event) { longLived++ })
	defer unsubLong()

	wg.Wait()
}

// TestEventWhenReturnsAt is the supplementary table test: a deliberate hand
// list over the seven event structs, spot-checking When() rather than
// completeness.
func TestEventWhenReturnsAt(t *testing.T) {
	at := time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name string
		ev   Event
	}{
		{"LevelStarted", LevelStarted{At: at}},
		{"CommandExecuted", CommandExecuted{At: at}},
		{"CheckRun", CheckRun{At: at}},
		{"HintTaken", HintTaken{At: at}},
		{"LevelPassed", LevelPassed{At: at}},
		{"LevelReset", LevelReset{At: at}},
		{"AchievementUnlocked", AchievementUnlocked{At: at}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !tt.ev.When().Equal(at) {
				t.Fatalf("When() = %v, want %v", tt.ev.When(), at)
			}
		})
	}
}
