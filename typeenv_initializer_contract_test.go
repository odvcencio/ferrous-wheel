package ferrouswheel

import "testing"

func TestCollectTopLevelInitializerTypesForEditorSymbols(t *testing.T) {
	source := []byte(`package main
type Box struct { Value int }
var count = 7
var ratio = 1.5
var label = "item"
var enabled = true
var letter = 'x'
var box = Box{Value: 1}
var pointer = &Box{Value: 2}
var left, right = 9, "nine"
const greeting = "hello"
const active, weight = true, 3.5
`)
	env, err := CollectTypes(source)
	if err != nil {
		t.Fatalf("collect: %v", err)
	}
	for name, want := range map[string]string{
		"count": "int", "ratio": "float64", "label": "string",
		"enabled": "bool", "letter": "rune", "box": "Box",
		"pointer": "*Box", "greeting": "string",
		"left": "int", "right": "string", "active": "bool", "weight": "float64",
	} {
		typ, err := env.LookupVar(name)
		if err != nil || typ.String() != want {
			t.Errorf("%s type = %v, %v; want %s", name, typ, err, want)
		}
	}
	if _, found := env.FindSymbol(","); found {
		t.Fatal("comma was registered as a source symbol")
	}
}
