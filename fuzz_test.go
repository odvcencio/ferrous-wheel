package ferrouswheel

import (
	"context"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func FuzzTranspileProducesParsableGo(f *testing.F) {
	seeds := []string{
		`package main`,
		`package main

func main() {
	let x = 1
	_ = x
}
`,
		`package main

func main() {
	enum Color { Red, Blue(int) }
	_ = Red()
}
`,
		`package main

func main() {
	x := "hello" |> fmt.Sprint
	_ = x
}
`,
		`package mx n

func main() {
	let ai = 1
	_ = x
}
`,
		`this is not valid fw`,
		// Regression seed for the range-comprehension bug: comprehensions
		// over a range_expression (`0 .. 5`) used to emit a bare comment
		// (` /* range 0..5 */ `) as the range clause instead of a real
		// loop, producing generated Go that doesn't parse. See CHANGELOG
		// v0.6.0 and comp_only.fw.
		`package main

func main() {
	let sq = [x * x for x in 0 .. 5]
	_ = sq
}
`,
		// Regression seed for the no-space range lexing bug: "0..10"
		// (no spaces) used to fail to parse at all because the digit-dot
		// float form ("0.") won longest-match over the range operator.
		`package main

func main() {
	for i in 0..10 {
		_ = i
	}
}
`,
		// Fuzz-discovered regressions from the v0.6.0 stabilization pass,
		// each of which previously transpiled "successfully" to Go that
		// doesn't parse. See parse_diagnostics.go and transpile.go's
		// emitRange for the fixes.
		"package A\n",                     // empty package: no top-level declarations
		"package 0A",                      // digit silently dropped between "package" and an identifier
		"package A\nfunc A()0A",           // same, for a bogus return type
		"package\vA",                      // vertical tab isn't Go whitespace but was treated as FW extras
		"package A\nfunc A(){#}",          // garbage alone in an otherwise-empty block
		"package A\nfunc A(){0\n#}",       // trailing garbage after the last real statement in a block
		"package A\nfunc A(){#0}",         // leading garbage before the first statement in a block
		"package A\x00",                   // NUL byte accepted as a statement terminator
		"package A\nfunc A(){08%0}",       // digit silently dropped inside a binary expression
		"package func\n",                  // Go reserved word used as a package name
		"package A\nfunc A(){for 0.A0{}}", // int_literal treated as a selector operand
		"package A\nfunc A(){0[0..0]}",    // range expression used as an index/subscript
		// Regression seed for a bug in the emitRange fix itself: a
		// range-based comprehension's loop header was computed
		// speculatively (and discarded) via t.emit(iterable) before being
		// overwritten with the real counting-loop header, which wrongly
		// tripped emitRange's new "range used standalone" error.
		`package main

func main() {
	squares := [x * x for x in 0 .. 10]
	_ = squares
}
`,
		// Regression seed: "for _ in 0..N" (discard the loop value) used
		// to transpile to "for _ := 0; _ < N; _++", which doesn't compile
		// since Go's blank identifier can be assigned but never read.
		`package main

func main() {
	for _ in 0 .. 3 {
		_ = 1
	}
}
`,
		// Regression seed: a raw invalid-UTF-8 byte pasted into a string
		// literal used to be echoed straight into the generated Go, which
		// go/scanner rejects ("Source code is Unicode text encoded in
		// UTF-8" — Go spec).
		"package A\nfunc A(){\"\xff\"}",
		// Regression seed: a raw (unescaped) newline inside a
		// double-quoted string literal used to be silently absorbed as
		// insignificant extras instead of ending the token or erroring,
		// producing generated Go with a literal line break inside an
		// interpreted string literal — illegal in Go.
		"package A\nfunc A(){\"\n\"}",
		// Regression seed: go_grammar.go's parameter_list rule (ported
		// from tree-sitter-go) makes the parameter sequence and the
		// trailing comma independently optional, so a bare "(,)" with no
		// actual parameters parsed and echoed straight through — real Go
		// rejects it ("expected ')', found ',').
		"package A\nfunc A(,)",
		// Regression seed: validatePackageClausePresent only checked that
		// the FIRST top-level declaration was a package clause, not that
		// there was only one — `package A\npackage A` parsed as two
		// package_clause siblings and was echoed straight through, which
		// real Go rejects ("expected declaration, found 'package'").
		"package A\npackage A",
	}
	for _, seed := range seeds {
		f.Add(seed)
	}
	// Build this bounded set of valid source files. Arbitrary fuzz mutations
	// can contain unresolved names, so they keep the parse-only property.
	compileCorpus := make(map[string]string)
	for _, name := range []string{"binary_parser.fw", "cli_tool.fw", "http_server.fw", "pipeline.fw", "resilient_client.fw"} {
		path := filepath.Join("testdata", "corpus", name)
		src, err := os.ReadFile(path)
		if err != nil {
			f.Fatalf("read compile corpus %s: %v", path, err)
		}
		f.Add(string(src))
		compileCorpus[string(src)] = name
	}

	f.Fuzz(func(t *testing.T, src string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Transpile panicked for %q: %v", src, r)
			}
		}()

		corpusName, mustCompile := compileCorpus[src]
		goCode, err := Transpile([]byte(src))
		if err != nil {
			if mustCompile {
				t.Fatalf("transpile compile corpus %s: %v", corpusName, err)
			}
			return
		}

		fset := token.NewFileSet()
		if _, err := parser.ParseFile(fset, "generated.go", goCode, parser.AllErrors); err != nil {
			t.Fatalf("generated Go should parse for %q: %v\n%s", src, err, goCode)
		}
		if mustCompile {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/fwcompilecorpus\n\ngo 1.25.0\n"), 0o600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(goCode), 0o600); err != nil {
				t.Fatal(err)
			}
			if corpusName == "resilient_client.fw" {
				// This source expects getValue from another file in its package.
				support := "package main\nfunc getValue() *string { v := \"ok\"; return &v }\n"
				if err := os.WriteFile(filepath.Join(dir, "support.go"), []byte(support), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			cmd := exec.CommandContext(ctx, "go", "build", "-o", filepath.Join(dir, "program"), ".")
			cmd.Dir = dir
			cmd.Env = append(os.Environ(), "GOWORK=off")
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("generated Go from %s must compile: %v\n%s\n%s", corpusName, err, out, goCode)
			}
		}
	})
}
