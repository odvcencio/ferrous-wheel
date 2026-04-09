package ferrouswheel

import (
	"strings"
	"testing"
)

func TestTranspileLogInfo(t *testing.T) {
	source := []byte(`package main
func f() {
	info "user registered", user_id: id, email: email
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if !strings.Contains(goCode, `slog.Info("user registered"`) {
		t.Error("expected slog.Info call")
	}
	if !strings.Contains(goCode, `"user_id", id`) {
		t.Error("expected user_id key-value pair")
	}
	if !strings.Contains(goCode, `"email", email`) {
		t.Error("expected email key-value pair")
	}
}

func TestTranspileLogBareIdent(t *testing.T) {
	source := []byte(`package main
func f() {
	info "registered", user_id, email
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if !strings.Contains(goCode, `"user_id", user_id`) {
		t.Error("expected bare ident expanded to key-value pair")
	}
	if !strings.Contains(goCode, `"email", email`) {
		t.Error("expected bare email expanded")
	}
}

func TestTranspileLogFatal(t *testing.T) {
	source := []byte(`package main
func f() {
	fatal "config missing", path: configPath
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if !strings.Contains(goCode, `slog.Error("config missing"`) {
		t.Error("expected slog.Error for fatal")
	}
	if !strings.Contains(goCode, "os.Exit(1)") {
		t.Error("expected os.Exit(1) after fatal log")
	}
}

func TestTranspileLogNoAttrs(t *testing.T) {
	source := []byte(`package main
func f() {
	info "started"
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if !strings.Contains(goCode, `slog.Info("started")`) {
		t.Error("expected slog.Info with no attrs")
	}
}

func TestTranspileLogAllLevels(t *testing.T) {
	source := []byte(`package main
func f() {
	trace "a"
	debug "b"
	info "c"
	warn "d"
	error "e"
	fatal "f"
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	for _, level := range []string{"slog.Log(", "slog.Debug(", "slog.Info(", "slog.Warn(", "slog.Error("} {
		if !strings.Contains(goCode, level) {
			t.Errorf("expected %s in output", level)
		}
	}
	if !strings.Contains(goCode, "os.Exit(1)") {
		t.Error("expected os.Exit for fatal")
	}
}

func TestTranspileColorInLog(t *testing.T) {
	source := []byte(`package main
func f() {
	info "status", s: color.green("ok")
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if !strings.Contains(goCode, "_fwcolor(") {
		t.Error("expected _fwcolor() call")
	}
	if !strings.Contains(goCode, "32") { // green ANSI code
		t.Error("expected green ANSI code 32")
	}
}

func TestTranspileColorNested(t *testing.T) {
	source := []byte(`package main
func f() {
	info "alert", s: color.bold(color.red("FAIL"))
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if strings.Count(goCode, "_fwcolor(") < 2 {
		t.Error("expected nested _fwcolor() calls")
	}
}

func TestTranspileColorHelperEmitted(t *testing.T) {
	source := []byte(`package main
func f() {
	info "status", s: color.green("ok")
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if !strings.Contains(goCode, "func _fwcolor(") {
		t.Error("expected _fwcolor helper definition")
	}
	if !strings.Contains(goCode, "NO_COLOR") {
		t.Error("expected NO_COLOR env var check")
	}
}

func TestTranspileLogImports(t *testing.T) {
	source := []byte(`package main
func f() {
	info "hello"
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if !strings.Contains(goCode, `"log/slog"`) {
		t.Error("expected log/slog import")
	}
}

func TestTranspileWithBlock(t *testing.T) {
	source := []byte(`package main
func f() {
	with request_id: rid, tenant: t {
		info "handling"
	}
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if !strings.Contains(goCode, `.With("request_id", rid`) {
		t.Error("expected .With() call with request_id")
	}
	if !strings.Contains(goCode, `slog.Info("handling"`) {
		t.Error("expected inner log call")
	}
}

func TestTranspileWithBlockTraceSpans(t *testing.T) {
	source := []byte(`package main
func f() {
	with service: "api" {
		info "boot"
	}
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if !strings.Contains(goCode, "enter") {
		t.Error("expected trace entry log")
	}
	if !strings.Contains(goCode, "exit") {
		t.Error("expected trace exit log")
	}
	if !strings.Contains(goCode, "time.Since") {
		t.Error("expected duration measurement")
	}
}

func TestTranspileWithBlockNested(t *testing.T) {
	source := []byte(`package main
func f() {
	with service: "api" {
		with request_id: rid {
			info "inner"
		}
	}
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if strings.Count(goCode, ".With(") < 2 {
		t.Error("expected nested .With() calls")
	}
}

func TestTranspileTimeBlock(t *testing.T) {
	source := []byte(`package main
func f() {
	time "deploy" {
		info "working"
	}
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if !strings.Contains(goCode, `"deploy"`) {
		t.Error("expected deploy name in output")
	}
	if !strings.Contains(goCode, "time.Now()") {
		t.Error("expected time.Now() call")
	}
	if !strings.Contains(goCode, "time.Since") {
		t.Error("expected time.Since for duration")
	}
	if !strings.Contains(goCode, "_fwlogDepth") {
		t.Error("expected depth tracking")
	}
}

func TestTranspileTimeBlockWithAttrs(t *testing.T) {
	source := []byte(`package main
func f() {
	time "query", sql: stmt {
		fetch()
	}
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if !strings.Contains(goCode, `"sql", stmt`) {
		t.Error("expected sql attr in time block")
	}
}

func TestTranspileTimeBlockNested(t *testing.T) {
	source := []byte(`package main
func f() {
	time "outer" {
		time "inner" {
			work()
		}
	}
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if strings.Count(goCode, "_fwlogDepth++") != 2 {
		t.Error("expected two depth increments for nesting")
	}
	if strings.Count(goCode, "_fwlogDepth--") != 2 {
		t.Error("expected two depth decrements for nesting")
	}
}

func TestTranspileLogConfig(t *testing.T) {
	source := []byte(`package main

log.config!(level: .info, time: .relative, format: .pretty)

func f() {
	info "hello"
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	// Config directive should not appear in output
	if strings.Contains(goCode, "log.config") {
		t.Error("raw log.config! should not appear in output")
	}
	// Should still have slog calls from the info statement
	if !strings.Contains(goCode, `slog.Info("hello")`) {
		t.Error("expected slog.Info call")
	}
}

func TestTranspileLogConfigDefaultLevel(t *testing.T) {
	source := []byte(`package main

log.config!(level: .debug)

func f() {
	debug "test"
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	// Should transpile normally, config consumed silently
	if strings.Contains(goCode, "log.config") {
		t.Error("raw log.config! should not appear in output")
	}
	if !strings.Contains(goCode, `slog.Debug("test")`) {
		t.Error("expected slog.Debug call")
	}
}

func TestTranspileTimeBlockHelperEmitted(t *testing.T) {
	source := []byte(`package main
func f() {
	time "op" {
		work()
	}
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	if !strings.Contains(goCode, "func _fwlogTreePrefix(") {
		t.Error("expected tree prefix helper")
	}
}

func TestTranspileLogHelperShape(t *testing.T) {
	source := []byte(`package main
func main() {
	info "hello", name: "world"
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	// Handler should be present
	if !strings.Contains(goCode, "_fwlogHandler") {
		t.Error("expected _fwlogHandler type")
	}
	// Trace level constant
	if !strings.Contains(goCode, "_fwLevelTrace") {
		t.Error("expected _fwLevelTrace constant")
	}
	// Relative timestamp support
	if !strings.Contains(goCode, "_fwlogStart") {
		t.Error("expected _fwlogStart for relative time")
	}
	// Color in handler
	if !strings.Contains(goCode, "_fwcolorEnabled") {
		t.Error("expected _fwcolorEnabled in handler")
	}
	// Init function
	if !strings.Contains(goCode, "func init()") {
		t.Error("expected init() function")
	}
	// FW_LOG env var
	if !strings.Contains(goCode, "FW_LOG") {
		t.Error("expected FW_LOG env var handling")
	}
}

func TestTranspileLogHelperWithConfig(t *testing.T) {
	source := []byte(`package main

log.config!(level: .debug)

func main() {
	debug "test"
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	// Config should set default level to debug
	if !strings.Contains(goCode, "slog.LevelDebug") {
		t.Error("expected slog.LevelDebug as default level from config")
	}
}

func TestTranspileLogHelperNoColorDuplicate(t *testing.T) {
	source := []byte(`package main
func f() {
	info "status", s: color.green("ok")
}
`)
	goCode, err := Transpile(source)
	if err != nil {
		t.Fatalf("transpile: %v", err)
	}
	t.Logf("Go:\n%s", goCode)

	// _fwcolor should appear exactly once (from log helper, not duplicated by color helper)
	count := strings.Count(goCode, "func _fwcolor(")
	if count != 1 {
		t.Errorf("expected exactly 1 _fwcolor definition, got %d", count)
	}
}
