package ferrouswheel

import (
	"go/parser"
	"go/token"
	"strconv"
	"testing"
)

func importSet(t *testing.T, code string) map[string]struct{} {
	t.Helper()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "generated.go", code, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("parse generated imports: %v\n%s", err, code)
	}

	imports := make(map[string]struct{}, len(file.Imports))
	for _, imp := range file.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			t.Fatalf("unquote import path %q: %v", imp.Path.Value, err)
		}
		imports[path] = struct{}{}
	}
	return imports
}

func TestTranspileInjectImportsIgnoresStringLiteralMatches(t *testing.T) {
	tests := []struct {
		name       string
		source     string
		wantImport string
	}{
		{
			name: "fmt",
			source: `package main

func main() {
	name := "fmt"
	_ = f"hello {name}"
}
`,
			wantImport: "fmt",
		},
		{
			name: "reflect",
			source: `package main

import "fmt"

func main() {
	fmt.Println("reflect")
	name := ""
	_ = name ?? "fallback"
}
`,
			wantImport: "reflect",
		},
		{
			name: "time",
			source: `package main

import "fmt"

func main() {
	fmt.Println("time")
	retry 1 {
		fmt.Println("tick")
	}
}
`,
			wantImport: "time",
		},
		{
			name: "encoding/json",
			source: `package main

import "fmt"

type Config struct {
	Value string
}

derive JSON for Config

func main() {
	fmt.Println("encoding/json")
}
`,
			wantImport: "encoding/json",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			goCode := compileCheck(t, tt.source)
			imports := importSet(t, goCode)
			if _, ok := imports[tt.wantImport]; !ok {
				t.Fatalf("expected %q import in generated code:\n%s", tt.wantImport, goCode)
			}
		})
	}
}
