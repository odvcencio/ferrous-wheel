package main

import (
	"encoding/json"
	"fmt"
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
	srv.sendNotification("textDocument/didOpen", didOpenParams{
		TextDocument: textDocumentItem{
			URI:  "file:///test.fw",
			Text: "package main\n\nfunc main() {\n\tlet x = 42\n\t_ = x\n}\n",
		},
	})

	// Update with same valid content
	data, _ := json.Marshal(didChangeParams{
		TextDocument: struct {
			URI string `json:"uri"`
		}{URI: "file:///test.fw"},
		ContentChanges: []struct {
			Text string `json:"text"`
		}{{Text: "package main\n\nfunc main() {\n\tlet y = 99\n\t_ = y\n}\n"}},
	})
	msg := &jsonrpcMessage{JSONRPC: "2.0", Method: "textDocument/didChange", Params: data}
	srv.handle(msg)

	srv.mu.Lock()
	diags := srv.lastDiags["file:///test.fw"]
	srv.mu.Unlock()
	if len(diags) != 0 {
		t.Errorf("expected 0 diagnostics after change, got %d", len(diags))
	}
}

func TestLSPDocumentSymbols(t *testing.T) {
	srv := newTestServer()
	srv.sendNotification("textDocument/didOpen", didOpenParams{
		TextDocument: textDocumentItem{
			URI:  "file:///test.fw",
			Text: "package main\n\nfunc hello() {\n}\n\nfunc world() {\n}\n",
		},
	})
	rawID := json.RawMessage([]byte("2"))
	resp := srv.handle(&jsonrpcMessage{
		JSONRPC: "2.0",
		ID:      &rawID,
		Method:  "textDocument/documentSymbol",
		Params:  json.RawMessage(`{"textDocument":{"uri":"file:///test.fw"}}`),
	})
	if resp == nil {
		t.Fatal("no response")
	}
	var symbols []map[string]interface{}
	json.Unmarshal(resp.Result, &symbols)
	if len(symbols) != 2 {
		t.Errorf("expected 2 symbols, got %d", len(symbols))
	}
	if len(symbols) > 0 {
		if symbols[0]["name"] != "hello" {
			t.Errorf("expected first symbol 'hello', got %v", symbols[0]["name"])
		}
	}
	if len(symbols) > 1 {
		if symbols[1]["name"] != "world" {
			t.Errorf("expected second symbol 'world', got %v", symbols[1]["name"])
		}
	}
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
