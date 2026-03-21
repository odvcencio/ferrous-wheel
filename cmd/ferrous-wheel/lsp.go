package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"

	ferrouswheel "github.com/odvcencio/ferrous-wheel"
)

// --- JSON-RPC 2.0 types ---

type jsonrpcMessage struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Method  string           `json:"method,omitempty"`
	Params  json.RawMessage  `json:"params,omitempty"`
	Result  json.RawMessage  `json:"result,omitempty"`
	Error   *jsonrpcError    `json:"error,omitempty"`
}

type jsonrpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// --- LSP types (minimal subset) ---

type initializeResult struct {
	Capabilities serverCapabilities `json:"capabilities"`
}

type serverCapabilities struct {
	TextDocumentSync   int  `json:"textDocumentSync"`
	HoverProvider      bool `json:"hoverProvider"`
	CompletionProvider *struct {
		TriggerChars []string `json:"triggerCharacters,omitempty"`
	} `json:"completionProvider,omitempty"`
	DefinitionProvider     bool `json:"definitionProvider"`
	DocumentSymbolProvider bool `json:"documentSymbolProvider"`
}

type textDocumentItem struct {
	URI  string `json:"uri"`
	Text string `json:"text"`
}

type didOpenParams struct {
	TextDocument textDocumentItem `json:"textDocument"`
}

type didChangeParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
	ContentChanges []struct {
		Text string `json:"text"`
	} `json:"contentChanges"`
}

type hoverParams struct {
	TextDocument struct{ URI string } `json:"textDocument"`
	Position     position             `json:"position"`
}

type position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type hoverResult struct {
	Contents markupContent `json:"contents"`
}

type markupContent struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

type diagnostic struct {
	Range    lspRange `json:"range"`
	Message  string   `json:"message"`
	Severity int      `json:"severity"`
}

type lspRange struct {
	Start position `json:"start"`
	End   position `json:"end"`
}

// --- Document state ---

type document struct {
	text string
	env  *ferrouswheel.TypeEnv
}

// --- Server ---

type lspServer struct {
	mu        sync.Mutex
	docs      map[string]*document
	writer    io.Writer
	lastDiags map[string][]diagnostic
}

func newLSPServer(w io.Writer) *lspServer {
	return &lspServer{
		docs:      make(map[string]*document),
		writer:    w,
		lastDiags: make(map[string][]diagnostic),
	}
}

func (s *lspServer) handle(msg *jsonrpcMessage) *jsonrpcMessage {
	switch msg.Method {
	case "initialize":
		caps := serverCapabilities{
			TextDocumentSync:       1, // full sync
			HoverProvider:          true,
			DefinitionProvider:     true,
			DocumentSymbolProvider: true,
			CompletionProvider: &struct {
				TriggerChars []string `json:"triggerCharacters,omitempty"`
			}{TriggerChars: []string{"."}},
		}
		return s.respond(msg.ID, initializeResult{Capabilities: caps})

	case "initialized":
		return nil

	case "shutdown":
		return s.respond(msg.ID, nil)

	case "exit":
		os.Exit(0)
		return nil

	case "textDocument/didOpen":
		var p didOpenParams
		json.Unmarshal(msg.Params, &p)
		s.openDoc(p.TextDocument.URI, p.TextDocument.Text)
		return nil

	case "textDocument/didChange":
		var p didChangeParams
		json.Unmarshal(msg.Params, &p)
		if len(p.ContentChanges) > 0 {
			s.updateDoc(p.TextDocument.URI, p.ContentChanges[0].Text)
		}
		return nil

	case "textDocument/didClose":
		var p struct {
			TextDocument struct{ URI string } `json:"textDocument"`
		}
		json.Unmarshal(msg.Params, &p)
		s.mu.Lock()
		delete(s.docs, p.TextDocument.URI)
		s.mu.Unlock()
		return nil

	case "textDocument/hover":
		var p hoverParams
		json.Unmarshal(msg.Params, &p)
		return s.hover(msg.ID, p)

	case "textDocument/completion":
		var p struct {
			TextDocument struct{ URI string } `json:"textDocument"`
			Position     position             `json:"position"`
		}
		json.Unmarshal(msg.Params, &p)
		return s.completion(msg.ID, p.TextDocument.URI, p.Position)

	case "textDocument/definition":
		var p struct {
			TextDocument struct{ URI string } `json:"textDocument"`
			Position     position             `json:"position"`
		}
		json.Unmarshal(msg.Params, &p)
		return s.definition(msg.ID, p.TextDocument.URI, p.Position)

	case "textDocument/documentSymbol":
		var p struct {
			TextDocument struct{ URI string } `json:"textDocument"`
		}
		json.Unmarshal(msg.Params, &p)
		return s.documentSymbols(msg.ID, p.TextDocument.URI)

	default:
		if msg.ID != nil {
			return s.respondError(msg.ID, -32601, "method not found: "+msg.Method)
		}
		return nil
	}
}

