package ferrouswheel

import (
	"strings"
	"testing"
)

func TestTranspilePackageErrorsNameTheSourceAndLocation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		source string
		want   string
	}{
		{"absent", "// source comment\n", "broken.fw:1:1: ferrous-wheel source is missing a 'package' declaration"},
		{"after declaration", "func f() {}\npackage main\n", "broken.fw:1:1: 'package' declaration must be the first declaration in the file"},
		{"duplicate", "package main\npackage main\n", "broken.fw:2:1: only one 'package' declaration is allowed per file"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := TranspileWithOptions([]byte(tc.source), TranspileOptions{SourceFile: "broken.fw"})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("diagnostic = %v, want %q", err, tc.want)
			}
		})
	}
}

func TestTranspileInvalidByteDiagnosticPointsToSource(t *testing.T) {
	source := []byte("package main\nvar x = \"bad\xff\"\n")
	_, _, err := TranspileWithOptions(source, TranspileOptions{SourceFile: "broken.fw"})
	if err == nil {
		t.Fatal("invalid UTF-8 must fail")
	}
	for _, want := range []string{
		"broken.fw:2:13: invalid UTF-8 byte sequence in source",
		"    var x = \"bad",
		"                ^",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("diagnostic %q lacks %q", err, want)
		}
	}
}

func TestTranspileLongLineDiagnosticStaysBounded(t *testing.T) {
	source := []byte("package main\nvar x = \"" + strings.Repeat("x", 200) + "\x00\"\n")
	_, _, err := TranspileWithOptions(source, TranspileOptions{SourceFile: "broken.fw"})
	if err == nil {
		t.Fatal("illegal control byte must fail")
	}
	if !strings.Contains(err.Error(), "broken.fw:2:210: illegal control byte 0x00") {
		t.Fatalf("diagnostic has wrong location: %v", err)
	}
	if !strings.Contains(err.Error(), " ...\n") || strings.Count(err.Error(), "x") >= 150 {
		t.Fatalf("diagnostic should show a bounded excerpt: %q", err)
	}
}
