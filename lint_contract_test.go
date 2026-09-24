package ferrouswheel

import "testing"

func TestLintCountsInterpolationAsARead(t *testing.T) {
	source := []byte(`package main
func f() {
    let name = "Ada"
    let greeting = f"Hello {name}"
    _ = greeting
}`)
	diagnostics, err := Lint(source)
	if err != nil {
		t.Fatalf("lint: %v", err)
	}
	for _, diagnostic := range diagnostics {
		if diagnostic.Rule == "unused-let" && diagnostic.Line == 3 {
			t.Fatalf("interpolated name was reported unused: %+v", diagnostic)
		}
	}
}

func TestLintSafeNavigationNeedsPointer(t *testing.T) {
	for _, tc := range []struct {
		name        string
		declaration string
		want        bool
	}{
		{"value receiver", "var point Point", true},
		{"pointer receiver", "var point *Point", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			source := []byte("package main\ntype Point struct { X int }\n" + tc.declaration + "\nfunc f() { _ = point?.X }\n")
			diagnostics, err := Lint(source)
			if err != nil {
				t.Fatalf("lint: %v", err)
			}
			found := false
			for _, diagnostic := range diagnostics {
				if diagnostic.Rule == "unnecessary-safe-nav" {
					found = true
					if diagnostic.Line != 4 {
						t.Fatalf("safe navigation diagnostic line = %d, want 4", diagnostic.Line)
					}
				}
			}
			if found != tc.want {
				t.Fatalf("unnecessary-safe-nav present = %t, want %t; diagnostics: %+v", found, tc.want, diagnostics)
			}
		})
	}
}