func (s *lspServer) openDoc(uri, text string) {
	s.mu.Lock()
	env := s.buildEnv(text)
	s.docs[uri] = &document{text: text, env: env}
	s.mu.Unlock()
	s.publishDiagnostics(uri, text)
}

func (s *lspServer) updateDoc(uri, text string) {
	s.mu.Lock()
	env := s.buildEnv(text)
	s.docs[uri] = &document{text: text, env: env}
	s.mu.Unlock()
	s.publishDiagnostics(uri, text)
}

func (s *lspServer) buildEnv(text string) *ferrouswheel.TypeEnv {
	env, err := ferrouswheel.CollectTypes([]byte(text))
	if err != nil {
		return ferrouswheel.NewTypeEnv()
	}
	return env
}

func (s *lspServer) publishDiagnostics(uri, text string) {
	var diags []diagnostic
	_, err := ferrouswheel.Transpile([]byte(text))
	if err != nil {
		diags = append(diags, diagnostic{
			Range:    lspRange{Start: position{0, 0}, End: position{0, 1}},
			Message:  err.Error(),
			Severity: 1,
		})
	}
	s.mu.Lock()
	s.lastDiags[uri] = diags
	s.mu.Unlock()

	params := map[string]interface{}{
		"uri":         uri,
		"diagnostics": diags,
	}
	s.notify("textDocument/publishDiagnostics", params)
}

func (s *lspServer) hover(id *json.RawMessage, p hoverParams) *jsonrpcMessage {
	s.mu.Lock()
	doc, ok := s.docs[p.TextDocument.URI]
	s.mu.Unlock()
	if !ok {
		return s.respond(id, nil)
	}

	lines := strings.Split(doc.text, "\n")
	if p.Position.Line >= len(lines) {
		return s.respond(id, nil)
	}
	line := lines[p.Position.Line]
	word := wordAtPosition(line, p.Position.Character)
	if word == "" {
		return s.respond(id, nil)
	}

	if doc.env != nil {
		if fn, err := doc.env.LookupFunc(word); err == nil {
			return s.respond(id, hoverResult{
				Contents: markupContent{Kind: "markdown", Value: fmt.Sprintf("```go\n%s %s\n```", word, fn.String())},
			})
		}
		if v, err := doc.env.LookupVar(word); err == nil {
			return s.respond(id, hoverResult{
				Contents: markupContent{Kind: "markdown", Value: fmt.Sprintf("```go\n%s %s\n```", word, v.String())},
			})
		}
	}

	return s.respond(id, nil)
}

// completionItem represents a single LSP completion suggestion.
type completionItem struct {
	Label  string `json:"label"`
	Kind   int    `json:"kind"`
	Detail string `json:"detail,omitempty"`
}

