// Package policy checks selected source-level operations before a Ferrous Wheel
// build. It is a build policy, not a runtime sandbox. It does not follow
// symlinks, constrain dependencies, or intercept calls made through reflection.
package policy

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	gotreesitter "github.com/odvcencio/gotreesitter"
	ferrouswheel "m31labs.dev/ferrous-wheel"
)

// Config enables checks independently. A nil AllowedImports or FileRoots
// allows that category without restriction. An empty, nonnil list denies all
// imports or direct filesystem calls in that category. Import names match
// exactly. File roots are absolute paths for the machine that checks the code.
// Direct filesystem calls require literal absolute paths inside a listed root.
type Config struct {
	AllowedImports []string `json:"allowedImports"`
	DenyProcess    bool     `json:"denyProcess"`
	DenyNetwork    bool     `json:"denyNetwork"`
	FileRoots      []string `json:"fileRoots"`
}

// Diagnostic names a source location and the policy rule that denied it.
type Diagnostic struct {
	File    string
	Line    int
	Column  int
	Kind    string
	Message string
}

func (d Diagnostic) Error() string {
	return fmt.Sprintf("%s:%d:%d: policy: %s", d.File, d.Line, d.Column, d.Message)
}

// ParseConfig reads one strict JSON policy document. Unknown keys and trailing
// data are errors, so a misspelled rule cannot silently disable a check.
func ParseConfig(data []byte) (Config, error) {
	var config Config
	if len(bytes.TrimSpace(data)) == 0 || bytes.TrimSpace(data)[0] != '{' {
		return config, errors.New("policy: expected a JSON object")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	if _, err := decoder.Token(); err != nil {
		return Config{}, fmt.Errorf("policy: decode config: %w", err)
	}
	seen := make(map[string]bool)
	for decoder.More() {
		keyToken, err := decoder.Token()
		if err != nil {
			return Config{}, fmt.Errorf("policy: decode config key: %w", err)
		}
		key := keyToken.(string)
		if seen[key] {
			return Config{}, fmt.Errorf("policy: duplicate rule %q", key)
		}
		seen[key] = true
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return Config{}, fmt.Errorf("policy: decode rule %q: %w", key, err)
		}
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return Config{}, fmt.Errorf("policy: rule %q must not be null", key)
		}
		switch key {
		case "allowedImports":
			err = json.Unmarshal(raw, &config.AllowedImports)
		case "denyProcess":
			err = json.Unmarshal(raw, &config.DenyProcess)
		case "denyNetwork":
			err = json.Unmarshal(raw, &config.DenyNetwork)
		case "fileRoots":
			err = json.Unmarshal(raw, &config.FileRoots)
		default:
			return Config{}, fmt.Errorf("policy: unknown rule %q", key)
		}
		if err != nil {
			return Config{}, fmt.Errorf("policy: decode rule %q: %w", key, err)
		}
	}
	if _, err := decoder.Token(); err != nil {
		return Config{}, fmt.Errorf("policy: close config: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		if err == nil {
			return Config{}, errors.New("policy: trailing JSON value")
		}
		return Config{}, fmt.Errorf("policy: trailing data: %w", err)
	}
	if err := config.validate(); err != nil {
		return Config{}, err
	}
	return config, nil
}

func (config Config) validate() error {
	seen := make(map[string]bool)
	for _, name := range config.AllowedImports {
		if name == "" || strings.TrimSpace(name) != name || strings.ContainsAny(name, "\x00\\") || seen[name] {
			return fmt.Errorf("policy: invalid or duplicate allowed import %q", name)
		}
		seen[name] = true
	}
	clear(seen)
	for _, root := range config.FileRoots {
		if !filepath.IsAbs(root) || filepath.Clean(root) != root || seen[root] {
			return fmt.Errorf("policy: filesystem root must be unique, absolute, and clean: %q", root)
		}
		seen[root] = true
	}
	return nil
}

func (config Config) active() bool {
	return config.AllowedImports != nil || config.DenyProcess || config.DenyNetwork || config.FileRoots != nil
}

type sourceImport struct {
	path  string
	alias string
	node  *gotreesitter.Node
	deny  bool
	used  bool
}

