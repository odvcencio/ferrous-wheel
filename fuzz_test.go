package ferrouswheel

import (
	"go/parser"
	"go/token"
	"testing"
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
	}
	for _, seed := range seeds {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, src string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Transpile panicked for %q: %v", src, r)
			}
		}()

		goCode, err := Transpile([]byte(src))
		if err != nil {
			return
		}

		fset := token.NewFileSet()
		if _, err := parser.ParseFile(fset, "generated.go", goCode, parser.AllErrors); err != nil {
			t.Fatalf("generated Go should parse for %q: %v\n%s", src, err, goCode)
		}
	})
}
