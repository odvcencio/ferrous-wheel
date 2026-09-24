package ferrouswheel

import (
	"strings"
	"testing"
)

func TestRetryExhaustionReturnsLastError(t *testing.T) {
	got := runCheck(t, `package main

import "errors"
import "fmt"

var attempts int
var failure = errors.New("still failing")

func fail() error {
	attempts++
	return failure
}

func run() error {
	retry 2 delay 0 {
		err := fail()
		guard err == nil else return err
	}
	return nil
}

func main() {
	err := run()
	fmt.Println(attempts, errors.Is(err, failure), err)
}
`)
	if got != "2 true retry exhausted on attempt 2: still failing" {
		t.Fatalf("retry exhaustion: got %q", got)
	}
}

func TestRetryExhaustionInMainFailsVisibly(t *testing.T) {
	got := runCheckErrorWithFiles(t, `package main

import "errors"

func fail() error { return errors.New("permanent failure") }

func main() {
	retry 1 delay 0 {
		err := fail()
		guard err == nil else return err
	}
}
`, nil)
	if !strings.Contains(got, "retry exhausted on attempt 1: permanent failure") {
		t.Fatalf("missing retry failure in process output: %q", got)
	}
}

func TestRetryDoesNotWaitAfterLastAttempt(t *testing.T) {
	got := runCheck(t, `package main

import "errors"
import "fmt"
import "time"

func run() error {
	retry 1 delay 1000 {
		guard false else return errors.New("failed")
	}
	return nil
}

func main() {
	start := time.Now()
	err := run()
	fmt.Println(err != nil, time.Since(start) < 500*time.Millisecond)
}
`)
	if got != "true true" {
		t.Fatalf("retry waited after final failure: %q", got)
	}
}

func TestRetryZeroAttemptsFails(t *testing.T) {
	got := runCheck(t, `package main

import "fmt"

func run() error {
	retry 0 {
		panic("body must not run")
	}
	return nil
}

func main() {
	fmt.Println(run())
}
`)
	if got != "retry requires at least one attempt" {
		t.Fatalf("zero attempts: got %q", got)
	}
}

func TestRetryContextCancelsBackoff(t *testing.T) {
	got := runCheck(t, `package main

import "context"
import "errors"
import "fmt"
import "time"

var attempts int

func run(ctx context.Context) (int, error) {
	retry 3 delay 2000 context ctx {
		attempts++
		guard false else return errors.New("temporary failure")
	}
	return 99, nil
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	time.AfterFunc(30*time.Millisecond, cancel)
	start := time.Now()
	value, err := run(ctx)
	fmt.Println(attempts, value, errors.Is(err, context.Canceled), time.Since(start) < time.Second)
}
`)
	if got != "1 0 true true" {
		t.Fatalf("retry cancellation: got %q", got)
	}
}

func TestRetryContextChecksBeforeFirstAttempt(t *testing.T) {
	got := runCheck(t, `package main

import "context"
import "errors"
import "fmt"

func run(ctx context.Context) error {
	retry 2 context ctx {
		panic("body must not run")
	}
	return nil
}

func main() {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	fmt.Println(errors.Is(run(ctx), context.Canceled))
}
`)
	if got != "true" {
		t.Fatalf("pre-canceled retry: got %q", got)
	}
}

func TestRetryCountEvaluatedOnceAndBlocksCanRepeat(t *testing.T) {
	got := runCheck(t, `package main

import "fmt"

var evaluations int

func count() int {
	evaluations++
	return 1
}

func main() {
	attempts := 0
	retry count() {
		attempts++
	}
	retry 1 {
		attempts++
	}
	fmt.Println(evaluations, attempts)
}
`)
	if got != "1 2" {
		t.Fatalf("retry count or repeated blocks: got %q", got)
	}
}

func TestRetryInsideVoidLiteralUsesItsOwnReturnContract(t *testing.T) {
	got := runCheckErrorWithFiles(t, `package main

import "errors"

func outer() error {
	action := func() {
		retry 1 {
			guard false else return errors.New("literal failure")
		}
	}
	action()
	return nil
}

func main() { _ = outer() }
`, nil)
	if !strings.Contains(got, "retry exhausted on attempt 1: literal failure") {
		t.Fatalf("missing literal retry failure: %q", got)
	}
}

func TestNestedRetryPropagatesToOuterAttempt(t *testing.T) {
	got := runCheck(t, `package main

import "errors"
import "fmt"

var attempts int
var failure = errors.New("nested failure")

func run() error {
	retry 2 delay 0 {
		retry 1 delay 0 {
			attempts++
			guard false else return failure
		}
	}
	return nil
}

func main() {
	err := run()
	fmt.Println(attempts, errors.Is(err, failure))
}
`)
	if got != "2 true" {
		t.Fatalf("nested retry: got %q", got)
	}
}
