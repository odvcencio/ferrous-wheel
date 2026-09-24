package ferrouswheel

import (
	"strings"
	"testing"
)

func TestConcurrentDeclarationDiagnosticsGiveSafeRewrite(t *testing.T) {
	const prefix = `package main
func pair() (int, string) { return 1, "one" }
func main() {
    concurrent {
`
	const suffix = `
    }
}`
	for _, tt := range []struct {
		name string
		body string
		want []string
	}{
		{"tuple let", "let (count, label) = pair()", []string{"var count int", "var label string", "count, label = pair()"}},
		{"short declaration", "count, label := pair()", []string{"var count int", "var label string", "count, label = pair()"}},
		{"typed let", "let enabled: bool = true", []string{"var enabled bool", "enabled = true"}},
		{"var declaration", "var count int = 1", []string{"Move this var declaration before the concurrent block"}},
		{"const declaration", "const count = 1", []string{"Move this const declaration before the concurrent block"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := Transpile([]byte(prefix + tt.body + suffix))
			if err == nil {
				t.Fatal("concurrent declaration passed transpilation")
			}
			for _, want := range tt.want {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("diagnostic %q missing %q", err, want)
				}
			}
		})
	}
}
