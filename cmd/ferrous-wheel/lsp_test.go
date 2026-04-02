package main

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type testServer struct {
	*lspServer
}

func newTestServer() *testServer {
	var buf strings.Builder
	srv := newLSPServer(&buf)
	return &testServer{lspServer: srv}
}

func (ts *testServer) send(method string, id int, params interface{}) *jsonrpcMessage {
	data, _ := json.Marshal(params)
	rawID := json.RawMessage([]byte(fmt.Sprintf("%d", id)))
	msg := &jsonrpcMessage{
		JSONRPC: "2.0",
		ID:      &rawID,
		Method:  method,
		Params:  data,
	}
	return ts.handle(msg)
}

func (ts *testServer) sendNotification(method string, params interface{}) {
	data, _ := json.Marshal(params)
	msg := &jsonrpcMessage{JSONRPC: "2.0", Method: method, Params: data}
	ts.handle(msg)
}

func fileURI(path string) string {
	return (&url.URL{Scheme: "file", Path: path}).String()
}

func TestLSPInitialize(t *testing.T) {
	srv := newTestServer()
	resp := srv.send("initialize", 1, map[string]interface{}{"capabilities": map[string]interface{}{}})
	if resp == nil {
		t.Fatal("no response")
	}
	var result initializeResult
	json.Unmarshal(resp.Result, &result)
	if !result.Capabilities.HoverProvider {
		t.Error("missing hoverProvider")
	}
	if !result.Capabilities.DefinitionProvider {
		t.Error("missing definitionProvider")
	}
	if !result.Capabilities.DocumentSymbolProvider {
		t.Error("missing documentSymbolProvider")
	}
	if result.Capabilities.TextDocumentSync != 1 {
		t.Errorf("expected textDocumentSync=1, got %d", result.Capabilities.TextDocumentSync)
	}
}

func TestLSPDiagnosticsCleanFile(t *testing.T) {
	srv := newTestServer()
	srv.sendNotification("textDocument/didOpen", didOpenParams{
		TextDocument: textDocumentItem{
			URI:  "file:///test.fw",
			Text: "package main\n\nfunc main() {\n\tlet x = 42\n\t_ = x\n}\n",
		},
	})
	srv.mu.Lock()
	diags := srv.lastDiags["file:///test.fw"]
	srv.mu.Unlock()
	if len(diags) != 0 {
		t.Errorf("expected 0 diagnostics, got %d: %v", len(diags), diags)
	}
}

func TestLSPDiagnosticsOnChange(t *testing.T) {
	srv := newTestServer()
	uri := "file:///test.fw"
	srv.sendNotification("textDocument/didOpen", didOpenParams{
		TextDocument: textDocumentItem{
			URI:  uri,
			Text: "package main\n\nfunc main() {\n\tlet x = 42\n\t_ = x\n}\n",
		},
	})

	// didChange marks the doc dirty but does not republish diagnostics.
	data, _ := json.Marshal(didChangeParams{
		TextDocument: struct {
			URI string `json:"uri"`
		}{URI: uri},
		ContentChanges: []struct {
			Text string `json:"text"`
		}{{Text: "package main\n\nfunc main() {\n\tlet x = \"hi\"\n\t_ = x\n}\n"}},
	})
	msg := &jsonrpcMessage{JSONRPC: "2.0", Method: "textDocument/didChange", Params: data}
	srv.handle(msg)

	srv.mu.Lock()
	diags := srv.lastDiags[uri]
	srv.mu.Unlock()
	if len(diags) != 0 {
		t.Errorf("expected 0 diagnostics after change, got %d", len(diags))
	}

	resp := srv.send("textDocument/hover", 7, hoverParams{
		TextDocument: struct{ URI string }{URI: uri},
		Position:     position{Line: 4, Character: 5},
	})
	if resp == nil {
		t.Fatal("no hover response")
	}
	var hover hoverResult
	if err := json.Unmarshal(resp.Result, &hover); err != nil {
		t.Fatalf("unmarshal hover: %v", err)
	}
	if !strings.Contains(hover.Contents.Value, "x string") {
		t.Fatalf("expected dirty hover rebuild to see updated string binding, got %q", hover.Contents.Value)
	}
}

