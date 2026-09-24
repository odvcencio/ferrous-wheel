package ferrouswheel

import "testing"

func TestLintFlagsTryWithoutErrorResult(t *testing.T) {
	for _, tt := range []struct {
		name string
		expr string
	}{
		{"prefix", "try onlyValue()"},
		{"postfix", "onlyValue()?"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			source := `package main
func onlyValue() int { return 7 }
func load() (int, error) { return 7, nil }
func f() (int, error) {
    let wrong = ` + tt.expr + `
    let valid = try load()
    return wrong + valid, nil
}`
			diags, err := Lint([]byte(source))
			if err != nil {
				t.Fatalf("lint: %v", err)
			}
			var relevant []LintDiagnostic
			for _, diag := range diags {
				if diag.Rule == "redundant-try" {
					relevant = append(relevant, diag)
				}
			}
			if len(relevant) != 1 || relevant[0].Line != 5 || relevant[0].Severity != LintWarning {
				t.Fatalf("redundant-try findings = %+v; want one warning on line 5", relevant)
			}
		})
	}
}

func TestLintReportsInitializerThatStartsOnLaterLine(t *testing.T) {
	for _, tt := range []struct {
		name    string
		binding string
	}{
		{"single binding", "let value =\n        1"},
		{"tuple binding", "let (value, text) =\n        pair()"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			source := `package main
func pair() (int, string) { return 1, "one" }
func f() {
    ` + tt.binding + `
}`
			diags, err := Lint([]byte(source))
			if err != nil {
				t.Fatalf("lint: %v", err)
			}
			var swallow []LintDiagnostic
			for _, diag := range diags {
				if diag.Rule == "let-swallow" {
					swallow = append(swallow, diag)
				}
			}
			if len(swallow) != 1 || swallow[0].Line != 5 || swallow[0].Severity != LintError {
				t.Fatalf("let-swallow findings = %+v, want error on line 5", swallow)
			}
		})
	}
}