func (s *lspServer) completion(id *json.RawMessage, uri string, pos position) *jsonrpcMessage {
	s.mu.Lock()
	doc, ok := s.docs[uri]
	s.mu.Unlock()
	if !ok {
		return s.respond(id, []completionItem{})
	}

	lines := strings.Split(doc.text, "\n")
	if pos.Line >= len(lines) {
		return s.respond(id, []completionItem{})
	}
	line := lines[pos.Line]
	col := pos.Character

	var items []completionItem

	// Check if the character before cursor is '.' for member completion
	if col > 0 && col <= len(line) && line[col-1] == '.' {
		// Find the word before the dot
		dotPos := col - 1
		start := dotPos
		for start > 0 && isIdentChar(line[start-1]) {
			start--
		}
		prefix := line[start:dotPos]

		if doc.env != nil && prefix != "" {
			// Check if it's a struct - offer fields
			if st, err := doc.env.LookupStruct(prefix); err == nil {
				for fname := range st.Fields {
					items = append(items, completionItem{
						Label:  fname,
						Kind:   5, // Field
						Detail: st.Fields[fname].String(),
					})
				}
			}
			// Check if it's an imported package - offer exports
			imports := doc.env.Imports()
			if imp, ok := imports[prefix]; ok {
				for name, fn := range imp.Funcs {
					items = append(items, completionItem{
						Label:  name,
						Kind:   3, // Function
						Detail: fn.String(),
					})
				}
				for name, typ := range imp.Types {
					items = append(items, completionItem{
						Label:  name,
						Kind:   22, // Struct
						Detail: typ.String(),
					})
				}
			}
		}
		return s.respond(id, items)
	}

	// Default: offer all known functions, structs, and enums
	if doc.env != nil {
		for name, fn := range doc.env.Funcs() {
			items = append(items, completionItem{
				Label:  name,
				Kind:   3, // Function
				Detail: fn.String(),
			})
		}
		for name := range doc.env.Structs() {
			items = append(items, completionItem{
				Label: name,
				Kind:  22, // Struct
			})
		}
		for name := range doc.env.Enums() {
			items = append(items, completionItem{
				Label: name,
				Kind:  13, // Enum
			})
		}
	}

	return s.respond(id, items)
}

// locationResult represents an LSP Location for go-to-definition.
type locationResult struct {
	URI   string   `json:"uri"`
	Range lspRange `json:"range"`
}

func (s *lspServer) definition(id *json.RawMessage, uri string, pos position) *jsonrpcMessage {
	s.mu.Lock()
	doc, ok := s.docs[uri]
	s.mu.Unlock()
	if !ok {
		return s.respond(id, nil)
	}

	lines := strings.Split(doc.text, "\n")
	if pos.Line >= len(lines) {
		return s.respond(id, nil)
	}
	line := lines[pos.Line]
	word := wordAtPosition(line, pos.Character)
	if word == "" {
		return s.respond(id, nil)
	}

	// Search for the declaration of this word in the document
	for i, l := range lines {
		trimmed := strings.TrimSpace(l)

		// func name(
		if strings.HasPrefix(trimmed, "func ") {
			fname := extractFuncName(trimmed)
			if fname == word {
				col := strings.Index(l, word)
				return s.respond(id, locationResult{
					URI: uri,
					Range: lspRange{
						Start: position{Line: i, Character: col},
						End:   position{Line: i, Character: col + len(word)},
					},
				})
			}
		}

		// let name =
		if strings.HasPrefix(trimmed, "let ") {
			rest := strings.TrimPrefix(trimmed, "let ")
			// Check for exact name followed by = or :
			if idx := strings.IndexAny(rest, " =:"); idx > 0 {
				name := rest[:idx]
				if name == word {
					col := strings.Index(l, word)
					return s.respond(id, locationResult{
						URI: uri,
						Range: lspRange{
							Start: position{Line: i, Character: col},
							End:   position{Line: i, Character: col + len(word)},
						},
					})
				}
			}
		}

		// enum Name {
		if strings.HasPrefix(trimmed, "enum ") {
			fields := strings.Fields(trimmed)
			if len(fields) >= 2 && fields[1] == word {
				col := strings.Index(l, word)
				return s.respond(id, locationResult{
					URI: uri,
					Range: lspRange{
						Start: position{Line: i, Character: col},
						End:   position{Line: i, Character: col + len(word)},
					},
				})
			}
		}

		// type Name struct/interface
		if strings.HasPrefix(trimmed, "type ") {
			fields := strings.Fields(trimmed)
			if len(fields) >= 2 && fields[1] == word {
				col := strings.Index(l, word)
				return s.respond(id, locationResult{
					URI: uri,
					Range: lspRange{
						Start: position{Line: i, Character: col},
						End:   position{Line: i, Character: col + len(word)},
					},
				})
			}
		}

		// name := or name = (short var declaration)
		if strings.Contains(l, word+" :=") {
			col := strings.Index(l, word)
			if col >= 0 {
				return s.respond(id, locationResult{
					URI: uri,
					Range: lspRange{
						Start: position{Line: i, Character: col},
						End:   position{Line: i, Character: col + len(word)},
					},
				})
			}
		}
	}

	return s.respond(id, nil)
}

