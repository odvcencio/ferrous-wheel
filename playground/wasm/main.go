//go:build js && wasm

package main

import (
	"encoding/json"
	"fmt"
	"syscall/js"

	ferrouswheel "m31labs.dev/ferrous-wheel"
)

type transpileResult struct {
	GoCode   string   `json:"goCode"`
	Warnings []string `json:"warnings"`
	Error    string   `json:"error"`
}

func transpile(this js.Value, args []js.Value) interface{} {
	if len(args) < 1 {
		return toJSON(transpileResult{Error: "no source provided"})
	}
	source := []byte(args[0].String())
	goCode, warnings, err := ferrouswheel.TranspileWithOptions(source, ferrouswheel.TranspileOptions{})

	result := transpileResult{GoCode: goCode}
	for _, w := range warnings {
		result.Warnings = append(result.Warnings, fmt.Sprintf("line %d:%d: %s", w.Line, w.Col, w.Message))
	}
	if err != nil {
		result.Error = err.Error()
	}
	return toJSON(result)
}

func toJSON(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func main() {
	js.Global().Set("fwTranspile", js.FuncOf(transpile))
	select {}
}
