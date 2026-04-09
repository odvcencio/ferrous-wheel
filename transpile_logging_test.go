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
