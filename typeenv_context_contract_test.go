package ferrouswheel

import (
	"strings"
	"testing"

	gotreesitter "github.com/odvcencio/gotreesitter"
)

// resolveIdentifierAtOccurrence exercises the same source-position lookup that
// the editor uses. Occurrences count identifier nodes in source order.
func resolveIdentifierAtOccurrence(t *testing.T, source, name string, occurrence int) (Type, error) {
	t.Helper()
	src := []byte(source)
	lang, err := GetFWLanguage()
	if err != nil {
		t.Fatalf("language: %v", err)
	}
	tree, err := gotreesitter.NewParser(lang).Parse(src)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	root := tree.RootNode()
	if root.HasError() {
		t.Fatalf("source has parse errors: %s", root.SExpr(lang))
	}
	env, err := CollectTypes(src)
	if err != nil {
		t.Fatalf("collect types: %v", err)
	}
	var matches []*gotreesitter.Node
	var visit func(*gotreesitter.Node)
	visit = func(node *gotreesitter.Node) {
		if node == nil {
			return
		}
		if node.Type(lang) == "identifier" && node.Text(src) == name {
			matches = append(matches, node)
		}
		for i := 0; i < node.ChildCount(); i++ {
			visit(node.Child(i))
		}
	}
	visit(root)
	if occurrence < 1 || occurrence > len(matches) {
		t.Fatalf("identifier %q occurrence %d absent; found %d", name, occurrence, len(matches))
	}
	return env.ResolveAt(root, matches[occurrence-1], lang, src)
}

func TestResolveAtUsesVisibleBindingAndRestoresShadowedBinding(t *testing.T) {
	source := `package main
func f(input string) {
    let value = 7
    _ = value
    {
        let value = "inner"
        _ = value
    }
    _ = value
    _ = input
}`
	for _, tc := range []struct {
		name       string
		occurrence int
		want       string
	}{
		{"value", 2, "int"},
		{"value", 4, "string"},
		{"value", 5, "int"},
		{"input", 2, "string"},
	} {
		t.Run(tc.name+"/"+tc.want, func(t *testing.T) {
			typ, err := resolveIdentifierAtOccurrence(t, source, tc.name, tc.occurrence)
			if err != nil {
				t.Fatalf("resolve: %v", err)
			}
			if typ.String() != tc.want {
				t.Fatalf("type = %s, want %s", typ, tc.want)
			}
		})
	}
}

func TestResolveAtRejectsUseBeforeLocalDeclaration(t *testing.T) {
	source := `package main
func f() {
    _ = later
    let later = 7
}`
	_, err := resolveIdentifierAtOccurrence(t, source, "later", 1)
	if err == nil || !strings.Contains(err.Error(), "undefined identifier: later") {
		t.Fatalf("use before declaration error = %v", err)
	}
}

func TestResolveAtLoopBindingsStayInLoop(t *testing.T) {
	source := `package main
func f() {
    let words: []string = []string{"a"}
    for index, word in words {
        _ = index
        _ = word
    }
    _ = word
}`
	for _, tc := range []struct {
		name       string
		occurrence int
		want       string
	}{
		{"index", 2, "int"},
		{"word", 2, "string"},
	} {
		typ, err := resolveIdentifierAtOccurrence(t, source, tc.name, tc.occurrence)
		if err != nil {
			t.Fatalf("resolve %s: %v", tc.name, err)
		}
		if typ.String() != tc.want {
			t.Fatalf("%s type = %s, want %s", tc.name, typ, tc.want)
		}
	}
	_, err := resolveIdentifierAtOccurrence(t, source, "word", 3)
	if err == nil || !strings.Contains(err.Error(), "undefined identifier: word") {
		t.Fatalf("loop variable escaped scope: %v", err)
	}
}

func TestResolveAtIfLetBindingDoesNotReachElse(t *testing.T) {
	source := `package main
func f() {
    let source = 7
    if let item = source {
        _ = item
    } else {
        _ = item
    }
}`
	typ, err := resolveIdentifierAtOccurrence(t, source, "item", 2)
	if err != nil {
		t.Fatalf("resolve if-let body: %v", err)
	}
	if typ.String() != "int" {
		t.Fatalf("if-let type = %s, want int", typ)
	}
	_, err = resolveIdentifierAtOccurrence(t, source, "item", 3)
	if err == nil || !strings.Contains(err.Error(), "undefined identifier: item") {
		t.Fatalf("if-let binding escaped to else: %v", err)
	}
}

