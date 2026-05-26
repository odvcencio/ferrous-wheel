package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"

	ferrouswheel "m31labs.dev/ferrous-wheel"
	gotreesitter "github.com/odvcencio/gotreesitter"
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

type didSaveParams struct {
	TextDocument struct {
		URI string `json:"uri"`
	} `json:"textDocument"`
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
	uri   string
	path  string
	dir   string
	text  string
	env   *ferrouswheel.TypeEnv
	root  *gotreesitter.Node
	lang  *gotreesitter.Language
	src   []byte
	dirty bool
}

type directoryState struct {
	env   *ferrouswheel.TypeEnv
	dirty bool
}

// --- Server ---

type lspServer struct {
	mu        sync.Mutex
	docs      map[string]*document
	dirs      map[string]*directoryState
	writer    io.Writer
	lastDiags map[string][]diagnostic
}

func newLSPServer(w io.Writer) *lspServer {
	return &lspServer{
		docs:      make(map[string]*document),
		dirs:      make(map[string]*directoryState),
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

	case "textDocument/didSave":
		var p didSaveParams
		json.Unmarshal(msg.Params, &p)
		s.saveDoc(p.TextDocument.URI)
		return nil

	case "textDocument/didClose":
		var p struct {
			TextDocument struct{ URI string } `json:"textDocument"`
		}
		json.Unmarshal(msg.Params, &p)
		s.closeDoc(p.TextDocument.URI)
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
	s.docs[uri] = &document{
		uri:   uri,
		path:  filePathForURI(uri),
		dir:   dirForURI(uri),
		text:  text,
		dirty: true,
	}
	s.markDirDirtyLocked(dirForURI(uri))
	s.mu.Unlock()
}

func (s *lspServer) updateDoc(uri, text string) {
	s.mu.Lock()
	if doc, ok := s.docs[uri]; ok {
		doc.text = text
		doc.dirty = true
		s.markDirDirtyLocked(doc.dir)
	} else {
		s.docs[uri] = &document{
			uri:   uri,
			path:  filePathForURI(uri),
			dir:   dirForURI(uri),
			text:  text,
			dirty: true,
		}
		s.markDirDirtyLocked(dirForURI(uri))
	}
	s.mu.Unlock()
}

func (s *lspServer) saveDoc(uri string) {
	s.publishDiagnostics(uri)
}

func filePathForURI(uri string) string {
	parsed, err := url.Parse(uri)
	if err != nil || parsed.Scheme != "file" {
		return ""
	}
	return parsed.Path
}

func dirForURI(uri string) string {
	path := filePathForURI(uri)
	if path == "" {
		return ""
	}
	return filepath.Dir(path)
}

func uriForPath(path string) string {
	if path == "" {
		return ""
	}
	return (&url.URL{Scheme: "file", Path: path}).String()
}

func parseDocument(text string) (*gotreesitter.Node, *gotreesitter.Language, []byte, error) {
	lang, err := ferrouswheel.GetFWLanguage()
	if err != nil {
		return nil, nil, nil, err
	}
	src := []byte(text)
	parser := gotreesitter.NewParser(lang)
	tree, err := parser.Parse(src)
	if err != nil {
		return nil, nil, nil, err
	}
	return tree.RootNode(), lang, src, nil
}

func (s *lspServer) directoryStateLocked(dir string) *directoryState {
	state, ok := s.dirs[dir]
	if !ok {
		state = &directoryState{dirty: true}
		s.dirs[dir] = state
	}
	return state
}

func (s *lspServer) markDirDirtyLocked(dir string) {
	s.directoryStateLocked(dir).dirty = true
}

func (s *lspServer) gatherDirSourcesLocked(dir string) map[string][]byte {
	files := make(map[string][]byte)
	if dir != "" {
		matches, err := filepath.Glob(filepath.Join(dir, "*.fw"))
		if err == nil {
			for _, match := range matches {
				src, readErr := os.ReadFile(match)
				if readErr == nil {
					files[match] = src
				}
			}
		}
	}
	for _, doc := range s.docs {
		if doc.dir != dir || doc.path == "" {
			continue
		}
		files[doc.path] = []byte(doc.text)
	}
	return files
}

func (s *lspServer) buildDirEnvLocked(dir string) *ferrouswheel.TypeEnv {
	files := s.gatherDirSourcesLocked(dir)
	if len(files) == 0 {
		return ferrouswheel.NewTypeEnv()
	}
	env, err := ferrouswheel.CollectTypesMulti(files)
	if err != nil {
		return ferrouswheel.NewTypeEnv()
	}
	moduleDir := ""
	if dir != "" {
		moduleDir = ferrouswheel.FindModuleDir(filepath.Join(dir, "dummy.fw"))
	}
	if err := env.LoadCollectedImports(moduleDir); err != nil {
		return env
	}
	return env
}

func (s *lspServer) ensureDocBuiltLocked(uri string) (*document, bool) {
	doc, ok := s.docs[uri]
	if !ok {
		return nil, false
	}
	if doc.dirty || doc.root == nil || doc.lang == nil {
		root, lang, src, err := parseDocument(doc.text)
		if err == nil {
			doc.root = root
			doc.lang = lang
			doc.src = src
		} else {
			doc.root = nil
			doc.lang = nil
			doc.src = []byte(doc.text)
		}
	}
	dirState := s.directoryStateLocked(doc.dir)
	if dirState.dirty || dirState.env == nil || doc.env == nil {
		dirState.env = s.buildDirEnvLocked(doc.dir)
		dirState.dirty = false
		for _, sibling := range s.docs {
			if sibling.dir == doc.dir {
				sibling.env = dirState.env
			}
		}
	}
	doc.env = dirState.env
	doc.dirty = false
	return doc, true
}

func (s *lspServer) closeDoc(uri string) {
	s.mu.Lock()
	if doc, ok := s.docs[uri]; ok {
		delete(s.docs, uri)
		s.markDirDirtyLocked(doc.dir)
	} else {
		delete(s.docs, uri)
	}
	s.mu.Unlock()
}

func (s *lspServer) publishDiagnostics(uri string) {
	var diags []diagnostic
	s.mu.Lock()
	doc, ok := s.docs[uri]
	if !ok {
		s.mu.Unlock()
		return
	}
	text := doc.text
	path := doc.path
	s.mu.Unlock()

	// Run lint and collect diagnostics
	lintDiags, lintErr := ferrouswheel.Lint([]byte(text))
	if lintErr == nil {
		for _, ld := range lintDiags {
			line := ld.Line - 1
			col := ld.Col - 1
			if line < 0 {
				line = 0
			}
			if col < 0 {
				col = 0
			}
			sev := 3 // info
			switch ld.Severity {
			case ferrouswheel.LintError:
				sev = 1
			case ferrouswheel.LintWarning:
				sev = 2
			}
			diags = append(diags, diagnostic{
				Range: lspRange{
					Start: position{Line: line, Character: col},
					End:   position{Line: line, Character: col + 1},
				},
				Message:  fmt.Sprintf("[%s] %s", ld.Rule, ld.Message),
				Severity: sev,
			})
		}
	}

	_, warnings, err := ferrouswheel.TranspileWithOptions([]byte(text), ferrouswheel.TranspileOptions{
		SourceFile: path,
		LintRan:    true,
	})
	if err != nil {
		diags = append(diags, diagnostic{
			Range:    lspRange{Start: position{0, 0}, End: position{0, 1}},
			Message:  err.Error(),
			Severity: 1,
		})
	} else {
		for _, warning := range warnings {
			line := warning.Line - 1
			col := warning.Col - 1
			if line < 0 {
				line = 0
			}
			if col < 0 {
				col = 0
			}
			diags = append(diags, diagnostic{
				Range: lspRange{
					Start: position{Line: line, Character: col},
					End:   position{Line: line, Character: col + 1},
				},
				Message:  warning.Message,
				Severity: 2,
			})
		}
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
	doc, ok := s.ensureDocBuiltLocked(p.TextDocument.URI)
	s.mu.Unlock()
	if !ok {
		return s.respond(id, nil)
	}

	node := nodeAtPosition(doc.root, p.Position)
	if node != nil && doc.env != nil && doc.lang != nil {
		typ, err := doc.env.ResolveAt(doc.root, node, doc.lang, doc.src)
		if err == nil && typ != nil {
			label := nodeTextAt(doc.src, hoverLabelNode(node, doc.lang))
			if label == "" {
				label = nodeTextAt(doc.src, node)
			}
			return s.respond(id, hoverResult{
				Contents: markupContent{Kind: "markdown", Value: fmt.Sprintf("```go\n%s %s\n```", label, typ.String())},
			})
		}
	}

	if doc.env != nil {
		word := wordAt(doc.text, p.Position)
		if word != "" {
			if symbol, ok := doc.env.FindSymbol(word); ok && symbol.Type != nil {
				return s.respond(id, hoverResult{
					Contents: markupContent{Kind: "markdown", Value: fmt.Sprintf("```go\n%s %s\n```", word, symbol.Type.String())},
				})
			}
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
	doc, ok := s.ensureDocBuiltLocked(uri)
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
	doc, ok := s.ensureDocBuiltLocked(uri)
	s.mu.Unlock()
	if !ok {
		return s.respond(id, nil)
	}

	word := wordAt(doc.text, pos)
	if word == "" {
		return s.respond(id, nil)
	}
	if doc.env != nil {
		if symbol, ok := doc.env.FindSymbol(word); ok {
			targetURI := uri
			if symbol.Location.File != "" {
				targetURI = uriForPath(symbol.Location.File)
			}
			return s.respond(id, locationResult{
				URI:   targetURI,
				Range: lspRangeFromSource(symbol.Location),
			})
		}
	}

	return s.respond(id, nil)
}

func (s *lspServer) documentSymbols(id *json.RawMessage, uri string) *jsonrpcMessage {
	s.mu.Lock()
	doc, ok := s.ensureDocBuiltLocked(uri)
	s.mu.Unlock()
	if !ok {
		return s.respond(id, []interface{}{})
	}

	var symbols []map[string]interface{}
	if doc.env != nil {
		for _, symbol := range doc.env.FindFileSymbols(doc.path) {
			symbols = append(symbols, map[string]interface{}{
				"name":           symbolLabel(symbol),
				"kind":           lspSymbolKind(symbol.Kind),
				"range":          lspRangeFromSource(symbol.Location),
				"selectionRange": lspRangeFromSource(symbol.Location),
			})
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

func wordAt(text string, pos position) string {
	lines := strings.Split(text, "\n")
	if pos.Line < 0 || pos.Line >= len(lines) {
		return ""
	}
	return wordAtPosition(lines[pos.Line], pos.Character)
}

func nodeAtPosition(root *gotreesitter.Node, pos position) *gotreesitter.Node {
	if root == nil {
		return nil
	}
	point := gotreesitter.Point{Row: uint32(pos.Line), Column: uint32(pos.Character)}
	cursor := gotreesitter.NewTreeCursor(root, nil)
	for {
		if cursor.GotoFirstChildForPoint(point) < 0 {
			break
		}
	}
	node := cursor.CurrentNode()
	for node != nil && !node.IsNamed() {
		node = node.Parent()
	}
	return node
}

func hoverLabelNode(node *gotreesitter.Node, lang *gotreesitter.Language) *gotreesitter.Node {
	if node == nil {
		return nil
	}
	parent := node.Parent()
	if parent == nil {
		return node
	}
	switch parent.Type(lang) {
	case "selector_expression", "safe_navigation":
		field := parent.ChildByFieldName("field", lang)
		if field != nil {
			return field
		}
	}
	return node
}

func nodeTextAt(src []byte, node *gotreesitter.Node) string {
	if node == nil {
		return ""
	}
	return string(src[node.StartByte():node.EndByte()])
}

func lspRangeFromSource(loc ferrouswheel.SourceRange) lspRange {
	startLine := loc.StartRow
	startCol := loc.StartCol
	endLine := loc.EndRow
	endCol := loc.EndCol
	if endLine < startLine || (endLine == startLine && endCol <= startCol) {
		endLine = startLine
		endCol = startCol + 1
	}
	return lspRange{
		Start: position{Line: startLine, Character: startCol},
		End:   position{Line: endLine, Character: endCol},
	}
}

func lspSymbolKind(kind ferrouswheel.SymbolKind) int {
	switch kind {
	case ferrouswheel.SymFunc:
		return 12
	case ferrouswheel.SymStruct:
		return 23
	case ferrouswheel.SymEnum:
		return 10
	case ferrouswheel.SymVar:
		return 13
	case ferrouswheel.SymConst:
		return 14
	case ferrouswheel.SymImpl:
		return 6
	default:
		return 13
	}
}

func symbolLabel(symbol ferrouswheel.SymbolInfo) string {
	return symbol.Name
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
