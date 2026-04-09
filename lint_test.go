package ferrouswheel

import (
	"testing"
)

func TestLintUnusedLet(t *testing.T) {
	source := []byte(`package main

func main() {
	let x = 1
}
`)
	diags, err := Lint(source)
	if err != nil {
		t.Fatalf("lint: %v", err)
	}
	found := false
	for _, d := range diags {
		if d.Rule == "unused-let" {
			found = true
		}
	}
	if !found {
		t.Error("expected unused-let diagnostic")
	}
}

func TestLintUnusedLetUsed(t *testing.T) {
	source := []byte(`package main

import "fmt"

func main() {
	let x = 1
	fmt.Println(x)
}
`)
	diags, err := Lint(source)
	if err != nil {
		t.Fatalf("lint: %v", err)
	}
	for _, d := range diags {
		if d.Rule == "unused-let" {
			t.Error("should not flag used let binding")
		}
	}
}

func TestLintEmptyMatch(t *testing.T) {
	source := []byte(`package main

func main() {
	let x = match v {}
	_ = x
}
`)
	diags, err := Lint(source)
	if err != nil {
		t.Fatalf("lint: %v", err)
	}
	found := false
	for _, d := range diags {
		if d.Rule == "empty-match" {
			found = true
		}
	}
	if !found {
		t.Error("expected empty-match diagnostic")
	}
}

func TestLintUnusedMut(t *testing.T) {
	source := []byte(`package main

func main() {
	let mut x = 1
	_ = x
}
`)
	diags, err := Lint(source)
	if err != nil {
		t.Fatalf("lint: %v", err)
	}
	found := false
	for _, d := range diags {
		if d.Rule == "unused-mut" {
			found = true
		}
	}
	if !found {
		t.Error("expected unused-mut diagnostic (x is never reassigned)")
	}
}

func TestLintUnusedMutReassigned(t *testing.T) {
	source := []byte(`package main

func main() {
	let mut x = 1
	x = 2
	_ = x
}
`)
	diags, err := Lint(source)
	if err != nil {
		t.Fatalf("lint: %v", err)
	}
	for _, d := range diags {
		if d.Rule == "unused-mut" {
			t.Error("should not flag reassigned mut binding")
		}
	}
}

// ---------- unreachable-match-arm tests ----------

func TestLintUnreachableMatchArm(t *testing.T) {
	source := []byte(`package main

func main() {
	let x = match v {
		_ => "default",
		1 => "one",
	}
	_ = x
}
`)
	diags, err := Lint(source)
	if err != nil {
		t.Fatalf("lint: %v", err)
	}
	found := false
	for _, d := range diags {
		if d.Rule == "unreachable-match-arm" {
			found = true
		}
	}
	if !found {
		t.Error("expected unreachable-match-arm diagnostic")
	}
}

func TestLintUnreachableMatchArmNegative(t *testing.T) {
	source := []byte(`package main

func main() {
	let x = match v {
		1 => "one",
		_ => "default",
	}
	_ = x
}
`)
	diags, err := Lint(source)
	if err != nil {
		t.Fatalf("lint: %v", err)
	}
	for _, d := range diags {
		if d.Rule == "unreachable-match-arm" {
			t.Error("should not flag arms before wildcard")
		}
	}
}

// ---------- empty-block tests ----------

func TestLintEmptyBlock(t *testing.T) {
	source := []byte(`package main

func main() {
	if true {}
}
`)
	diags, err := Lint(source)
	if err != nil {
		t.Fatalf("lint: %v", err)
	}
	found := false
	for _, d := range diags {
		if d.Rule == "empty-block" {
			found = true
		}
	}
	if !found {
		t.Error("expected empty-block diagnostic")
	}
}

func TestLintEmptyBlockNegative(t *testing.T) {
	source := []byte(`package main

func main() {
	if true {
		let x = 1
		_ = x
	}
}
`)
	diags, err := Lint(source)
	if err != nil {
		t.Fatalf("lint: %v", err)
	}
	for _, d := range diags {
		if d.Rule == "empty-block" {
			t.Error("should not flag non-empty block")
		}
	}
}