func (s *lspServer) documentSymbols(id *json.RawMessage, uri string) *jsonrpcMessage {
	s.mu.Lock()
	doc, ok := s.docs[uri]
	s.mu.Unlock()
	if !ok {
		return s.respond(id, []interface{}{})
	}

	var symbols []map[string]interface{}
	lines := strings.Split(doc.text, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "func ") {
			name := extractFuncName(trimmed)
			if name != "" {
				symbols = append(symbols, map[string]interface{}{
					"name": name,
					"kind": 12, // Function
					"range": lspRange{
						Start: position{Line: i, Character: 0},
						End:   position{Line: i, Character: len(line)},
					},
					"selectionRange": lspRange{
						Start: position{Line: i, Character: strings.Index(line, name)},
						End:   position{Line: i, Character: strings.Index(line, name) + len(name)},
					},
				})
			}
		} else if strings.HasPrefix(trimmed, "enum ") {
			fields := strings.Fields(trimmed)
			if len(fields) >= 2 {
				name := fields[1]
				symbols = append(symbols, map[string]interface{}{
					"name": name,
					"kind": 10, // Enum
					"range": lspRange{
						Start: position{Line: i, Character: 0},
						End:   position{Line: i, Character: len(line)},
					},
					"selectionRange": lspRange{
						Start: position{Line: i, Character: strings.Index(line, name)},
						End:   position{Line: i, Character: strings.Index(line, name) + len(name)},
					},
				})
			}
		}
	}
	return s.respond(id, symbols)
}

func extractFuncName(line string) string {
	line = strings.TrimPrefix(line, "func ")
	if strings.HasPrefix(line, "(") {
		idx := strings.Index(line, ")")
		if idx < 0 {
			return ""
		}
		line = strings.TrimSpace(line[idx+1:])
	}
	idx := strings.IndexByte(line, '(')
	if idx < 0 {
		return ""
	}
	return strings.TrimSpace(line[:idx])
}

func wordAtPosition(line string, col int) string {
	if col >= len(line) {
		return ""
	}
	start, end := col, col
	for start > 0 && isIdentChar(line[start-1]) {
		start--
	}
	for end < len(line) && isIdentChar(line[end]) {
		end++
	}
	if start == end {
		return ""
	}
	return line[start:end]
}

func isIdentChar(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') || (b >= '0' && b <= '9') || b == '_'
}

// --- JSON-RPC helpers ---

func (s *lspServer) respond(id *json.RawMessage, result interface{}) *jsonrpcMessage {
	data, _ := json.Marshal(result)
	return &jsonrpcMessage{JSONRPC: "2.0", ID: id, Result: data}
}

func (s *lspServer) respondError(id *json.RawMessage, code int, msg string) *jsonrpcMessage {
	return &jsonrpcMessage{JSONRPC: "2.0", ID: id, Error: &jsonrpcError{Code: code, Message: msg}}
}

func (s *lspServer) notify(method string, params interface{}) {
	data, _ := json.Marshal(params)
	msg := jsonrpcMessage{JSONRPC: "2.0", Method: method, Params: data}
	s.writeMessage(&msg)
}

func (s *lspServer) writeMessage(msg *jsonrpcMessage) {
	data, _ := json.Marshal(msg)
	fmt.Fprintf(s.writer, "Content-Length: %d\r\n\r\n%s", len(data), data)
}

// --- Main loop ---

func runLSP() int {
	srv := newLSPServer(os.Stdout)
	reader := bufio.NewReader(os.Stdin)

	for {
		var contentLength int
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return 0
			}
			line = strings.TrimSpace(line)
			if line == "" {
				break
			}
			if strings.HasPrefix(line, "Content-Length:") {
				val := strings.TrimSpace(strings.TrimPrefix(line, "Content-Length:"))
				contentLength, _ = strconv.Atoi(val)
			}
		}

		if contentLength == 0 {
			continue
		}

		body := make([]byte, contentLength)
		_, err := io.ReadFull(reader, body)
		if err != nil {
			return 1
		}

		var msg jsonrpcMessage
		if err := json.Unmarshal(body, &msg); err != nil {
			continue
		}

		resp := srv.handle(&msg)
		if resp != nil {
			srv.writeMessage(resp)
		}
	}
}
