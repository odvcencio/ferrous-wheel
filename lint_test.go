package ferrouswheel

import "testing"

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