// ---------- shadowed-let tests ----------

func TestLintShadowedLet(t *testing.T) {
	source := []byte(`package main

func main() {
	let x = 1
	if true {
		let x = 2
		_ = x
	}
	_ = x
}
`)
	diags, err := Lint(source)
	if err != nil {
		t.Fatalf("lint: %v", err)
	}
	found := false
	for _, d := range diags {
		if d.Rule == "shadowed-let" {
			found = true
		}
	}
	if !found {
		t.Error("expected shadowed-let diagnostic")
	}
}

func TestLintShadowedLetNegative(t *testing.T) {
	source := []byte(`package main

func main() {
	let x = 1
	let y = 2
	_ = x
	_ = y
}
`)
	diags, err := Lint(source)
	if err != nil {
		t.Fatalf("lint: %v", err)
	}
	for _, d := range diags {
		if d.Rule == "shadowed-let" {
			t.Error("should not flag non-shadowing let bindings")
		}
	}
}

// ---------- unreachable-after-guard tests ----------

func TestLintUnreachableAfterGuard(t *testing.T) {
	source := []byte(`package main

func main() {
	guard true else return
	_ = 1
}
`)
	diags, err := Lint(source)
	if err != nil {
		t.Fatalf("lint: %v", err)
	}
	// guard true else return: the guard passes (condition is true), so _ = 1 is reachable.
	for _, d := range diags {
		if d.Rule == "unreachable-after-guard" {
			t.Error("should not flag reachable code after guard with true condition")
		}
	}
}

func TestLintUnreachableAfterGuardFalse(t *testing.T) {
	source := []byte(`package main

func main() {
	guard false else { return }
	_ = 1
}
`)
	diags, err := Lint(source)
	if err != nil {
		t.Fatalf("lint: %v", err)
	}
	found := false
	for _, d := range diags {
		if d.Rule == "unreachable-after-guard" {
			found = true
		}
	}
	if !found {
		t.Error("expected unreachable-after-guard diagnostic for guard false else { return }")
	}
}

// ---------- missing-else-if-expr tests ----------

func TestLintMissingElseIfExpr(t *testing.T) {
	source := []byte(`package main

func main() {
	let x = if true { 1 }
	_ = x
}
`)
	diags, err := Lint(source)
	if err != nil {
		t.Fatalf("lint: %v", err)
	}
	found := false
	for _, d := range diags {
		if d.Rule == "missing-else-if-expr" {
			found = true
		}
	}
	if !found {
		t.Error("expected missing-else-if-expr diagnostic")
	}
}

func TestLintMissingElseIfExprNegative(t *testing.T) {
	source := []byte(`package main

func main() {
	let x = if true { 1 } else { 2 }
	_ = x
}
`)
	diags, err := Lint(source)
	if err != nil {
		t.Fatalf("lint: %v", err)
	}
	for _, d := range diags {
		if d.Rule == "missing-else-if-expr" {
			t.Error("should not flag if-expression with else branch")
		}
	}
}

// ---------- LintSeverity.String tests ----------

func TestLintSeverityString(t *testing.T) {
	tests := []struct {
		sev  LintSeverity
		want string
	}{
		{LintError, "error"},
		{LintWarning, "warning"},
		{LintInfo, "info"},
	}
	for _, tt := range tests {
		if got := tt.sev.String(); got != tt.want {
			t.Errorf("LintSeverity(%d).String() = %q, want %q", tt.sev, got, tt.want)
		}
	}
}

// ---------- bare-error tests ----------

func TestLintBareError(t *testing.T) {
	source := []byte(`package main
func f() {
	error "something failed"
}
`)
	diags, err := Lint(source)
	if err != nil {
		t.Fatalf("lint: %v", err)
	}
	found := false
	for _, d := range diags {
		if d.Rule == "bare-error" {
			found = true
		}
	}
	if !found {
		t.Error("expected bare-error diagnostic")
	}
}

