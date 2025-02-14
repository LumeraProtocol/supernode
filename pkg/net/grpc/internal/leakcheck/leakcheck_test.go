package leakcheck

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

type testLogger struct {
	errorCount int
	errors     []string
}

func (e *testLogger) Logf(string, ...any) {
}

func (e *testLogger) Errorf(format string, args ...any) {
	e.errors = append(e.errors, fmt.Sprintf(format, args...))
	e.errorCount++
}

func TestCheck(t *testing.T) {
	const leakCount = 3
	for i := 0; i < leakCount; i++ {
		go func() { time.Sleep(2 * time.Second) }()
	}
	if ig := interestingGoroutines(); len(ig) == 0 {
		t.Error("blah")
	}
	e := &testLogger{}
	CheckGoroutines(e, time.Second)
	if e.errorCount != leakCount {
		t.Errorf("CheckGoroutines found %v leaks, want %v leaks", e.errorCount, leakCount)
		t.Logf("leaked goroutines:\n%v", strings.Join(e.errors, "\n"))
	}
	CheckGoroutines(t, 3*time.Second)
}

func ignoredTestingLeak(d time.Duration) {
	time.Sleep(d)
}

func TestCheckRegisterIgnore(t *testing.T) {
	RegisterIgnoreGoroutine("ignoredTestingLeak")
	const leakCount = 3
	for i := 0; i < leakCount; i++ {
		go func() { time.Sleep(2 * time.Second) }()
	}
	go func() { ignoredTestingLeak(3 * time.Second) }()
	if ig := interestingGoroutines(); len(ig) == 0 {
		t.Error("blah")
	}
	e := &testLogger{}
	CheckGoroutines(e, time.Second)
	if e.errorCount != leakCount {
		t.Errorf("CheckGoroutines found %v leaks, want %v leaks", e.errorCount, leakCount)
		t.Logf("leaked goroutines:\n%v", strings.Join(e.errors, "\n"))
	}
	CheckGoroutines(t, 3*time.Second)
}