func TestResolveAtMultiBindingUsesEachReturnType(t *testing.T) {
	source := `package main
func pair() (int, string) { return 7, "seven" }
func f() {
    let (count, label) = pair()
    _ = count
    _ = label
}`
	for _, tc := range []struct {
		name string
		want string
	}{
		{"count", "int"},
		{"label", "string"},
	} {
		typ, err := resolveIdentifierAtOccurrence(t, source, tc.name, 2)
		if err != nil {
			t.Fatalf("resolve %s: %v", tc.name, err)
		}
		if typ.String() != tc.want {
			t.Fatalf("%s type = %s, want %s", tc.name, typ, tc.want)
		}
	}
}

func TestResolveAtComprehensionBindingDoesNotLeak(t *testing.T) {
	source := `package main
func f() {
    let words: []string = []string{"a"}
    let result = [word for word in words if word != ""]
    _ = word
    _ = result
}`
	for _, occurrence := range []int{1, 2, 3} {
		typ, err := resolveIdentifierAtOccurrence(t, source, "word", occurrence)
		if err != nil {
			t.Fatalf("resolve comprehension word %d: %v", occurrence, err)
		}
		if typ.String() != "string" {
			t.Fatalf("comprehension word %d type = %s, want string", occurrence, typ)
		}
	}
	_, err := resolveIdentifierAtOccurrence(t, source, "word", 4)
	if err == nil || !strings.Contains(err.Error(), "undefined identifier: word") {
		t.Fatalf("comprehension binding escaped scope: %v", err)
	}
}

func TestResolveAtGoLocalBindings(t *testing.T) {
	source := `package main
func f() {
    var fromVar string = "text"
    const fromConst = 4
    fromShort := true
    _ = fromVar
    _ = fromConst
    _ = fromShort
}`
	for _, tc := range []struct {
		name string
		want string
	}{
		{"fromVar", "string"},
		{"fromConst", "int"},
		{"fromShort", "bool"},
	} {
		typ, err := resolveIdentifierAtOccurrence(t, source, tc.name, 2)
		if err != nil {
			t.Fatalf("resolve %s: %v", tc.name, err)
		}
		if typ.String() != tc.want {
			t.Fatalf("%s type = %s, want %s", tc.name, typ, tc.want)
		}
	}
}

func TestResolveAtValueLoopInfersStringRuneAndRangeInt(t *testing.T) {
	source := `package main
func f() {
    let text = "abc"
    for letter in text { _ = letter }
    for number in 0..3 { _ = number }
}`
	for _, tc := range []struct {
		name string
		want string
	}{
		{"letter", "rune"},
		{"number", "int"},
	} {
		typ, err := resolveIdentifierAtOccurrence(t, source, tc.name, 2)
		if err != nil {
			t.Fatalf("resolve %s: %v", tc.name, err)
		}
		if typ.String() != tc.want {
			t.Fatalf("%s type = %s, want %s", tc.name, typ, tc.want)
		}
	}
}

func TestResolveAtGoMultiBindingsTrackIndividualValues(t *testing.T) {
	source := `package main
func pair() (int, string) { return 7, "seven" }
func f() {
    var count, label = pair()
    _ = count
    _ = label
}`
	for _, tc := range []struct {
		name string
		want string
	}{
		{"count", "int"},
		{"label", "string"},
	} {
		typ, err := resolveIdentifierAtOccurrence(t, source, tc.name, 2)
		if err != nil {
			t.Fatalf("resolve %s: %v", tc.name, err)
		}
		if typ.String() != tc.want {
			t.Fatalf("%s type = %s, want %s", tc.name, typ, tc.want)
		}
	}
}

func TestResolveAtSelectorFieldUsesItsStructType(t *testing.T) {
	source := []byte(`package main
type Point struct { X int }
func f(point *Point) {
    _ = point.X
    _ = point?.X
}`)
	lang, err := GetFWLanguage()
	if err != nil {
		t.Fatalf("language: %v", err)
	}
	tree, err := gotreesitter.NewParser(lang).Parse(source)
	if err != nil || tree.RootNode().HasError() {
		t.Fatalf("parse: %v", err)
	}
	env, err := CollectTypes(source)
	if err != nil {
		t.Fatalf("collect types: %v", err)
	}
	for _, text := range []string{"point.X", "point?.X"} {
		start := strings.Index(string(source), text) + len(text) - 1
		if start < len(text)-1 {
			t.Fatalf("missing fixture expression %q", text)
		}
		field := tree.RootNode().NamedDescendantForByteRange(uint32(start), uint32(start+1))
		if field == nil || field.Text(source) != "X" {
			t.Fatalf("field target for %q = %v", text, field)
		}
		typ, err := env.ResolveAt(tree.RootNode(), field, lang, source)
		if err != nil {
			t.Fatalf("resolve %s: %v", text, err)
		}
		if typ.String() != "int" {
			t.Fatalf("%s type = %s, want int", text, typ)
		}
	}
}