func TestLintBareErrorWithAttrs(t *testing.T) {
	source := []byte(`package main
func f() {
	error "something failed", err: e
}
`)
	diags, err := Lint(source)
	if err != nil {
		t.Fatalf("lint: %v", err)
	}
	for _, d := range diags {
		if d.Rule == "bare-error" {
			t.Error("should not flag error with attrs")
		}
	}
}

// ---------- bare-fatal tests ----------

func TestLintBareFatal(t *testing.T) {
	source := []byte(`package main
func f() {
	fatal "crash"
}
`)
	diags, err := Lint(source)
	if err != nil {
		t.Fatalf("lint: %v", err)
	}
	found := false
	for _, d := range diags {
		if d.Rule == "bare-fatal" {
			found = true
		}
	}
	if !found {
		t.Error("expected bare-fatal diagnostic")
	}
}

// ---------- debug-no-attrs tests ----------

func TestLintDebugNoAttrs(t *testing.T) {
	source := []byte(`package main
func f() {
	debug "checking"
}
`)
	diags, err := Lint(source)
	if err != nil {
		t.Fatalf("lint: %v", err)
	}
	found := false
	for _, d := range diags {
		if d.Rule == "debug-no-attrs" {
			found = true
		}
	}
	if !found {
		t.Error("expected debug-no-attrs diagnostic")
	}
}

// ---------- time-empty-block tests ----------

func TestLintTimeEmptyBlock(t *testing.T) {
	source := []byte(`package main
func f() {
	time "noop" {}
}
`)
	diags, err := Lint(source)
	if err != nil {
		t.Fatalf("lint: %v", err)
	}
	found := false
	for _, d := range diags {
		if d.Rule == "time-empty-block" {
			found = true
		}
	}
	if !found {
		t.Error("expected time-empty-block diagnostic")
	}
}

// ---------- with-no-logs tests ----------

func TestLintWithNoLogs(t *testing.T) {
	source := []byte(`package main
func f() {
	with service: "api" {
		doWork()
	}
}
`)
	diags, err := Lint(source)
	if err != nil {
		t.Fatalf("lint: %v", err)
	}
	found := false
	for _, d := range diags {
		if d.Rule == "with-no-logs" {
			found = true
		}
	}
	if !found {
		t.Error("expected with-no-logs diagnostic")
	}
}

// ---------- with-single-log tests ----------

func TestLintWithSingleLog(t *testing.T) {
	source := []byte(`package main
func f() {
	with service: "api" {
		info "boot", service: "api"
	}
}
`)
	diags, err := Lint(source)
	if err != nil {
		t.Fatalf("lint: %v", err)
	}
	found := false
	for _, d := range diags {
		if d.Rule == "with-single-log" {
			found = true
		}
	}
	if !found {
		t.Error("expected with-single-log diagnostic")
	}
}

// ---------- nested-time-same-name tests ----------

func TestLintNestedTimeSameName(t *testing.T) {
	source := []byte(`package main
func f() {
	time "op" {
		time "op" {
			work()
		}
	}
}
`)
	diags, err := Lint(source)
	if err != nil {
		t.Fatalf("lint: %v", err)
	}
	found := false
	for _, d := range diags {
		if d.Rule == "nested-time-same-name" {
			found = true
		}
	}
	if !found {
		t.Error("expected nested-time-same-name diagnostic")
	}
}

// ---------- log-in-hot-loop tests ----------

func TestLintLogInHotLoop(t *testing.T) {
	source := []byte(`package main
func f() {
	repeat 1000 {
		info "iteration"
	}
}
`)
	diags, err := Lint(source)
	if err != nil {
		t.Fatalf("lint: %v", err)
	}
	found := false
	for _, d := range diags {
		if d.Rule == "log-in-hot-loop" {
			found = true
		}
	}
	if !found {
		t.Error("expected log-in-hot-loop diagnostic")
	}
}

func TestLintLogInHotLoopNegative(t *testing.T) {
	source := []byte(`package main
func f() {
	info "not in a loop"
}
`)
	diags, err := Lint(source)
	if err != nil {
		t.Fatalf("lint: %v", err)
	}
	for _, d := range diags {
		if d.Rule == "log-in-hot-loop" {
			t.Error("should not flag log outside loop")
		}
	}
}