func TestLSPDiagnosticsIncludeWarnings(t *testing.T) {
	srv := newTestServer()
	uri := "file:///test.fw"
	srv.sendNotification("textDocument/didOpen", didOpenParams{
		TextDocument: textDocumentItem{
			URI:  uri,
			Text: "package main\n\nfunc main() {\n\tfallback := fn(v) v ?? \"fallback\"\n\t_ = fallback\n}\n",
		},
	})
	srv.sendNotification("textDocument/didSave", didSaveParams{
		TextDocument: struct {
			URI string `json:"uri"`
		}{URI: uri},
	})
	srv.mu.Lock()
	diags := srv.lastDiags[uri]
	srv.mu.Unlock()
	if len(diags) != 1 {
		t.Fatalf("expected 1 warning diagnostic, got %d: %+v", len(diags), diags)
	}
	if diags[0].Severity != 2 {
		t.Fatalf("expected warning severity 2, got %+v", diags[0])
	}
	if diags[0].Range.Start.Line != 3 {
		t.Fatalf("expected warning on line 3, got %+v", diags[0])
	}
	if !strings.Contains(diags[0].Message, "unresolved for ??") {
		t.Fatalf("unexpected warning diagnostic: %+v", diags[0])
	}
}

func TestLSPDocumentSymbols(t *testing.T) {
	dir := t.TempDir()
	otherPath := filepath.Join(dir, "other.fw")
	if err := os.WriteFile(otherPath, []byte("package main\n\nfunc sibling() {}\n"), 0644); err != nil {
		t.Fatalf("write sibling file: %v", err)
	}
	mainPath := filepath.Join(dir, "main.fw")
	mainURI := fileURI(mainPath)
	srv := newTestServer()
	srv.sendNotification("textDocument/didOpen", didOpenParams{
		TextDocument: textDocumentItem{
			URI:  mainURI,
			Text: "package main\n\nfunc hello(\n\tname string,\n) string {\n\treturn name\n}\n",
		},
	})
	rawID := json.RawMessage([]byte("2"))
	resp := srv.handle(&jsonrpcMessage{
		JSONRPC: "2.0",
		ID:      &rawID,
		Method:  "textDocument/documentSymbol",
		Params:  json.RawMessage(fmt.Sprintf(`{"textDocument":{"uri":%q}}`, mainURI)),
	})
	if resp == nil {
		t.Fatal("no response")
	}
	var symbols []map[string]interface{}
	json.Unmarshal(resp.Result, &symbols)
	if len(symbols) != 1 {
		t.Errorf("expected 1 current-file symbol, got %d", len(symbols))
	}
	if len(symbols) > 0 {
		if symbols[0]["name"] != "hello" {
			t.Errorf("expected symbol 'hello', got %v", symbols[0]["name"])
		}
	}
}

func TestLSPDefinitionCrossFile(t *testing.T) {
	dir := t.TempDir()
	typesPath := filepath.Join(dir, "types.fw")
	if err := os.WriteFile(typesPath, []byte("package main\n\ntype User struct { Name string }\n"), 0644); err != nil {
		t.Fatalf("write types.fw: %v", err)
	}
	mainPath := filepath.Join(dir, "main.fw")
	mainURI := fileURI(mainPath)

	srv := newTestServer()
	srv.sendNotification("textDocument/didOpen", didOpenParams{
		TextDocument: textDocumentItem{
			URI:  mainURI,
			Text: "package main\n\nfunc main() {\n\tlet user = User{}\n\t_ = user\n}\n",
		},
	})

	resp := srv.send("textDocument/definition", 9, map[string]interface{}{
		"textDocument": map[string]string{"uri": mainURI},
		"position":     map[string]int{"line": 3, "character": 13},
	})
	if resp == nil {
		t.Fatal("no response")
	}
	var loc locationResult
	if err := json.Unmarshal(resp.Result, &loc); err != nil {
		t.Fatalf("unmarshal location: %v", err)
	}
	if loc.URI != fileURI(typesPath) {
		t.Fatalf("expected definition in %s, got %+v", fileURI(typesPath), loc)
	}
	if loc.Range.Start.Line != 2 {
		t.Fatalf("expected struct definition on line 2, got %+v", loc)
	}
}

func TestLSPHoverLocalBinding(t *testing.T) {
	dir := t.TempDir()
	mainPath := filepath.Join(dir, "main.fw")
	mainURI := fileURI(mainPath)

	srv := newTestServer()
	srv.sendNotification("textDocument/didOpen", didOpenParams{
		TextDocument: textDocumentItem{
			URI:  mainURI,
			Text: "package main\n\nfunc main() {\n\tlet name = \"\"\n\t_ = name\n}\n",
		},
	})

	resp := srv.send("textDocument/hover", 11, hoverParams{
		TextDocument: struct{ URI string }{URI: mainURI},
		Position:     position{Line: 4, Character: 6},
	})
	if resp == nil {
		t.Fatal("no response")
	}
	var hover hoverResult
	if err := json.Unmarshal(resp.Result, &hover); err != nil {
		t.Fatalf("unmarshal hover: %v", err)
	}
	if !strings.Contains(hover.Contents.Value, "name string") {
		t.Fatalf("expected local binding hover, got %q", hover.Contents.Value)
	}
}