// Check reports every visible policy violation in source order. It examines
// original source nodes before module resolution or code generation.
// Named imports and direct package calls are checked. Dynamic paths and
// indirect filesystem function references are denied when FileRoots is set.
// To constrain calls through other packages, use AllowedImports as an explicit
// allowlist. A successful check does not grant runtime confinement.
func Check(source []byte, sourceFile string, config Config) ([]Diagnostic, error) {
	if sourceFile == "" {
		return nil, errors.New("policy: source filename is required")
	}
	if err := config.validate(); err != nil {
		return nil, err
	}
	if !config.active() {
		return nil, nil
	}
	lang, err := ferrouswheel.GetFWLanguage()
	if err != nil {
		return nil, fmt.Errorf("policy: load language: %w", err)
	}
	tree, err := gotreesitter.NewParser(lang).Parse(source)
	if err != nil {
		return nil, fmt.Errorf("policy: parse source: %w", err)
	}
	root := tree.RootNode()
	if root == nil || root.HasError() {
		return nil, errors.New("policy: source has parse errors")
	}
	checker := sourceChecker{
		source: source, file: sourceFile, config: config, language: lang,
		imports: make(map[string]*sourceImport),
	}
	checker.walk(root, checker.collectImport)
	checker.walk(root, checker.checkSelector)
	checker.checkImportFallbacks()
	sort.SliceStable(checker.diagnostics, func(i, j int) bool {
		a, b := checker.diagnostics[i], checker.diagnostics[j]
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		if a.Column != b.Column {
			return a.Column < b.Column
		}
		return a.Kind < b.Kind
	})
	return checker.diagnostics, nil
}

type sourceChecker struct {
	source      []byte
	file        string
	config      Config
	language    *gotreesitter.Language
	imports     map[string]*sourceImport
	allImports  []*sourceImport
	diagnostics []Diagnostic
}

func (c *sourceChecker) walk(node *gotreesitter.Node, visit func(*gotreesitter.Node)) {
	if node == nil {
		return
	}
	visit(node)
	for i := 0; i < node.NamedChildCount(); i++ {
		c.walk(node.NamedChild(i), visit)
	}
}

func (c *sourceChecker) text(node *gotreesitter.Node) string {
	return string(c.source[node.StartByte():node.EndByte()])
}

func (c *sourceChecker) add(node *gotreesitter.Node, kind, message string) {
	point := node.StartPoint()
	c.diagnostics = append(c.diagnostics, Diagnostic{
		File: c.file, Line: int(point.Row) + 1, Column: int(point.Column) + 1,
		Kind: kind, Message: message,
	})
}

func (c *sourceChecker) collectImport(node *gotreesitter.Node) {
	if node.Type(c.language) != "import_spec" {
		return
	}
	pathNode := node.ChildByFieldName("path", c.language)
	if pathNode == nil {
		return
	}
	importPath, err := strconv.Unquote(c.text(pathNode))
	if err != nil {
		c.add(pathNode, "import", "invalid import path")
		return
	}
	alias := path.Base(importPath)
	if name := node.ChildByFieldName("name", c.language); name != nil {
		alias = c.text(name)
	}
	ref := &sourceImport{path: importPath, alias: alias, node: node}
	c.allImports = append(c.allImports, ref)
	if alias != "_" && alias != "." {
		c.imports[alias] = ref
	}
	if c.config.AllowedImports != nil && !contains(c.config.AllowedImports, importPath) {
		c.add(pathNode, "import", fmt.Sprintf("import %q is not allowed", importPath))
		ref.deny = true
	}
	if alias == "." && (c.config.DenyProcess || c.config.DenyNetwork || c.config.FileRoots != nil) && !ref.deny {
		c.add(node, "import", fmt.Sprintf("dot import %q cannot be checked", importPath))
		ref.deny = true
	}
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func (c *sourceChecker) checkSelector(node *gotreesitter.Node) {
	if node.Type(c.language) != "selector_expression" {
		return
	}
	operand := node.ChildByFieldName("operand", c.language)
	field := node.ChildByFieldName("field", c.language)
	if operand == nil || field == nil || operand.Type(c.language) != "identifier" {
		return
	}
	alias, method := c.text(operand), c.text(field)
	ref := c.imports[alias]
	if ref == nil || ref.deny {
		return
	}
	call := c.directCall(node)
	if c.config.DenyProcess && processSelector(ref.path, method) {
		action := "use"
		if call != nil {
			action = "call"
		}
		c.add(node, "process", fmt.Sprintf("process %s %s.%s is denied", action, alias, method))
		ref.used = true
		return
	}
	if c.config.DenyNetwork && networkImport(ref.path) && call != nil {
		c.add(node, "network", fmt.Sprintf("network call %s.%s is denied", alias, method))
		ref.used = true
		return
	}
	if c.config.FileRoots != nil {
		if pathArgs, ok := filePathArgs(ref.path, method); ok {
			if call == nil {
				c.add(node, "filesystem", fmt.Sprintf("indirect filesystem call %s.%s cannot be checked", alias, method))
				return
			}
			arguments := call.ChildByFieldName("arguments", c.language)
			for _, position := range pathArgs {
				if arguments == nil || position >= arguments.NamedChildCount() {
					c.add(node, "filesystem", fmt.Sprintf("filesystem call %s.%s has no path argument", alias, method))
					continue
				}
				c.checkPath(arguments.NamedChild(position), alias+"."+method)
			}
		}
	}
}

func (c *sourceChecker) directCall(selector *gotreesitter.Node) *gotreesitter.Node {
	parent := selector.Parent()
	if parent == nil || parent.Type(c.language) != "call_expression" {
		return nil
	}
	function := parent.ChildByFieldName("function", c.language)
	if function != nil && function.StartByte() == selector.StartByte() && function.EndByte() == selector.EndByte() {
		return parent
	}
	return nil
}

func (c *sourceChecker) checkPath(node *gotreesitter.Node, call string) {
	literal := c.text(node)
	value, err := strconv.Unquote(literal)
	if err != nil || (len(literal) > 0 && literal[0] != '"' && literal[0] != '`') || !filepath.IsAbs(value) {
		c.add(node, "filesystem", fmt.Sprintf("filesystem call %s requires a literal absolute path", call))
		return
	}
	clean := filepath.Clean(value)
	for _, root := range c.config.FileRoots {
		if inside(clean, root) {
			return
		}
	}
	c.add(node, "filesystem", fmt.Sprintf("filesystem path %q is outside allowed roots", value))
}

func inside(name, root string) bool {
	relative, err := filepath.Rel(root, name)
	if err != nil || filepath.IsAbs(relative) {
		return false
	}
	return relative == "." || (relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)))
}