func TestLSPCompletionForImportedAlias(t *testing.T) {
	srv := newTestServer()
	srv.sendNotification("textDocument/didOpen", didOpenParams{
		TextDocument: textDocumentItem{
			URI:  "file:///test.fw",
			Text: "package main\n\nimport h \"net/http\"\n\nfunc f() {\n\th.Get\n}\n",
		},
	})

	resp := srv.send("textDocument/completion", 3, map[string]interface{}{
		"textDocument": map[string]string{"uri": "file:///test.fw"},
		"position":     map[string]int{"line": 5, "character": 3},
	})
	if resp == nil {
		t.Fatal("no response")
	}

	var items []completionItem
	if err := json.Unmarshal(resp.Result, &items); err != nil {
		t.Fatalf("unmarshal completions: %v", err)
	}

	for _, item := range items {
		if item.Label == "Get" {
			return
		}
	}
	t.Fatalf("expected imported package completion for Get, got %+v", items)
}

func TestLSPShutdown(t *testing.T) {
	srv := newTestServer()
	resp := srv.send("shutdown", 1, nil)
	if resp == nil {
		t.Fatal("no response to shutdown")
	}
}

func TestLSPUnknownMethod(t *testing.T) {
	srv := newTestServer()
	resp := srv.send("nonexistent/method", 1, nil)
	if resp == nil || resp.Error == nil {
		t.Fatal("expected error for unknown method")
	}
	if resp.Error.Code != -32601 {
		t.Errorf("expected -32601, got %d", resp.Error.Code)
	}
}

func TestLSPDidClose(t *testing.T) {
	srv := newTestServer()
	srv.sendNotification("textDocument/didOpen", didOpenParams{
		TextDocument: textDocumentItem{
			URI:  "file:///test.fw",
			Text: "package main\n",
		},
	})
	srv.mu.Lock()
	_, exists := srv.docs["file:///test.fw"]
	srv.mu.Unlock()
	if !exists {
		t.Fatal("document not tracked after open")
	}

	// Close
	data, _ := json.Marshal(map[string]interface{}{
		"textDocument": map[string]string{"uri": "file:///test.fw"},
	})
	srv.handle(&jsonrpcMessage{JSONRPC: "2.0", Method: "textDocument/didClose", Params: data})

	srv.mu.Lock()
	_, exists = srv.docs["file:///test.fw"]
	srv.mu.Unlock()
	if exists {
		t.Error("document still tracked after close")
	}
}

func TestLSPHoverNoDocument(t *testing.T) {
	srv := newTestServer()
	resp := srv.send("textDocument/hover", 1, hoverParams{
		TextDocument: struct{ URI string }{URI: "file:///nonexistent.fw"},
		Position:     position{Line: 0, Character: 0},
	})
	if resp == nil {
		t.Fatal("expected response")
	}
	// Should return null result for unknown document
	if string(resp.Result) != "null" {
		t.Errorf("expected null result, got %s", resp.Result)
	}
}

func TestLSPNotificationNoResponse(t *testing.T) {
	srv := newTestServer()
	// Notifications without ID should not produce a response
	data, _ := json.Marshal(map[string]interface{}{})
	msg := &jsonrpcMessage{JSONRPC: "2.0", Method: "unknown/notification", Params: data}
	resp := srv.handle(msg)
	if resp != nil {
		t.Error("notification should not produce a response")
	}
}

func TestWordAtPosition(t *testing.T) {
	tests := []struct {
		line string
		col  int
		want string
	}{
		{"func hello() {", 5, "hello"},
		{"func hello() {", 7, "hello"},
		{"let x = 42", 4, "x"},
		{"let x = 42", 8, "42"},
		{"", 0, ""},
		{"  ", 1, ""},
		{"foo.bar", 0, "foo"},
		{"foo.bar", 4, "bar"},
	}
	for _, tt := range tests {
		got := wordAtPosition(tt.line, tt.col)
		if got != tt.want {
			t.Errorf("wordAtPosition(%q, %d) = %q, want %q", tt.line, tt.col, got, tt.want)
		}
	}
}

func TestExtractFuncName(t *testing.T) {
	tests := []struct {
		line string
		want string
	}{
		{"func hello() {", "hello"},
		{"func main() {", "main"},
		{"func (p *Point) getX() float64 {", "getX"},
		{"func () {", ""},
	}
	for _, tt := range tests {
		got := extractFuncName(tt.line)
		if got != tt.want {
			t.Errorf("extractFuncName(%q) = %q, want %q", tt.line, got, tt.want)
		}
	}
}