func (c *sourceChecker) checkImportFallbacks() {
	for _, ref := range c.allImports {
		if ref.deny || ref.used {
			continue
		}
		if c.config.FileRoots != nil && rawSystemImport(ref.path) {
			c.add(ref.node, "filesystem", fmt.Sprintf("raw system import %q cannot be checked against file roots", ref.path))
			continue
		}
		if c.config.DenyNetwork && rawSystemImport(ref.path) {
			c.add(ref.node, "network", fmt.Sprintf("raw system import %q can bypass network checks", ref.path))
			continue
		}
		if c.config.DenyProcess && processOnlyImport(ref.path) {
			c.add(ref.node, "process", fmt.Sprintf("process-capable import %q is denied", ref.path))
			continue
		}
		if c.config.DenyNetwork && networkImport(ref.path) {
			c.add(ref.node, "network", fmt.Sprintf("network-capable import %q is denied", ref.path))
		}
	}
}

func processSelector(importPath, method string) bool {
	switch importPath {
	case "os/exec":
		return true
	case "os":
		return method == "StartProcess"
	case "syscall":
		return method == "Exec" || method == "ForkExec" || method == "StartProcess" || method == "Syscall"
	case "m31labs.dev/ferrous-wheel/ops":
		return method == "Run"
	default:
		return false
	}
}

func processOnlyImport(importPath string) bool {
	return importPath == "os/exec" || importPath == "syscall" ||
		strings.HasPrefix(importPath, "golang.org/x/sys/")
}

func rawSystemImport(importPath string) bool {
	return importPath == "syscall" || strings.HasPrefix(importPath, "golang.org/x/sys/")
}

func networkImport(importPath string) bool {
	return importPath == "net" || importPath == "net/http" || strings.HasPrefix(importPath, "net/http/") ||
		importPath == "net/rpc" || strings.HasPrefix(importPath, "net/rpc/") || importPath == "net/smtp" ||
		importPath == "crypto/tls" || importPath == "golang.org/x/net" ||
		strings.HasPrefix(importPath, "golang.org/x/net/") || importPath == "google.golang.org/grpc" ||
		strings.HasPrefix(importPath, "google.golang.org/grpc/")
}

func filePathArgs(importPath, method string) ([]int, bool) {
	switch importPath {
	case "os":
		switch method {
		case "Open", "OpenFile", "OpenRoot", "ReadFile", "WriteFile", "Create", "CreateTemp",
			"Mkdir", "MkdirAll", "MkdirTemp", "Remove", "RemoveAll", "Stat", "Lstat", "ReadDir",
			"Chdir", "Chmod", "Chown", "Lchown", "Chtimes", "Truncate", "DirFS":
			return []int{0}, true
		case "Rename", "Link", "Symlink":
			return []int{0, 1}, true
		}
	case "path/filepath":
		switch method {
		case "Walk", "WalkDir", "Glob", "EvalSymlinks":
			return []int{0}, true
		}
	case "io/ioutil":
		switch method {
		case "ReadFile", "WriteFile", "ReadDir", "TempDir", "TempFile":
			return []int{0}, true
		}
	case "m31labs.dev/ferrous-wheel/hostfs":
		if method == "Open" || method == "OpenReal" {
			return []int{0}, true
		}
	case "m31labs.dev/ferrous-wheel/ops":
		if method == "AppendFile" {
			return []int{0}, true
		}
	}
	return nil, false
}
