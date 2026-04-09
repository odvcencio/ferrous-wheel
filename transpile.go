package ferrouswheel

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	gotreesitter "github.com/odvcencio/gotreesitter"
)

var (
	fwLangOnce   sync.Once
	fwLangCached *gotreesitter.Language
	fwLangErr    error
)

// GetFWLanguage returns the cached ferrous-wheel tree-sitter language.
func GetFWLanguage() (*gotreesitter.Language, error) {
	return getFWLanguage()
}

func getFWLanguage() (*gotreesitter.Language, error) {
	fwLangOnce.Do(func() {
		fwLangCached, fwLangErr = GenerateLanguage(Grammar())
	})
	return fwLangCached, fwLangErr
}

func validateTopLevelOnly(root *gotreesitter.Node, lang *gotreesitter.Language, src []byte, env *TypeEnv) error {
	type validationState struct {
		functionDepth int
		inConcurrent  bool
		inBreaker     bool
		inThrottle    bool
	}

	text := func(n *gotreesitter.Node) string {
		return string(src[n.StartByte():n.EndByte()])
	}
	childByField := func(n *gotreesitter.Node, field string) *gotreesitter.Node {
		return n.ChildByFieldName(field, lang)
	}
	sanitizedText := func(n *gotreesitter.Node) string {
		return strings.Join(strings.Fields(text(n)), " ")
	}
	typeString := func(typ Type) string {
		if typ == nil {
			return "/* type */"
		}
		if u, ok := typ.(*UntypedConstType); ok {
			typ = u.Default()
		}
		if typ == nil {
			return "/* type */"
		}
		return typ.String()
	}
	resolveNodeType := func(n *gotreesitter.Node) Type {
		if env == nil || n == nil {
			return nil
		}
		typ, err := env.Resolve(n, lang, src)
		if err != nil {
			return nil
		}
		if u, ok := typ.(*UntypedConstType); ok {
			return u.Default()
		}
		return typ
	}
	expressionListTypes := func(exprList *gotreesitter.Node) []Type {
		if env == nil || exprList == nil {
			return nil
		}
		if exprList.NamedChildCount() == 1 {
			typ := resolveNodeType(exprList.NamedChild(0))
			if tuple, ok := typ.(*TupleType); ok {
				return tuple.Elems
			}
			if typ != nil {
				return []Type{typ}
			}
			return nil
		}
		types := make([]Type, 0, int(exprList.NamedChildCount()))
		for i := 0; i < int(exprList.NamedChildCount()); i++ {
			types = append(types, resolveNodeType(exprList.NamedChild(i)))
		}
		return types
	}
	expressionListNames := func(exprList *gotreesitter.Node) []string {
		if exprList == nil {
			return nil
		}
		var names []string
		for i := 0; i < int(exprList.NamedChildCount()); i++ {
			child := exprList.NamedChild(i)
			if child != nil && child.Type(lang) == "identifier" {
				names = append(names, text(child))
			}
		}
		return names
	}
	formatConcurrentRewrite := func(names []string, types []Type, assignment string) error {
		var b strings.Builder
		b.WriteString("concurrent block cannot contain variable declarations (they would race).\n")
		b.WriteString("Rewrite as:\n\n")
		for i, name := range names {
			typ := Type(nil)
			if i < len(types) {
				typ = types[i]
			}
			fmt.Fprintf(&b, "\tvar %s %s\n", name, typeString(typ))
		}
		b.WriteString("\tconcurrent {\n")
		fmt.Fprintf(&b, "\t\t%s\n", assignment)
		b.WriteString("\t}")
		return fmt.Errorf("%s", b.String())
	}
	concurrentDeclError := func(n *gotreesitter.Node) error {
		switch n.Type(lang) {
		case "let_declaration":
			nameNode := childByField(n, "name")
			valueNode := childByField(n, "value")
			if nameNode != nil && valueNode != nil {
				return formatConcurrentRewrite(
					[]string{text(nameNode)},
					[]Type{resolveNodeType(n)},
					fmt.Sprintf("%s = %s", text(nameNode), sanitizedText(valueNode)),
				)
			}
		case "let_multi_declaration":
			valueNode := childByField(n, "value")
			if valueNode != nil {
				var names []string
				for i := 0; i < int(n.NamedChildCount()); i++ {
					child := n.NamedChild(i)
					switch child.Type(lang) {
					case "identifier":
						names = append(names, text(child))
					case "let_typed_binding":
						if nameNode := childByField(child, "name"); nameNode != nil {
							names = append(names, text(nameNode))
						}
					}
				}
				if len(names) > 0 {
					return formatConcurrentRewrite(
						names,
						expressionListTypes(valueNode),
						fmt.Sprintf("%s = %s", strings.Join(names, ", "), sanitizedText(valueNode)),
					)
				}
			}
		case "short_var_declaration":
			left := childByField(n, "left")
			right := childByField(n, "right")
			names := expressionListNames(left)
			if len(names) > 0 && right != nil {
				return formatConcurrentRewrite(
					names,
					expressionListTypes(right),
					fmt.Sprintf("%s = %s", strings.Join(names, ", "), sanitizedText(right)),
				)
			}
		case "var_declaration":
			return fmt.Errorf("concurrent block cannot contain variable declarations (they would race).\nMove this var declaration before the concurrent block.")
		case "const_declaration":
			return fmt.Errorf("concurrent block cannot contain declarations inside the block body.\nMove this const declaration before the concurrent block.")
		}
		return fmt.Errorf("%s is not supported inside concurrent blocks; predeclare variables outside the block", n.Type(lang))
	}

	var walk func(n *gotreesitter.Node, state validationState) error
	walk = func(n *gotreesitter.Node, state validationState) error {
		if n == nil {
			return nil
		}

		nodeType := n.Type(lang)
		if state.functionDepth > 0 {
			switch nodeType {
			case "enum_declaration", "derive_declaration", "impl_block":
				return fmt.Errorf("%s must appear at top level", nodeType)
			}
		}

		if state.inConcurrent {
			switch nodeType {
			case "let_declaration", "let_multi_declaration", "short_var_declaration", "var_declaration", "const_declaration":
				return concurrentDeclError(n)
			}
		}

		if state.inBreaker && nodeType == "return_statement" {
			return fmt.Errorf("return_statement is not supported inside breaker blocks")
		}

		if state.inThrottle {
			switch nodeType {
			case "for_statement", "for_in_statement", "for_in_index_statement", "repeat_statement", "until_statement":
				return fmt.Errorf("%s is not supported inside throttle blocks; throttle currently gates block entry, not each loop iteration", nodeType)
			}
		}

		nextState := state
		switch nodeType {
		case "function_declaration", "method_declaration", "func_literal", "lambda_expression":
			nextState.functionDepth++
		case "concurrent_block":
			nextState.inConcurrent = true
		case "breaker_block":
			nextState.inBreaker = true
		case "throttle_block":
			nextState.inThrottle = true
		}

		for i := 0; i < int(n.NamedChildCount()); i++ {
			if err := walk(n.NamedChild(i), nextState); err != nil {
				return err
			}
		}
		return nil
	}

	return walk(root, validationState{})
}

func validateTryUsage(root *gotreesitter.Node, lang *gotreesitter.Language, src []byte) error {
	text := func(n *gotreesitter.Node) string {
		return string(src[n.StartByte():n.EndByte()])
	}

	resultTypes := func(resultNode *gotreesitter.Node) []string {
		if resultNode == nil {
			return nil
		}
		if resultNode.Type(lang) != "parameter_list" {
			return []string{text(resultNode)}
		}

		var types []string
		for i := 0; i < int(resultNode.NamedChildCount()); i++ {
			param := resultNode.NamedChild(i)
			if param == nil || param.Type(lang) != "parameter_declaration" {
				continue
			}
			typeNode := param.ChildByFieldName("type", lang)
			if typeNode == nil {
				continue
			}

			nameCount := 0
			for j := 0; j < int(param.ChildCount()); j++ {
				if param.FieldNameForChild(j, lang) == "name" {
					nameCount++
				}
			}
			if nameCount == 0 {
				nameCount = 1
			}

			typ := text(typeNode)
			for range nameCount {
				types = append(types, typ)
			}
		}
		return types
	}

	trySiteSupported := func(n *gotreesitter.Node) bool {
		parent := n.Parent()
		if parent == nil {
			return false
		}

		switch parent.Type(lang) {
		case "let_declaration", "let_multi_declaration":
			return parent.ChildByFieldName("value", lang) == n
		case "expression_statement":
			// postfix_try (expr?) is allowed as a standalone expression statement
			return n.Type(lang) == "postfix_try"
		case "expression_list":
			site := parent.Parent()
			if site == nil {
				return false
			}
			switch site.Type(lang) {
			case "short_var_declaration":
				right := site.ChildByFieldName("right", lang)
				return right == parent && right.NamedChildCount() == 1 && right.NamedChild(0) == n
			case "assignment_statement":
				right := site.ChildByFieldName("right", lang)
				if right == nil || right != parent || right.NamedChildCount() != 1 || right.NamedChild(0) != n {
					return false
				}
				for i := 0; i < int(site.ChildCount()); i++ {
					if site.FieldNameForChild(i, lang) == "operator" {
						return text(site.Child(i)) == "="
					}
				}
			}
		}
		return false
	}

	validateTryNode := func(n *gotreesitter.Node) error {
		isPostfix := n.Type(lang) == "postfix_try"
		expr := n.ChildByFieldName("expr", lang)
		if expr == nil || expr.Type(lang) != "call_expression" {
			if isPostfix {
				return fmt.Errorf("? currently only supports direct call expressions")
			}
			return fmt.Errorf("try currently only supports direct call expressions")
		}
		if !trySiteSupported(n) {
			if isPostfix {
				return fmt.Errorf("? is only supported on the right-hand side of let, tuple let, :=, or = assignments, or as a standalone expression statement")
			}
			return fmt.Errorf("try is only supported on the right-hand side of let, tuple let, :=, or = assignments")
		}

		for cur := n.Parent(); cur != nil; cur = cur.Parent() {
			switch cur.Type(lang) {
			case "retry_block":
				return nil
			case "lambda_expression":
				return fmt.Errorf("try is not supported inside lambda expressions")
			case "concurrent_block", "breaker_block", "fan_out_block":
				return fmt.Errorf("try is not supported directly inside %s; wrap the call in retry or handle the error explicitly", cur.Type(lang))
			case "function_declaration", "method_declaration", "func_literal":
				types := resultTypes(cur.ChildByFieldName("result", lang))
				if len(types) == 0 || types[len(types)-1] != "error" {
					return fmt.Errorf("try requires the enclosing %s to return a trailing error", cur.Type(lang))
				}
				return nil
			}
		}

		return fmt.Errorf("try must appear inside an error-returning function, method, func literal, or retry block")
	}

	var walk func(n *gotreesitter.Node) error
	walk = func(n *gotreesitter.Node) error {
		if n == nil {
			return nil
		}
		if n.Type(lang) == "error_propagation" || n.Type(lang) == "postfix_try" {
			if err := validateTryNode(n); err != nil {
				return err
			}
		}
		for i := 0; i < int(n.NamedChildCount()); i++ {
			if err := walk(n.NamedChild(i)); err != nil {
				return err
			}
		}
		return nil
	}

	return walk(root)
}

func validateMemoryUsage(root *gotreesitter.Node, lang *gotreesitter.Language, src []byte) error {
	text := func(n *gotreesitter.Node) string {
		return string(src[n.StartByte():n.EndByte()])
	}

	var walk func(n *gotreesitter.Node) error
	walk = func(n *gotreesitter.Node) error {
		if n == nil {
			return nil
		}

		if n.Type(lang) == "mmap_block" {
			typeNode := n.ChildByFieldName("type", lang)
			if typeNode == nil || text(typeNode) != "[]byte" {
				return fmt.Errorf("mmap_block currently requires []byte target type")
			}
		}

		for i := 0; i < int(n.NamedChildCount()); i++ {
			if err := walk(n.NamedChild(i)); err != nil {
				return err
			}
		}
		return nil
	}

	return walk(root)
}

func validateNoUnparsedText(root *gotreesitter.Node, src []byte) error {
	if root == nil {
		return nil
	}

	// gotreesitter can omit some inner tokens from custom DSL nodes (for example
	// `impl` method names and `defer!` prefixes), so only validate uncovered text
	// between top-level siblings where recovery garbage like `package mx n` appears.
	prev := uint32(0)
	for i := 0; i < root.ChildCount(); i++ {
		child := root.Child(i)
		if child == nil || child.EndByte() <= child.StartByte() {
			continue
		}
		if child.StartByte() > prev {
			if len(bytes.TrimSpace(src[prev:child.StartByte()])) > 0 {
				return fmt.Errorf("parse errors in ferrous-wheel source")
			}
		}
		if child.EndByte() > prev {
			prev = child.EndByte()
		}
	}
	if uint32(len(src)) > prev {
		if len(bytes.TrimSpace(src[prev:])) > 0 {
			return fmt.Errorf("parse errors in ferrous-wheel source")
		}
	}
	return nil
}

// Transpile converts .fw source to valid Go code.
// TranspileOptions configures the transpilation process.
type TranspileOptions struct {
	SourceFile string // original .fw filename for //line directives
	LintRan    bool   // true if lint already ran; skip duplicate checks (e.g., if-expr else)
}

// Warning is a non-fatal issue encountered during transpilation.
type Warning struct {
	Line    int
	Col     int
	Message string
}

// Transpile converts .fw source to valid Go code.
func Transpile(source []byte) (string, error) {
	goCode, _, err := TranspileWithOptions(source, TranspileOptions{})
	return goCode, err
}

// TranspileWithOptions converts .fw source to valid Go code with configurable options.
func TranspileWithOptions(source []byte, opts TranspileOptions) (string, []Warning, error) {
	lang, err := getFWLanguage()
	if err != nil {
		return "", nil, fmt.Errorf("generate ferrous-wheel language: %w", err)
	}

	parser := gotreesitter.NewParser(lang)
	tree, err := parser.Parse(source)
	if err != nil {
		return "", nil, fmt.Errorf("parse: %w", err)
	}

	root := tree.RootNode()
	if root.HasError() {
		return "", nil, fmt.Errorf("parse errors in ferrous-wheel source")
	}

	var env *TypeEnv
	if collected, collectErr := collectTypes(source); collectErr == nil {
		_ = collected.LoadCollectedImports(findModuleDir(opts.SourceFile))
		env = collected
	}
	if err := validateNoUnparsedText(root, source); err != nil {
		return "", nil, err
	}
	if err := validateTopLevelOnly(root, lang, source, env); err != nil {
		return "", nil, err
	}
	if err := validateTryUsage(root, lang, source); err != nil {
		return "", nil, err
	}
	if err := validateMemoryUsage(root, lang, source); err != nil {
		return "", nil, err
	}

	sourceLabel := opts.SourceFile
	if sourceLabel != "" {
		sourceLabel = filepath.Base(sourceLabel)
	}
	var inferCtx *InferenceContext
	if env != nil {
		inferCtx = NewInferenceContext()
		env.InferCtx = inferCtx
	}
	t := &fwTranspiler{src: source, lang: lang, sourceFile: sourceLabel, typeEnv: env, inferCtx: inferCtx, lintRan: opts.LintRan}

	result := t.emit(root)

	// Check for accumulated transpile errors (e.g., immutable binding violations)
	if len(t.transpileErrors) > 0 {
		return "", t.warnings, t.transpileErrors[0]
	}

	// Detect Result[T] and Option[T] usage in the transpiled output
	t.detectGenericTypes(result)

	result = t.injectImports(result)
	result = t.injectGenericTypes(result)
	result = t.injectSupportCode(result)
	return result, t.warnings, nil
}

type fwTranspiler struct {
	src             []byte
	lang            *gotreesitter.Language
	sourceFile      string // original .fw filename for //line directives
	typeEnv         *TypeEnv
	inferCtx        *InferenceContext
	warnings        []Warning
	transpileErrors []error
	needsReflect    bool
	needsFmt        bool
	needsJSON       bool
	needsResultType bool
	needsOptionType bool
	needsUnsafe     bool
	needsRuntime    bool
	needsOS         bool
	needsSyscall    bool
	needsSync       bool
	needsTime       bool
	needsBreaker    bool
	needsThrottle   bool
	needsFanIn      bool
	needsUnsafeCast    bool
	needsSlog          bool   // log/slog import
	needsContext       bool   // context import (for slog.Log with trace level)
	needsLogHelper     bool   // pretty-print handler + init (defines _fwLevelTrace const)
	needsColorHelper   bool   // fwcolor() helper
	needsTimeBlock     bool   // tree-drawing prefix helper
	needsFlagHelper    bool   // auto-injected -v/--quiet flags
	logConfigLevel     string // from log.config! directive
	logConfigFormat    string // from log.config! directive
	logConfigTime      string // from log.config! directive
	timeBlockCounter   int    // unique var names for time blocks
	lintRan            bool   // true if lint already ran; skip duplicate checks
	implReceiver    string // non-empty when inside an impl block
	lastPipeValue   string // set by emitPipeline for selector reconstruction
	tryTargets      []tryTarget
	tryCounter      int
}

type tryTargetKind int

const (
	tryTargetFunction tryTargetKind = iota
	tryTargetRetry
)

type tryTarget struct {
	kind        tryTargetKind
	returnTypes []string
}

func (t *fwTranspiler) text(n *gotreesitter.Node) string {
	return string(t.src[n.StartByte():n.EndByte()])
}

func (t *fwTranspiler) nodeType(n *gotreesitter.Node) string {
	return n.Type(t.lang)
}

func (t *fwTranspiler) childByField(n *gotreesitter.Node, field string) *gotreesitter.Node {
	return n.ChildByFieldName(field, t.lang)
}

func (t *fwTranspiler) warningExprText(n *gotreesitter.Node) string {
	if n == nil {
		return "<expr>"
	}
	text := strings.Join(strings.Fields(t.text(n)), " ")
	if len(text) > 40 {
		return text[:37] + "..."
	}
	if text == "" {
		return "<expr>"
	}
	return text
}

func (t *fwTranspiler) addWarning(n *gotreesitter.Node, message string) string {
	if n != nil {
		pos := n.StartPoint()
		t.warnings = append(t.warnings, Warning{
			Line:    int(pos.Row) + 1,
			Col:     int(pos.Column) + 1,
			Message: message,
		})
	} else {
		t.warnings = append(t.warnings, Warning{Message: message})
	}
	return message
}

func (t *fwTranspiler) withTypeScope(register func(), emit func() string) string {
	if t.typeEnv == nil {
		return emit()
	}
	t.typeEnv.PushScope()
	defer t.typeEnv.PopScope()
	if register != nil {
		register()
	}
	return emit()
}

func (t *fwTranspiler) emitScopedNode(n *gotreesitter.Node, register func()) string {
	if n == nil {
		return "{}"
	}
	return t.withTypeScope(register, func() string {
		return t.emit(n)
	})
}

func (t *fwTranspiler) normalizeBindingType(typ Type) Type {
	if u, ok := typ.(*UntypedConstType); ok {
		return u.Default()
	}
	return typ
}

func (t *fwTranspiler) resolvedType(n *gotreesitter.Node) Type {
	if t.typeEnv == nil || n == nil {
		return nil
	}
	typ, err := t.typeEnv.Resolve(n, t.lang, t.src)
	if err != nil {
		return nil
	}
	return t.normalizeBindingType(typ)
}

func (t *fwTranspiler) resolvedTypeExpr(n *gotreesitter.Node) Type {
	if t.typeEnv == nil || n == nil {
		return nil
	}
	typ, err := t.typeEnv.resolveTypeExpr(n, t.lang, t.src)
	if err != nil {
		return nil
	}
	return t.normalizeBindingType(typ)
}

func (t *fwTranspiler) registerBinding(name string, typ Type) {
	if t.typeEnv == nil || name == "" {
		return
	}
	typ = t.normalizeBindingType(typ)
	if typ == nil {
		return
	}
	t.typeEnv.RegisterVar(name, typ)
}

// registerLetBinding registers a let binding with mutability tracking.
// Names starting with _fw are compiler-generated temporaries and bypass tracking.
func (t *fwTranspiler) registerLetBinding(name string, typ Type, mutable bool) {
	t.registerBinding(name, typ)
	if t.typeEnv != nil && name != "" && !strings.HasPrefix(name, "_fw") {
		t.typeEnv.scope.mutable[name] = mutable
	}
}

// registerLetBindings registers multiple let bindings with mutability tracking.
func (t *fwTranspiler) registerLetBindings(names []string, explicitTypes []Type, exprList *gotreesitter.Node, mutable bool) {
	if t.typeEnv == nil || len(names) == 0 {
		return
	}
	resolved := t.resolveExpressionListTypes(exprList)
	for i, name := range names {
		var typ Type
		if i < len(explicitTypes) && explicitTypes[i] != nil {
			typ = explicitTypes[i]
		} else if i < len(resolved) {
			typ = resolved[i]
		}
		t.registerLetBinding(name, typ, mutable)
	}
}

func (t *fwTranspiler) registerParameterList(params *gotreesitter.Node) {
	if t.typeEnv == nil || params == nil {
		return
	}
	for i := 0; i < int(params.NamedChildCount()); i++ {
		param := params.NamedChild(i)
		if param == nil {
			continue
		}
		switch t.nodeType(param) {
		case "parameter_declaration":
			paramType := t.resolvedTypeExpr(t.childByField(param, "type"))
			if paramType == nil {
				continue
			}
			for j := 0; j < int(param.ChildCount()); j++ {
				if param.FieldNameForChild(j, t.lang) != "name" {
					continue
				}
				nameNode := param.Child(j)
				if nameNode != nil {
					t.registerBinding(t.text(nameNode), paramType)
				}
			}
		case "variadic_parameter_declaration":
			nameNode := t.childByField(param, "name")
			elemType := t.resolvedTypeExpr(t.childByField(param, "type"))
			if nameNode == nil || elemType == nil {
				continue
			}
			t.registerBinding(t.text(nameNode), &SliceType{Elem: elemType})
		}
	}
}

func (t *fwTranspiler) resolveExpressionListTypes(exprList *gotreesitter.Node) []Type {
	if t.typeEnv == nil || exprList == nil {
		return nil
	}
	if exprList.NamedChildCount() == 1 {
		typ := t.resolvedType(exprList.NamedChild(0))
		if tuple, ok := typ.(*TupleType); ok {
			out := make([]Type, 0, len(tuple.Elems))
			for _, elem := range tuple.Elems {
				out = append(out, t.normalizeBindingType(elem))
			}
			return out
		}
		if typ != nil {
			return []Type{typ}
		}
		return nil
	}

	types := make([]Type, 0, int(exprList.NamedChildCount()))
	for i := 0; i < int(exprList.NamedChildCount()); i++ {
		types = append(types, t.resolvedType(exprList.NamedChild(i)))
	}
	return types
}

func (t *fwTranspiler) registerBindings(names []string, explicitTypes []Type, exprList *gotreesitter.Node) {
	if t.typeEnv == nil || len(names) == 0 {
		return
	}
	resolved := t.resolveExpressionListTypes(exprList)
	for i, name := range names {
		var typ Type
		if i < len(explicitTypes) && explicitTypes[i] != nil {
			typ = explicitTypes[i]
		} else if i < len(resolved) {
			typ = resolved[i]
		}
		t.registerBinding(name, typ)
	}
}

func (t *fwTranspiler) identifierNames(exprList *gotreesitter.Node) []string {
	if exprList == nil {
		return nil
	}
	var names []string
	for i := 0; i < int(exprList.NamedChildCount()); i++ {
		child := exprList.NamedChild(i)
		if child != nil && t.nodeType(child) == "identifier" {
			names = append(names, t.text(child))
		}
	}
	return names
}

func (t *fwTranspiler) letMultiBindings(n *gotreesitter.Node) ([]string, []Type) {
	var (
		names []string
		types []Type
	)
	for i := 0; i < int(n.NamedChildCount()); i++ {
		child := n.NamedChild(i)
		switch t.nodeType(child) {
		case "identifier":
			names = append(names, t.text(child))
			types = append(types, nil)
		case "let_typed_binding":
			nameNode := t.childByField(child, "name")
			if nameNode == nil {
				continue
			}
			names = append(names, t.text(nameNode))
			types = append(types, t.resolvedTypeExpr(t.childByField(child, "type")))
		}
	}
	return names, types
}

func (t *fwTranspiler) rangeValueType(iterable *gotreesitter.Node) Type {
	if t.typeEnv == nil || iterable == nil {
		return nil
	}
	if t.nodeType(iterable) == "range_expression" {
		return Primitive("int")
	}
	iterType := t.resolvedType(iterable)
	switch it := iterType.(type) {
	case *SliceType:
		return it.Elem
	case *MapType:
		return it.Value
	case *ChanType:
		return it.Elem
	case Primitive:
		if it == Primitive("string") {
			return Primitive("rune")
		}
	}
	return nil
}

func (t *fwTranspiler) emitCallableScoped(n *gotreesitter.Node) string {
	return t.withTypeScope(func() {
		t.registerParameterList(t.childByField(n, "receiver"))
		t.registerParameterList(t.childByField(n, "parameters"))
		if result := t.childByField(n, "result"); result != nil && t.nodeType(result) == "parameter_list" {
			t.registerParameterList(result)
		}
	}, func() string {
		return t.emitCallableWithTryTarget(n)
	})
}

func (t *fwTranspiler) typeParameterDeclAndArgs(listNode *gotreesitter.Node) (decl, args string) {
	if listNode == nil {
		return "", ""
	}

	decl = t.text(listNode)

	var names []string
	for i := 0; i < int(listNode.NamedChildCount()); i++ {
		paramDecl := listNode.NamedChild(i)
		if paramDecl == nil || t.nodeType(paramDecl) != "type_parameter_declaration" {
			continue
		}
		for j := 0; j < int(paramDecl.NamedChildCount()); j++ {
			child := paramDecl.NamedChild(j)
			if child != nil && t.nodeType(child) == "identifier" {
				names = append(names, t.text(child))
			}
		}
	}
	if len(names) > 0 {
		args = "[" + strings.Join(names, ", ") + "]"
	}
	return decl, args
}

func (t *fwTranspiler) callableTryTarget(n *gotreesitter.Node) (tryTarget, bool) {
	resultNode := t.childByField(n, "result")
	returnTypes := t.resultTypes(resultNode)
	if len(returnTypes) == 0 || returnTypes[len(returnTypes)-1] != "error" {
		return tryTarget{}, false
	}
	return tryTarget{kind: tryTargetFunction, returnTypes: returnTypes}, true
}

func (t *fwTranspiler) resultTypes(resultNode *gotreesitter.Node) []string {
	if resultNode == nil {
		return nil
	}
	if t.nodeType(resultNode) != "parameter_list" {
		return []string{t.text(resultNode)}
	}

	var types []string
	for i := 0; i < int(resultNode.NamedChildCount()); i++ {
		param := resultNode.NamedChild(i)
		if param == nil || t.nodeType(param) != "parameter_declaration" {
			continue
		}
		typeNode := t.childByField(param, "type")
		if typeNode == nil {
			continue
		}

		nameCount := 0
		for j := 0; j < int(param.ChildCount()); j++ {
			if param.FieldNameForChild(j, t.lang) == "name" {
				nameCount++
			}
		}
		if nameCount == 0 {
			nameCount = 1
		}

		typ := t.text(typeNode)
		for range nameCount {
			types = append(types, typ)
		}
	}
	return types
}

func (t *fwTranspiler) withTryTarget(target tryTarget, emit func() string) string {
	t.tryTargets = append(t.tryTargets, target)
	defer func() {
		t.tryTargets = t.tryTargets[:len(t.tryTargets)-1]
	}()
	return emit()
}

func (t *fwTranspiler) currentTryTarget() (tryTarget, bool) {
	if len(t.tryTargets) == 0 {
		return tryTarget{}, false
	}
	return t.tryTargets[len(t.tryTargets)-1], true
}

func (t *fwTranspiler) nextTryErrName() string {
	name := fmt.Sprintf("_fwTryErr%d", t.tryCounter)
	t.tryCounter++
	return name
}

func (t *fwTranspiler) expressionListItems(listNode *gotreesitter.Node) []string {
	if listNode == nil {
		return nil
	}
	var items []string
	for i := 0; i < int(listNode.NamedChildCount()); i++ {
		items = append(items, t.emit(listNode.NamedChild(i)))
	}
	return items
}

func (t *fwTranspiler) singleTryFromExpressionList(listNode *gotreesitter.Node) *gotreesitter.Node {
	if listNode == nil || listNode.NamedChildCount() != 1 {
		return nil
	}
	child := listNode.NamedChild(0)
	if child == nil || (t.nodeType(child) != "error_propagation" && t.nodeType(child) != "postfix_try") {
		return nil
	}
	return child
}

func (t *fwTranspiler) tryReturnStatement(errName string) string {
	target, ok := t.currentTryTarget()
	if !ok {
		return fmt.Sprintf("panic(%s)", errName)
	}

	switch target.kind {
	case tryTargetRetry:
		return "return " + errName
	case tryTargetFunction:
		if len(target.returnTypes) == 1 {
			return "return " + errName
		}

		values := make([]string, 0, len(target.returnTypes))
		for i, typ := range target.returnTypes {
			if i == len(target.returnTypes)-1 {
				values = append(values, errName)
				continue
			}
			values = append(values, fmt.Sprintf("*new(%s)", typ))
		}
		return "return " + strings.Join(values, ", ")
	default:
		return fmt.Sprintf("panic(%s)", errName)
	}
}

func (t *fwTranspiler) emitTryAssignment(lhs []string, op string, tryNode *gotreesitter.Node) string {
	expr := t.childByField(tryNode, "expr")
	if expr == nil || len(lhs) == 0 {
		return t.text(tryNode)
	}

	errName := t.nextTryErrName()
	callText := t.emit(expr)
	var b strings.Builder

	switch op {
	case ":=":
		fmt.Fprintf(&b, "%s, %s := %s\n", strings.Join(lhs, ", "), errName, callText)
	case "=":
		fmt.Fprintf(&b, "var %s error\n", errName)
		fmt.Fprintf(&b, "%s, %s = %s\n", strings.Join(lhs, ", "), errName, callText)
	default:
		return t.text(tryNode)
	}

	fmt.Fprintf(&b, "if %s != nil {\n\t%s\n}", errName, t.tryReturnStatement(errName))
	return b.String()
}

func (t *fwTranspiler) emitCallableWithTryTarget(n *gotreesitter.Node) string {
	if target, ok := t.callableTryTarget(n); ok {
		return t.withTryTarget(target, func() string {
			return t.emitDefault(n)
		})
	}
	return t.emitDefault(n)
}

func (t *fwTranspiler) emit(n *gotreesitter.Node) string {
	switch t.nodeType(n) {
	case "enum_declaration":
		return t.emitEnum(n)
	case "let_declaration":
		return t.emitLet(n)
	case "let_multi_declaration":
		return t.emitLetMulti(n)
	case "ternary_expression":
		return t.emitTernary(n)
	case "match_expression":
		return t.emitMatch(n)
	case "null_coalesce":
		return t.emitNullCoalesce(n)
	case "error_propagation":
		return t.emitErrorProp(n)
	case "postfix_try":
		return t.emitPostfixTry(n)
	case "safe_navigation":
		return t.emitSafeNav(n)
	case "lambda_expression":
		return t.emitLambda(n)
	case "call_expression":
		return t.emitCall(n)
	case "method_declaration":
		return t.emitMethodDecl(n)
	case "func_literal":
		return t.emitFuncLiteral(n)
	case "derive_declaration":
		return t.emitDerive(n)
	case "if_let_statement":
		return t.emitIfLet(n)
	case "range_expression":
		return t.emitRange(n)
	case "for_in_statement":
		return t.emitForIn(n)
	case "for_in_index_statement":
		return t.emitForInIndex(n)
	case "fstring":
		return t.emitFString(n)
	case "guard_statement":
		return t.emitGuard(n)
	case "defer_error":
		return t.emitDeferError(n)
	case "impl_block":
		return t.emitImplBlock(n)
	case "unless_statement":
		return t.emitUnless(n)
	case "until_statement":
		return t.emitUntil(n)
	case "repeat_statement":
		return t.emitRepeat(n)
	case "list_comprehension":
		return t.emitListComprehension(n)
	case "swap_statement":
		return t.emitSwap(n)
	case "function_declaration":
		return t.emitFunctionDecl(n)
	case "var_declaration":
		return t.emitVarDecl(n)
	case "const_declaration":
		return t.emitConstDecl(n)
	case "short_var_declaration":
		return t.emitShortVarDecl(n)
	case "assignment_statement":
		return t.emitAssignment(n)
	case "block":
		return t.emitBlock(n)
	// Low-level features
	case "arena_block":
		return t.emitArena(n)
	case "pin_statement":
		return t.emitPin(n)
	case "unpin_statement":
		return t.emitUnpin(n)
	case "unsafe_cast":
		return t.emitUnsafeCast(n)
	case "mmap_block":
		return t.emitMmap(n)
	case "packed_annotation":
		return t.emitPacked(n)
	case "vectorize_statement":
		return t.emitVectorize(n)
	// Concurrency features
	case "select_block":
		return t.emitSelectBlock(n)
	case "fan_out_block":
		return t.emitFanOut(n)
	case "fan_in_expression":
		return t.emitFanIn(n)
	case "pipeline_expression":
		return t.emitPipeline(n)
	case "selector_expression":
		return t.emitSelectorExpression(n)
	case "concurrent_block":
		return t.emitConcurrent(n)
	case "throttle_block":
		return t.emitThrottle(n)
	case "retry_block":
		return t.emitRetry(n)
	case "breaker_block":
		return t.emitBreaker(n)
	// Logging
	case "log_statement":
		return t.emitLogStatement(n)
	case "color_call":
		return t.emitColorCall(n)
	case "log_with_block":
		return t.emitLogWithBlock(n)
	case "log_time_block":
		return t.emitLogTimeBlock(n)
	case "log_config":
		return t.emitLogConfig(n)
	case "if_statement":
		if t.isExpressionPosition(n) {
			return t.emitIfExpression(n)
		}
		return t.emitDefault(n)
	default:
		return t.emitDefault(n)
	}
}

func (t *fwTranspiler) emitDefault(n *gotreesitter.Node) string {
	cc := int(n.ChildCount())
	if cc == 0 {
		return t.text(n)
	}
	var b strings.Builder
	prev := n.StartByte()
	for i := 0; i < cc; i++ {
		c := n.Child(i)
		if c.StartByte() > prev {
			b.Write(t.src[prev:c.StartByte()])
		}
		b.WriteString(t.emit(c))
		prev = c.EndByte()
	}
	if n.EndByte() > prev {
		b.Write(t.src[prev:n.EndByte()])
	}
	return b.String()
}

func (t *fwTranspiler) emitBlock(n *gotreesitter.Node) string {
	return t.withTypeScope(nil, func() string {
		return t.emitDefault(n)
	})
}

// enum Color { Red, Green, Blue(int) }
// -> type Color struct { tag int; blueVal0 int }
//
//	const (ColorRed = 0; ColorGreen = 1; ColorBlue = 2)
//	func Red() Color { return Color{tag: 0} }
//	func Green() Color { return Color{tag: 1} }
//	func Blue(v0 int) Color { return Color{tag: 2, blue0: v0} }
func (t *fwTranspiler) emitEnum(n *gotreesitter.Node) string {
	directive := t.emitLineDirective(n)
	name := "Enum"
	if nameNode := t.childByField(n, "name"); nameNode != nil {
		name = t.text(nameNode)
	}
	typeParamsDecl, typeParamsArgs := t.typeParameterDeclAndArgs(t.childByField(n, "type_parameters"))

	// Collect variants
	type variant struct {
		name  string
		types []string
	}
	var variants []variant
	for i := 0; i < int(n.NamedChildCount()); i++ {
		c := n.NamedChild(i)
		if t.nodeType(c) == "enum_variant" {
			v := variant{}
			if vname := t.childByField(c, "name"); vname != nil {
				v.name = t.text(vname)
			}
			// Collect type params (all named children except the name identifier)
			for j := 0; j < int(c.NamedChildCount()); j++ {
				tc := c.NamedChild(j)
				if t.nodeType(tc) != "identifier" {
					v.types = append(v.types, t.text(tc))
				}
			}
			variants = append(variants, v)
		}
	}

	var b strings.Builder

	// Struct with tag + variant fields
	fmt.Fprintf(&b, "type %s%s struct {\n\ttag int\n", name, typeParamsDecl)
	for _, v := range variants {
		for j, typ := range v.types {
			fmt.Fprintf(&b, "\t%s%d %s\n", strings.ToLower(v.name), j, typ)
		}
	}
	b.WriteString("}\n\n")

	// Constants
	b.WriteString("const (\n")
	for i, v := range variants {
		fmt.Fprintf(&b, "\t%s%s = %d\n", name, v.name, i)
	}
	b.WriteString(")\n\n")

	// Constructor functions
	for i, v := range variants {
		if len(v.types) == 0 {
			fmt.Fprintf(&b, "func %s%s() %s%s { return %s%s{tag: %d} }\n",
				v.name, typeParamsDecl, name, typeParamsArgs, name, typeParamsArgs, i)
		} else {
			params := make([]string, len(v.types))
			args := make([]string, len(v.types))
			for j, typ := range v.types {
				params[j] = fmt.Sprintf("v%d %s", j, typ)
				args[j] = fmt.Sprintf("%s%d: v%d", strings.ToLower(v.name), j, j)
			}
			fmt.Fprintf(&b, "func %s%s(%s) %s%s { return %s%s{tag: %d, %s} }\n",
				v.name, typeParamsDecl, strings.Join(params, ", "), name, typeParamsArgs, name, typeParamsArgs, i, strings.Join(args, ", "))
		}
	}

	return directive + b.String()
}

// let x = 1 -> x := 1
func (t *fwTranspiler) emitLet(n *gotreesitter.Node) string {
	nameNode := t.childByField(n, "name")
	value := t.childByField(n, "value")
	if nameNode == nil || value == nil {
		return t.text(n)
	}
	name := t.text(nameNode)
	isMut := t.childByField(n, "mutable") != nil

	if t.nodeType(value) == "error_propagation" || t.nodeType(value) == "postfix_try" {
		out := t.emitTryAssignment([]string{name}, ":=", value)
		t.registerLetBinding(name, t.resolvedType(n), isMut)
		return out
	}
	out := fmt.Sprintf("%s := %s", name, t.emit(value))
	t.registerLetBinding(name, t.resolvedType(n), isMut)
	return out
}

// let (a, b) = f() -> a, b := f()
func (t *fwTranspiler) emitLetMulti(n *gotreesitter.Node) string {
	value := t.childByField(n, "value")
	if value == nil {
		return t.text(n)
	}

	names, explicitTypes := t.letMultiBindings(n)
	if len(names) == 0 {
		return t.text(n)
	}
	isMut := t.childByField(n, "mutable") != nil

	if t.nodeType(value) == "error_propagation" || t.nodeType(value) == "postfix_try" {
		out := t.emitTryAssignment(names, ":=", value)
		t.registerLetBindings(names, explicitTypes, value, isMut)
		return out
	}

	out := fmt.Sprintf("%s := %s", strings.Join(names, ", "), t.emit(value))
	t.registerLetBindings(names, explicitTypes, value, isMut)
	return out
}

// cond ? trueVal : falseVal -> func() interface{} { if cond { return trueVal }; return falseVal }()
func (t *fwTranspiler) emitTernary(n *gotreesitter.Node) string {
	cond := t.childByField(n, "condition")
	cons := t.childByField(n, "consequence")
	alt := t.childByField(n, "alternative")
	if cond == nil || cons == nil || alt == nil {
		return t.text(n)
	}

	// Typed path: resolve both arms and unify to avoid interface{}
	if t.typeEnv != nil {
		consType, err1 := t.typeEnv.Resolve(cons, t.lang, t.src)
		altType, err2 := t.typeEnv.Resolve(alt, t.lang, t.src)
		if err1 == nil && err2 == nil {
			unified, uerr := UnifyWithContext(t.inferCtx, consType, altType)
			if uerr == nil {
				if u, ok := unified.(*UntypedConstType); ok {
					unified = u.Default()
				}
				return fmt.Sprintf("func() %s { if %s { return %s }; return %s }()",
					unified.String(), t.emit(cond), t.emit(cons), t.emit(alt))
			}
		}
	}

	// fallback: type resolution failed
	return fmt.Sprintf("func() interface{} { if %s { return %s }; return %s }()",
		t.emit(cond), t.emit(cons), t.emit(alt))
}

// isExpressionPosition checks whether the given node is used in an expression
// context (RHS of let, short_var_declaration, assignment, return, call argument).
func (t *fwTranspiler) isExpressionPosition(n *gotreesitter.Node) bool {
	parent := n.Parent()
	if parent == nil {
		return false
	}
	switch t.nodeType(parent) {
	case "let_declaration", "let_multi_declaration":
		return t.childByField(parent, "value") == n
	case "short_var_declaration":
		return true
	case "return_statement":
		return true
	case "call_expression":
		return true
	case "expression_list":
		gp := parent.Parent()
		if gp != nil {
			switch t.nodeType(gp) {
			case "short_var_declaration", "assignment_statement", "return_statement":
				return true
			}
		}
	}
	return false
}

// containsNodeType walks the subtree rooted at n and returns true if any
// descendant (including n itself) has the given node type.
func (t *fwTranspiler) containsNodeType(n *gotreesitter.Node, nodeType string) bool {
	if t.nodeType(n) == nodeType {
		return true
	}
	for i := 0; i < int(n.ChildCount()); i++ {
		if t.containsNodeType(n.Child(i), nodeType) {
			return true
		}
	}
	return false
}

// blockLastExpr returns the last expression node from a block's statement_list.
// If the block is empty or the last statement is not an expression_statement
// (or another expression-capable node like if_statement), it returns nil.
func (t *fwTranspiler) blockLastExpr(block *gotreesitter.Node) *gotreesitter.Node {
	nc := int(block.NamedChildCount())
	for i := nc - 1; i >= 0; i-- {
		c := block.NamedChild(i)
		if t.nodeType(c) == "statement_list" {
			return t.blockLastExpr(c)
		}
		if t.nodeType(c) == "expression_statement" {
			if c.NamedChildCount() > 0 {
				return c.NamedChild(0)
			}
			return c
		}
		// An if_statement at the end of a block is also a valid tail expression
		// (it will be recursively transpiled as an if-expression IIFE)
		if t.nodeType(c) == "if_statement" {
			return c
		}
		// last child is a statement, not an expression
		return nil
	}
	return nil
}

// emitIfExpression transpiles an if_statement that appears in expression position
// as an IIFE (Immediately Invoked Function Expression):
//
//	let x = if cond { a } else { b }
//	→ x := func() TYPE { if cond { return a }; return b }()
func (t *fwTranspiler) emitIfExpression(n *gotreesitter.Node) string {
	// Validate that else branch exists by walking the entire if-chain
	t.validateIfExprElse(n)
	if len(t.transpileErrors) > 0 {
		return t.text(n)
	}

	// Validate no error_propagation inside branches (would return from IIFE, not enclosing function)
	t.validateIfExprNoTry(n)
	if len(t.transpileErrors) > 0 {
		return t.text(n)
	}

	// Validate that each branch ends with an expression
	t.validateIfExprBranches(n)
	if len(t.transpileErrors) > 0 {
		return t.text(n)
	}

	// Collect branch expression types for unification
	returnType := "interface{}"
	if t.typeEnv != nil {
		branchTypes := t.collectIfExprBranchTypes(n)
		if len(branchTypes) > 0 {
			unified := branchTypes[0]
			allOk := true
			for _, bt := range branchTypes[1:] {
				u, uerr := UnifyWithContext(t.inferCtx, unified, bt)
				if uerr != nil {
					allOk = false
					break
				}
				unified = u
			}
			if allOk && unified != nil {
				if u, ok := unified.(*UntypedConstType); ok {
					unified = u.Default()
				}
				returnType = unified.String()
			}
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "func() %s { ", returnType)
	t.emitIfExprBody(&b, n)
	b.WriteString(" }()")
	return b.String()
}

// validateIfExprElse walks an if-expression chain and errors if any terminal
// branch is missing an else clause.
func (t *fwTranspiler) validateIfExprElse(n *gotreesitter.Node) {
	// When lint has already run, it owns the missing-else-if-expr diagnostic.
	// Skip the transpiler's duplicate check.
	if t.lintRan {
		return
	}
	alt := t.childByField(n, "alternative")
	if alt == nil {
		t.transpileErrors = append(t.transpileErrors, fmt.Errorf(
			"if-expression requires an else branch"))
		return
	}
	// If alternative is another if_statement (else-if), recurse
	if t.nodeType(alt) == "if_statement" {
		t.validateIfExprElse(alt)
	}
}

// validateIfExprNoTry checks that no branch contains error_propagation or postfix_try nodes.
func (t *fwTranspiler) validateIfExprNoTry(n *gotreesitter.Node) {
	cons := t.childByField(n, "consequence")
	if cons != nil && (t.containsNodeType(cons, "error_propagation") || t.containsNodeType(cons, "postfix_try")) {
		t.transpileErrors = append(t.transpileErrors, fmt.Errorf(
			"if-expression branch must not contain try (error propagation would return from the IIFE, not the enclosing function)"))
		return
	}
	alt := t.childByField(n, "alternative")
	if alt == nil {
		return
	}
	if t.nodeType(alt) == "if_statement" {
		t.validateIfExprNoTry(alt)
	} else if t.containsNodeType(alt, "error_propagation") || t.containsNodeType(alt, "postfix_try") {
		t.transpileErrors = append(t.transpileErrors, fmt.Errorf(
			"if-expression branch must not contain try (error propagation would return from the IIFE, not the enclosing function)"))
	}
}

// validateIfExprBranches checks that each branch block ends with an expression.
func (t *fwTranspiler) validateIfExprBranches(n *gotreesitter.Node) {
	cons := t.childByField(n, "consequence")
	if cons != nil && t.blockLastExpr(cons) == nil {
		t.transpileErrors = append(t.transpileErrors, fmt.Errorf(
			"if-expression branch must end with an expression, not a statement"))
		return
	}
	alt := t.childByField(n, "alternative")
	if alt == nil {
		return
	}
	if t.nodeType(alt) == "if_statement" {
		t.validateIfExprBranches(alt)
	} else if t.blockLastExpr(alt) == nil {
		t.transpileErrors = append(t.transpileErrors, fmt.Errorf(
			"if-expression branch must end with an expression, not a statement"))
	}
}

// collectIfExprBranchTypes collects the types of all branch tail expressions.
func (t *fwTranspiler) collectIfExprBranchTypes(n *gotreesitter.Node) []Type {
	var types []Type
	cons := t.childByField(n, "consequence")
	if cons != nil {
		if expr := t.blockLastExpr(cons); expr != nil {
			if bt, err := t.typeEnv.Resolve(expr, t.lang, t.src); err == nil {
				types = append(types, bt)
			}
		}
	}
	alt := t.childByField(n, "alternative")
	if alt == nil {
		return types
	}
	if t.nodeType(alt) == "if_statement" {
		types = append(types, t.collectIfExprBranchTypes(alt)...)
	} else {
		if expr := t.blockLastExpr(alt); expr != nil {
			if bt, err := t.typeEnv.Resolve(expr, t.lang, t.src); err == nil {
				types = append(types, bt)
			}
		}
	}
	return types
}

// emitExprValue emits a node as an expression value. If the node is an
// if_statement, it forces emission as an if-expression IIFE regardless of
// the node's position in the tree.
func (t *fwTranspiler) emitExprValue(n *gotreesitter.Node) string {
	if t.nodeType(n) == "if_statement" {
		return t.emitIfExpression(n)
	}
	return t.emit(n)
}

// emitIfExprBody writes the if/else-if/else chain body inside the IIFE.
// For a simple if/else:  if COND { return EXPR }; return EXPR
// For else-if chains:    if COND { return EXPR } else if COND { return EXPR }; return EXPR
func (t *fwTranspiler) emitIfExprBody(b *strings.Builder, n *gotreesitter.Node) {
	cond := t.childByField(n, "condition")
	cons := t.childByField(n, "consequence")
	alt := t.childByField(n, "alternative")

	if cond == nil || cons == nil {
		b.WriteString(t.text(n))
		return
	}

	// Emit the consequence block: extract preceding statements and the tail expression
	consExpr := t.blockLastExpr(cons)
	consPrefix := t.emitBlockWithoutLast(cons)

	if alt == nil {
		// Should not happen (validated earlier), but fallback
		fmt.Fprintf(b, "if %s { %sreturn %s }", t.emit(cond), consPrefix, t.emitExprValue(consExpr))
		return
	}

	if t.nodeType(alt) == "if_statement" {
		// else-if chain: emit current branch, then recurse
		fmt.Fprintf(b, "if %s { %sreturn %s } else ", t.emit(cond), consPrefix, t.emitExprValue(consExpr))
		t.emitIfExprBody(b, alt)
	} else {
		// Terminal else block
		altExpr := t.blockLastExpr(alt)
		altPrefix := t.emitBlockWithoutLast(alt)
		if altPrefix == "" {
			// Simple case: single expression in both branches
			fmt.Fprintf(b, "if %s { %sreturn %s }; %sreturn %s",
				t.emit(cond), consPrefix, t.emitExprValue(consExpr), altPrefix, t.emitExprValue(altExpr))
		} else {
			fmt.Fprintf(b, "if %s { %sreturn %s } else { %sreturn %s }",
				t.emit(cond), consPrefix, t.emitExprValue(consExpr), altPrefix, t.emitExprValue(altExpr))
		}
	}
}

// emitBlockWithoutLast emits all statements in a block except the last one
// (which is the tail expression for if-expressions). Returns empty string if
// the block only has one statement.
func (t *fwTranspiler) emitBlockWithoutLast(block *gotreesitter.Node) string {
	// Find the statement_list child
	var stmtList *gotreesitter.Node
	for i := 0; i < int(block.NamedChildCount()); i++ {
		c := block.NamedChild(i)
		if t.nodeType(c) == "statement_list" {
			stmtList = c
			break
		}
	}
	if stmtList == nil {
		return ""
	}
	nc := int(stmtList.NamedChildCount())
	if nc <= 1 {
		return ""
	}
	var b strings.Builder
	for i := 0; i < nc-1; i++ {
		c := stmtList.NamedChild(i)
		b.WriteString(t.emit(c))
		b.WriteString("; ")
	}
	return b.String()
}

// match val { 1 => "one", 2 => "two" }
// -> func() interface{} { switch val { case 1: return "one"; case 2: return "two" } }()
func (t *fwTranspiler) emitMatch(n *gotreesitter.Node) string {
	subject := t.childByField(n, "subject")
	if subject == nil {
		return t.text(n)
	}

	// Typed path: resolve and unify all arm body types
	returnType := "interface{}"
	if t.typeEnv != nil {
		var armTypes []Type
		allResolved := true
		for i := 0; i < int(n.NamedChildCount()); i++ {
			c := n.NamedChild(i)
			if t.nodeType(c) == "match_arm" {
				body := t.childByField(c, "body")
				if body != nil {
					bt, err := t.typeEnv.Resolve(body, t.lang, t.src)
					if err != nil {
						allResolved = false
						break
					}
					armTypes = append(armTypes, bt)
				}
			}
		}
		if allResolved && len(armTypes) > 0 {
			unified := armTypes[0]
			for _, at := range armTypes[1:] {
				u, uerr := UnifyWithContext(t.inferCtx, unified, at)
				if uerr != nil {
					unified = nil
					break
				}
				unified = u
			}
			if unified != nil {
				if u, ok := unified.(*UntypedConstType); ok {
					unified = u.Default()
				}
				returnType = unified.String()
			}
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "func() %s { switch %s {\n", returnType, t.emit(subject))

	for i := 0; i < int(n.NamedChildCount()); i++ {
		c := n.NamedChild(i)
		if t.nodeType(c) == "match_arm" {
			pattern := t.childByField(c, "pattern")
			guard := t.childByField(c, "guard")
			body := t.childByField(c, "body")
			if pattern != nil && body != nil {
				if guard != nil {
					// match x { n if n > 0 => "positive" }
					// -> case n: if n > 0 { return "positive" }
					fmt.Fprintf(&b, "case %s:\n\tif %s {\n\t\treturn %s\n\t}\n",
						t.emit(pattern), t.emit(guard), t.emit(body))
				} else {
					fmt.Fprintf(&b, "case %s:\n\treturn %s\n", t.emit(pattern), t.emit(body))
				}
			}
		}
	}

	b.WriteString("default:\n\tpanic(fmt.Sprintf(\"non-exhaustive match: no arm matched value %v\", ")
	b.WriteString(t.emit(subject))
	b.WriteString("))\n}\n}()")
	t.needsFmt = true
	return b.String()
}

// val ?? "default" -> zero-value coalescing (works for ALL types)
func (t *fwTranspiler) emitNullCoalesce(n *gotreesitter.Node) string {
	left := t.childByField(n, "left")
	right := t.childByField(n, "right")
	if left == nil || right == nil {
		return t.text(n)
	}

	// Typed path: resolve left type, use type-specific zero check
	if t.typeEnv != nil {
		leftType, lerr := t.typeEnv.Resolve(left, t.lang, t.src)
		rightType, rerr := t.typeEnv.Resolve(right, t.lang, t.src)
		// Try unifying left and right to get a concrete type even when one side is untyped
		resolvedType := leftType
		if lerr == nil && rerr == nil {
			if unified, uerr := UnifyWithContext(t.inferCtx, leftType, rightType); uerr == nil {
				resolvedType = unified
			}
		}
		if lerr == nil && resolvedType != nil {
			if u, ok := resolvedType.(*UntypedConstType); ok {
				resolvedType = u.Default()
			}
			zeroExpr, zerr := ZeroExpr(resolvedType)
			if zerr == nil {
				l := t.emit(left)
				return fmt.Sprintf("func() %s { if %s != %s { return %s }; return %s }()",
					resolvedType.String(), l, zeroExpr, l, t.emit(right))
			}
		}
	}

	// fallback: type resolution failed
	t.needsReflect = true
	l := t.emit(left)
	warn := t.addWarning(n, fmt.Sprintf("type of '%s' unresolved for ?? - using reflection fallback; add type annotations to avoid this", t.warningExprText(left)))
	return fmt.Sprintf(`func() interface{} {
		// fw:warn: %s
		_v := reflect.ValueOf(%s)
		if _v.IsValid() && !_v.IsZero() { return %s }
		return %s
	}()`, warn, l, l, t.emit(right))
}

// try lowering is handled at the statement site after validation.
func (t *fwTranspiler) emitErrorProp(n *gotreesitter.Node) string {
	return t.text(n)
}

// emitPostfixTry handles standalone postfix_try (expr?) when it appears as a bare
// expression statement rather than inside a let/assignment. It generates a temporary
// to capture the value and error, then checks and propagates the error.
func (t *fwTranspiler) emitPostfixTry(n *gotreesitter.Node) string {
	expr := t.childByField(n, "expr")
	if expr == nil {
		return t.text(n)
	}

	errName := t.nextTryErrName()
	callText := t.emit(expr)
	var b strings.Builder
	fmt.Fprintf(&b, "_fwVal, %s := %s\n", errName, callText)
	fmt.Fprintf(&b, "if %s != nil {\n\t%s\n}\n", errName, t.tryReturnStatement(errName))
	b.WriteString("_ = _fwVal")
	return b.String()
}

func (t *fwTranspiler) emitCall(n *gotreesitter.Node) string {
	return t.emitDefault(n)
}

// obj?.field -> nil-safe field access
func (t *fwTranspiler) emitSafeNav(n *gotreesitter.Node) string {
	obj := t.childByField(n, "object")
	field := t.childByField(n, "field")
	if obj == nil || field == nil {
		return t.text(n)
	}

	// Typed path: resolve object type and field type for direct access
	if t.typeEnv != nil {
		objType, err := t.typeEnv.Resolve(obj, t.lang, t.src)
		if err == nil {
			// Apply inference context substitutions in case objType contains TypeVars
			if t.inferCtx != nil {
				objType = t.inferCtx.Apply(objType)
			}
			fieldName := t.text(field)
			fieldType, ferr := t.typeEnv.ResolveFieldAccess(objType, fieldName)
			if ferr == nil {
				fieldType = t.normalizeBindingType(fieldType)
				o := t.emit(obj)
				// Check if object is a pointer type — needs nil check
				if _, isPtr := objType.(*PointerType); isPtr {
					zeroExpr, zerr := ZeroExpr(fieldType)
					if zerr != nil {
						zeroExpr = fmt.Sprintf("*new(%s)", fieldType.String())
					}
					return fmt.Sprintf("func() %s { if %s == nil { return %s }; return %s.%s }()",
						fieldType.String(), o, zeroExpr, o, fieldName)
				}
				// Non-pointer struct: direct access, no nil check needed
				return fmt.Sprintf("%s.%s", o, fieldName)
			}
		}
	}

	// fallback: type resolution failed
	t.needsReflect = true
	o := t.emit(obj)
	f := t.text(field)
	warn := t.addWarning(n, fmt.Sprintf("type of '%s' unresolved for ?. - using reflection fallback; add type annotations to avoid this", t.warningExprText(obj)))
	return fmt.Sprintf(`func() interface{} {
		// fw:warn: %s
		_o := any(%s)
		_v := reflect.ValueOf(_o)
		if !_v.IsValid() { return nil }
		for _v.Kind() == reflect.Interface || _v.Kind() == reflect.Pointer {
			if _v.IsNil() { return nil }
			_v = _v.Elem()
		}
		if _v.Kind() != reflect.Struct { return nil }
		_f := _v.FieldByName(%q)
		if !_f.IsValid() { return nil }
		return _f.Interface()
	}()`, warn, o, f)
}

// fn(x, y) body -> func literal
func (t *fwTranspiler) emitLambda(n *gotreesitter.Node) string {
	params := t.childByField(n, "params")
	body := t.childByField(n, "body")
	if params == nil || body == nil {
		return t.text(n)
	}

	return t.withTypeScope(func() {
		for i := 0; i < int(params.NamedChildCount()); i++ {
			c := params.NamedChild(i)
			if t.nodeType(c) != "lambda_typed_param" {
				continue
			}
			nameNode := t.childByField(c, "name")
			typeNode := t.childByField(c, "type")
			if nameNode == nil || typeNode == nil {
				continue
			}
			t.registerBinding(t.text(nameNode), t.resolvedTypeExpr(typeNode))
		}
	}, func() string {
		// Typed path: check for lambda_typed_param nodes and return_type field
		if t.typeEnv != nil {
			hasTypedParams := false
			for i := 0; i < int(params.NamedChildCount()); i++ {
				if t.nodeType(params.NamedChild(i)) == "lambda_typed_param" {
					hasTypedParams = true
					break
				}
			}

			if hasTypedParams {
				var b strings.Builder
				b.WriteString("func(")
				first := true
				for i := 0; i < int(params.NamedChildCount()); i++ {
					c := params.NamedChild(i)
					if t.nodeType(c) == "lambda_typed_param" {
						nameNode := t.childByField(c, "name")
						typeNode := t.childByField(c, "type")
						if nameNode != nil && typeNode != nil {
							if !first {
								b.WriteString(", ")
							}
							first = false
							fmt.Fprintf(&b, "%s %s", t.text(nameNode), t.text(typeNode))
						}
					} else if t.nodeType(c) == "identifier" {
						if !first {
							b.WriteString(", ")
						}
						first = false
						fmt.Fprintf(&b, "%s interface{}", t.text(c))
					}
				}
				b.WriteString(")")

				// Check for return type annotation
				retType := t.childByField(n, "return_type")
				if retType != nil {
					fmt.Fprintf(&b, " %s ", t.text(retType))
				} else {
					// Try to resolve return type from body
					lambdaType, err := t.typeEnv.Resolve(n, t.lang, t.src)
					if err == nil {
						if t.inferCtx != nil {
							lambdaType = t.inferCtx.Apply(lambdaType)
						}
						if ft, ok := lambdaType.(*FuncType); ok && len(ft.Results) > 0 {
							rt := t.normalizeBindingType(ft.Results[0])
							if rt != nil {
								fmt.Fprintf(&b, " %s ", rt.String())
							} else {
								b.WriteString(" interface{} ")
							}
						} else {
							b.WriteString(" interface{} ")
						}
					} else {
						b.WriteString(" interface{} ")
					}
				}

				bodyText := t.emit(body)
				if t.nodeType(body) == "block" {
					b.WriteString(bodyText)
				} else {
					fmt.Fprintf(&b, "{ return %s }", bodyText)
				}
				return b.String()
			}
		}

		// fallback: type resolution failed — untyped params
		var paramNames []string
		for i := 0; i < int(params.NamedChildCount()); i++ {
			c := params.NamedChild(i)
			if t.nodeType(c) == "identifier" {
				paramNames = append(paramNames, t.text(c))
			}
		}

		// Try to infer lambda type from call context (bidirectional inference).
		// If the lambda is an argument to a function with a known signature,
		// use the expected parameter type to type the lambda's params.
		if expectedFT := t.inferLambdaTypeFromCallContext(n, len(paramNames)); expectedFT != nil {
			var b strings.Builder
			b.WriteString("func(")
			for i, p := range paramNames {
				if i > 0 {
					b.WriteString(", ")
				}
				if i < len(expectedFT.Params) {
					t.registerBinding(p, expectedFT.Params[i])
					fmt.Fprintf(&b, "%s %s", p, expectedFT.Params[i].String())
				} else {
					fmt.Fprintf(&b, "%s interface{}", p)
				}
			}
			b.WriteString(")")
			if len(expectedFT.Results) > 0 {
				fmt.Fprintf(&b, " %s ", expectedFT.Results[0].String())
			} else {
				b.WriteString(" ")
			}
			bodyText := t.emit(body)
			if t.nodeType(body) == "block" {
				b.WriteString(bodyText)
			} else {
				fmt.Fprintf(&b, "{ return %s }", bodyText)
			}
			return b.String()
		}

		// Build Go func literal with interface{} params
		var b strings.Builder
		b.WriteString("func(")
		for i, p := range paramNames {
			if i > 0 {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "%s interface{}", p)
		}
		b.WriteString(") interface{} ")

		bodyText := t.emit(body)
		if t.nodeType(body) == "block" {
			b.WriteString(bodyText)
		} else {
			fmt.Fprintf(&b, "{ return %s }", bodyText)
		}

		return b.String()
	})
}

// inferLambdaTypeFromCallContext checks if a lambda node is an argument to a function
// call and, if so, returns the expected FuncType for that argument position. This enables
// bidirectional type inference: the callee's parameter type flows down into the lambda.
func (t *fwTranspiler) inferLambdaTypeFromCallContext(lambdaNode *gotreesitter.Node, paramCount int) *FuncType {
	if t.typeEnv == nil {
		return nil
	}

	// Walk up: lambda -> argument_list -> call_expression
	argList := lambdaNode.Parent()
	if argList == nil {
		return nil
	}
	callExpr := argList.Parent()
	if callExpr == nil || t.nodeType(callExpr) != "call_expression" {
		// The parent might be the call_expression directly if the grammar
		// nests lambda as a direct child; try the argList itself.
		if t.nodeType(argList) == "call_expression" {
			callExpr = argList
			argList = nil
		} else {
			return nil
		}
	}

	// Resolve the callee's type
	callee := callExpr.ChildByFieldName("function", t.lang)
	if callee == nil {
		return nil
	}
	calleeType, err := t.typeEnv.Resolve(callee, t.lang, t.src)
	if err != nil {
		return nil
	}
	ft, ok := calleeType.(*FuncType)
	if !ok {
		return nil
	}

	// Find which argument position the lambda occupies
	argIdx := -1
	if argList != nil {
		namedIdx := 0
		for i := 0; i < int(argList.NamedChildCount()); i++ {
			child := argList.NamedChild(i)
			if child.StartByte() == lambdaNode.StartByte() && child.EndByte() == lambdaNode.EndByte() {
				argIdx = namedIdx
				break
			}
			namedIdx++
		}
	}
	if argIdx < 0 || argIdx >= len(ft.Params) {
		return nil
	}

	// The expected type at that position should be a FuncType
	expectedParam := ft.Params[argIdx]
	expectedFT, ok := expectedParam.(*FuncType)
	if !ok {
		return nil
	}

	// Verify arity matches
	if len(expectedFT.Params) != paramCount {
		return nil
	}

	return expectedFT
}

// emitFunctionDecl handles function_declaration, injecting receiver when inside impl block.
func (t *fwTranspiler) emitFunctionDecl(n *gotreesitter.Node) string {
	directive := t.emitLineDirective(n)
	text := t.emitCallableScoped(n)
	if t.implReceiver == "" {
		return directive + text
	}
	// Inside an impl block, add receiver to function declarations.
	// function_declaration: func name(params) returnType { body }
	// Transform to: func (self Type) name(params) returnType { body }
	if strings.HasPrefix(text, "func ") {
		return directive + "func (self " + t.implReceiver + ") " + text[5:]
	}
	return directive + text
}

func (t *fwTranspiler) emitMethodDecl(n *gotreesitter.Node) string {
	return t.emitCallableScoped(n)
}

func (t *fwTranspiler) emitFuncLiteral(n *gotreesitter.Node) string {
	return t.emitCallableScoped(n)
}

func (t *fwTranspiler) emitVarDecl(n *gotreesitter.Node) string {
	out := t.emitDefault(n)
	if t.typeEnv == nil {
		return out
	}
	for i := 0; i < int(n.NamedChildCount()); i++ {
		spec := n.NamedChild(i)
		if spec == nil || t.nodeType(spec) != "var_spec" {
			continue
		}
		valueNode := t.childByField(spec, "value")
		if valueNode == nil {
			continue
		}
		var names []string
		var explicit []Type
		for j := 0; j < int(spec.ChildCount()); j++ {
			if spec.FieldNameForChild(j, t.lang) != "name" {
				continue
			}
			nameNode := spec.Child(j)
			if nameNode == nil {
				continue
			}
			names = append(names, t.text(nameNode))
			explicit = append(explicit, t.resolvedTypeExpr(t.childByField(spec, "type")))
		}
		t.registerBindings(names, explicit, valueNode)
	}
	return out
}

func (t *fwTranspiler) emitConstDecl(n *gotreesitter.Node) string {
	out := t.emitDefault(n)
	if t.typeEnv == nil {
		return out
	}
	for i := 0; i < int(n.NamedChildCount()); i++ {
		spec := n.NamedChild(i)
		if spec == nil || t.nodeType(spec) != "const_spec" {
			continue
		}
		typeNode := t.childByField(spec, "type")
		explicitType := t.resolvedTypeExpr(typeNode)
		var (
			names    []string
			explicit []Type
		)
		for j := 0; j < int(spec.ChildCount()); j++ {
			if spec.FieldNameForChild(j, t.lang) != "name" {
				continue
			}
			nameNode := spec.Child(j)
			if nameNode == nil {
				continue
			}
			names = append(names, t.text(nameNode))
			explicit = append(explicit, explicitType)
		}
		t.registerBindings(names, explicit, t.childByField(spec, "value"))
	}
	return out
}

func (t *fwTranspiler) emitShortVarDecl(n *gotreesitter.Node) string {
	right := t.childByField(n, "right")
	tryNode := t.singleTryFromExpressionList(right)
	if tryNode == nil {
		out := t.emitDefault(n)
		t.registerBindings(t.identifierNames(t.childByField(n, "left")), nil, right)
		return out
	}

	lhs := t.expressionListItems(t.childByField(n, "left"))
	if len(lhs) == 0 {
		return t.emitDefault(n)
	}
	out := t.emitTryAssignment(lhs, ":=", tryNode)
	t.registerBindings(lhs, nil, right)
	return out
}

func (t *fwTranspiler) checkImmutableAssignment(n *gotreesitter.Node) {
	left := t.childByField(n, "left")
	if left == nil || t.typeEnv == nil {
		return
	}
	// Check each identifier in the LHS expression list
	for i := 0; i < int(left.NamedChildCount()); i++ {
		child := left.NamedChild(i)
		if child == nil {
			continue
		}
		if t.nodeType(child) == "identifier" {
			name := t.text(child)
			if t.typeEnv.scope.isLetBinding(name) && !t.typeEnv.scope.isMutable(name) {
				pos := child.StartPoint()
				t.transpileErrors = append(t.transpileErrors, fmt.Errorf(
					"line %d:%d: cannot assign to immutable binding '%s' -- use 'let mut' to make it mutable",
					pos.Row+1, pos.Column+1, name))
			}
		}
	}
	// Also check if LHS is a single identifier (not wrapped in expression_list)
	if t.nodeType(left) == "identifier" {
		name := t.text(left)
		if t.typeEnv.scope.isLetBinding(name) && !t.typeEnv.scope.isMutable(name) {
			pos := left.StartPoint()
			t.transpileErrors = append(t.transpileErrors, fmt.Errorf(
				"line %d:%d: cannot assign to immutable binding '%s' -- use 'let mut' to make it mutable",
				pos.Row+1, pos.Column+1, name))
		}
	}
}

func (t *fwTranspiler) emitAssignment(n *gotreesitter.Node) string {
	t.checkImmutableAssignment(n)

	right := t.childByField(n, "right")
	tryNode := t.singleTryFromExpressionList(right)
	if tryNode == nil {
		return t.emitDefault(n)
	}

	lhs := t.expressionListItems(t.childByField(n, "left"))
	if len(lhs) == 0 {
		return t.emitDefault(n)
	}
	return t.emitTryAssignment(lhs, "=", tryNode)
}

// derive Stringer for Color -> generate interface impl methods
func (t *fwTranspiler) emitDerive(n *gotreesitter.Node) string {
	traitNode := t.childByField(n, "trait")
	typeNode := t.childByField(n, "type")
	if traitNode == nil || typeNode == nil {
		return t.text(n)
	}
	trait := t.text(traitNode)
	typeName := t.text(typeNode)

	var b strings.Builder
	switch trait {
	case "Stringer":
		t.needsFmt = true
		fmt.Fprintf(&b, "func (x %s) String() string {\n", typeName)
		fmt.Fprintf(&b, "\treturn fmt.Sprintf(\"%s(%%v)\", x)\n", typeName)
		b.WriteString("}\n")
	case "JSON":
		t.needsJSON = true
		fmt.Fprintf(&b, "func (x %s) MarshalJSON() ([]byte, error) {\n", typeName)
		fmt.Fprintf(&b, "\ttype _fwJSONAlias %s\n", typeName)
		b.WriteString("\treturn json.Marshal(_fwJSONAlias(x))\n")
		b.WriteString("}\n")
		fmt.Fprintf(&b, "func (x *%s) UnmarshalJSON(data []byte) error {\n", typeName)
		fmt.Fprintf(&b, "\ttype _fwJSONAlias %s\n", typeName)
		b.WriteString("\tvar tmp _fwJSONAlias\n")
		b.WriteString("\tif err := json.Unmarshal(data, &tmp); err != nil {\n")
		b.WriteString("\t\treturn err\n")
		b.WriteString("\t}\n")
		fmt.Fprintf(&b, "\t*x = %s(tmp)\n", typeName)
		b.WriteString("\treturn nil\n")
		b.WriteString("}\n")
	case "Equal":
		fmt.Fprintf(&b, "func (x %s) Equal(other %s) bool {\n", typeName, typeName)
		fmt.Fprintf(&b, "\treturn x == other\n")
		b.WriteString("}\n")
	default:
		fmt.Fprintf(&b, "// derive %s for %s: unknown trait\n", trait, typeName)
	}
	return b.String()
}

// if let x = expr { body } -> if x := expr; x != nil { body }
func (t *fwTranspiler) emitIfLet(n *gotreesitter.Node) string {
	pattern := t.childByField(n, "pattern")
	value := t.childByField(n, "value")
	if pattern == nil || value == nil {
		return t.text(n)
	}
	varName := t.text(pattern)
	valExpr := t.emit(value)

	var b strings.Builder
	fmt.Fprintf(&b, "if %s := %s; %s != nil", varName, valExpr, varName)

	// Find the first block (then-block)
	blockFound := false
	for i := 0; i < int(n.NamedChildCount()); i++ {
		c := n.NamedChild(i)
		if t.nodeType(c) == "block" {
			if !blockFound {
				b.WriteString(" ")
				b.WriteString(t.emitScopedNode(c, func() {
					t.registerLetBinding(varName, t.resolvedType(value), false)
				}))
				blockFound = true
			} else {
				// else block
				b.WriteString(" else ")
				b.WriteString(t.emit(c))
			}
		}
	}

	return b.String()
}

// 0..10 -> (kept as-is; used by for_in to generate range loop)
func (t *fwTranspiler) emitRange(n *gotreesitter.Node) string {
	start := t.childByField(n, "start")
	end := t.childByField(n, "end")
	if start == nil || end == nil {
		return t.text(n)
	}
	// Range expression is primarily consumed by for_in; if standalone, emit as comment
	return fmt.Sprintf("/* range %s..%s */", t.emit(start), t.emit(end))
}

// for v in iterable { body }
func (t *fwTranspiler) emitForIn(n *gotreesitter.Node) string {
	varNode := t.childByField(n, "var")
	iterable := t.childByField(n, "iterable")
	if varNode == nil || iterable == nil {
		return t.text(n)
	}

	varName := t.text(varNode)
	blockNode := t.findBlockNode(n)
	block := t.emitScopedNode(blockNode, func() {
		t.registerBinding(varName, t.rangeValueType(iterable))
	})

	// Check if iterable is a range_expression (0..10)
	if t.nodeType(iterable) == "range_expression" {
		start := t.childByField(iterable, "start")
		end := t.childByField(iterable, "end")
		if start != nil && end != nil {
			if block != "" {
				return fmt.Sprintf("for %s := %s; %s < %s; %s++ %s",
					varName, t.emit(start), varName, t.emit(end), varName, block)
			}
		}
	}

	// General iterable: for _, v := range iterable { body }
	return fmt.Sprintf("for _, %s := range %s %s", varName, t.emit(iterable), block)
}

// for i, v in iterable { body }
func (t *fwTranspiler) emitForInIndex(n *gotreesitter.Node) string {
	indexNode := t.childByField(n, "index")
	varNode := t.childByField(n, "var")
	iterable := t.childByField(n, "iterable")
	if indexNode == nil || varNode == nil || iterable == nil {
		return t.text(n)
	}

	block := t.emitScopedNode(t.findBlockNode(n), func() {
		t.registerBinding(t.text(indexNode), Primitive("int"))
		t.registerBinding(t.text(varNode), t.rangeValueType(iterable))
	})
	return fmt.Sprintf("for %s, %s := range %s %s",
		t.text(indexNode), t.text(varNode), t.emit(iterable), block)
}

// findBlockNode finds the first block child node.
func (t *fwTranspiler) findBlockNode(n *gotreesitter.Node) *gotreesitter.Node {
	for i := 0; i < int(n.NamedChildCount()); i++ {
		c := n.NamedChild(i)
		if t.nodeType(c) == "block" {
			return c
		}
	}
	return nil
}

// findBlock finds the first block child node and emits it.
func (t *fwTranspiler) findBlock(n *gotreesitter.Node) string {
	block := t.findBlockNode(n)
	if block == nil {
		return "{}"
	}
	return t.emit(block)
}

func (t *fwTranspiler) channelElemType(n *gotreesitter.Node) Type {
	if ch, ok := t.resolvedType(n).(*ChanType); ok {
		return ch.Elem
	}
	return nil
}

// f"hello {name}" -> fmt.Sprintf("hello %v", name)
// emitFString handles f"hello {name}" -> fmt.Sprintf("hello %v", name)
// The fstring node is a single token matching f"...", so we parse the text directly.
func (t *fwTranspiler) emitFString(n *gotreesitter.Node) string {
	raw := t.text(n) // e.g. f"hello {name}"
	if len(raw) < 3 || raw[0] != 'f' || raw[1] != '"' {
		return raw
	}
	inner := raw[2 : len(raw)-1] // strip f" and trailing "

	t.needsFmt = true

	var format strings.Builder
	var args []string
	i := 0
	for i < len(inner) {
		if inner[i] == '{' {
			// Find matching }
			j := i + 1
			depth := 1
			for j < len(inner) && depth > 0 {
				if inner[j] == '{' {
					depth++
				} else if inner[j] == '}' {
					depth--
				}
				j++
			}
			expr := inner[i+1 : j-1]
			format.WriteString("%v")
			args = append(args, expr)
			i = j
		} else {
			format.WriteByte(inner[i])
			i++
		}
	}

	if len(args) == 0 {
		return `"` + inner + `"` // no interpolation, return as regular string
	}

	return fmt.Sprintf("fmt.Sprintf(\"%s\", %s)", format.String(), strings.Join(args, ", "))
}

// guard cond else { return err } -> if !(cond) { return err }
func (t *fwTranspiler) emitGuard(n *gotreesitter.Node) string {
	cond := t.childByField(n, "condition")
	body := t.childByField(n, "body")
	if cond == nil || body == nil {
		return t.text(n)
	}

	bodyText := t.emit(body)
	if t.nodeType(body) != "block" {
		bodyText = "{\n\t" + bodyText + "\n}"
	}
	return fmt.Sprintf("if !(%s) %s", t.emit(cond), bodyText)
}

// defer! f.Close() -> defer func() { if _cerr := f.Close(); _cerr != nil && err == nil { err = _cerr } }()
func (t *fwTranspiler) emitDeferError(n *gotreesitter.Node) string {
	expr := t.childByField(n, "expr")
	if expr == nil {
		return t.text(n)
	}
	e := t.emit(expr)
	return fmt.Sprintf("defer func() {\n\tif _cerr := %s; _cerr != nil && err == nil {\n\t\terr = _cerr\n\t}\n}()", e)
}

// impl Type { fn methods... } -> emit each function with (self Type) receiver
// Since Go's block rule parses `func Name()` as func_literal (not function_declaration),
// we extract the block text and perform string-level transformation.
func (t *fwTranspiler) emitImplBlock(n *gotreesitter.Node) string {
	directive := t.emitLineDirective(n)
	typeNode := t.childByField(n, "type")
	if typeNode == nil {
		return t.text(n)
	}

	typeName := t.text(typeNode)

	// Find the block child and get its text
	for i := 0; i < int(n.NamedChildCount()); i++ {
		c := n.NamedChild(i)
		if t.nodeType(c) == "block" {
			blockText := t.text(c)
			// Strip the outer { } braces
			if len(blockText) >= 2 && blockText[0] == '{' {
				blockText = blockText[1 : len(blockText)-1]
			}
			blockText = strings.TrimSpace(blockText)
			// Replace "func " with "func (self Type) " for each function in the block
			receiver := fmt.Sprintf("func (self %s) ", typeName)
			result := strings.ReplaceAll(blockText, "func ", receiver)
			return directive + result + "\n"
		}
	}

	return t.text(n)
}

// unless cond { body } -> if !(cond) { body }
func (t *fwTranspiler) emitUnless(n *gotreesitter.Node) string {
	cond := t.childByField(n, "condition")
	if cond == nil {
		return t.text(n)
	}
	block := t.findBlock(n)
	return fmt.Sprintf("if !(%s) %s", t.emit(cond), block)
}

// until cond { body } -> for !(cond) { body }
func (t *fwTranspiler) emitUntil(n *gotreesitter.Node) string {
	cond := t.childByField(n, "condition")
	if cond == nil {
		return t.text(n)
	}
	block := t.findBlock(n)
	return fmt.Sprintf("for !(%s) %s", t.emit(cond), block)
}

// repeat 5 { body } -> for _i := 0; _i < 5; _i++ { body }
func (t *fwTranspiler) emitRepeat(n *gotreesitter.Node) string {
	count := t.childByField(n, "count")
	if count == nil {
		return t.text(n)
	}
	block := t.findBlock(n)
	return fmt.Sprintf("for _i := 0; _i < %s; _i++ %s", t.emit(count), block)
}

// [x*2 for x in items if x > 0] -> IIFE with range + filter
func (t *fwTranspiler) emitListComprehension(n *gotreesitter.Node) string {
	expr := t.childByField(n, "expr")
	varNode := t.childByField(n, "var")
	iterable := t.childByField(n, "iterable")
	filter := t.childByField(n, "filter")
	if expr == nil || varNode == nil || iterable == nil {
		return t.text(n)
	}

	// Typed path: resolve iterable element type and expression type
	elemTypeStr := "interface{}"
	var elemType Type
	if t.typeEnv != nil {
		iterType, err := t.typeEnv.Resolve(iterable, t.lang, t.src)
		if err == nil {
			// Apply inference context substitutions to resolve TypeVars
			if t.inferCtx != nil {
				iterType = t.inferCtx.Apply(iterType)
			}
			if sliceType, ok := iterType.(*SliceType); ok {
				elemType = sliceType.Elem
				// Register the loop variable's type in a temporary scope
				t.typeEnv.PushScope()
				t.typeEnv.RegisterVar(t.text(varNode), elemType)
				exprType, exprErr := t.typeEnv.Resolve(expr, t.lang, t.src)
				t.typeEnv.PopScope()
				if exprErr == nil {
					if t.inferCtx != nil {
						exprType = t.inferCtx.Apply(exprType)
					}
					exprType = t.normalizeBindingType(exprType)
					if exprType != nil {
						elemTypeStr = exprType.String()
					}
				}
			}
		}
	}

	varName := t.text(varNode)
	filterText := ""
	exprText := ""
	if elemType != nil {
		if filter != nil {
			filterText = t.withTypeScope(func() {
				t.registerBinding(varName, elemType)
			}, func() string {
				return t.emit(filter)
			})
		}
		exprText = t.withTypeScope(func() {
			t.registerBinding(varName, elemType)
		}, func() string {
			return t.emit(expr)
		})
	} else {
		if filter != nil {
			filterText = t.emit(filter)
		}
		exprText = t.emit(expr)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "func() []%s {\n", elemTypeStr)
	fmt.Fprintf(&b, "\tvar _result []%s\n", elemTypeStr)
	fmt.Fprintf(&b, "\tfor _, %s := range %s {\n", varName, t.emit(iterable))
	if filter != nil {
		fmt.Fprintf(&b, "\t\tif %s {\n", filterText)
		fmt.Fprintf(&b, "\t\t\t_result = append(_result, %s)\n", exprText)
		fmt.Fprintf(&b, "\t\t}\n")
	} else {
		fmt.Fprintf(&b, "\t\t_result = append(_result, %s)\n", exprText)
	}
	fmt.Fprintf(&b, "\t}\n")
	fmt.Fprintf(&b, "\treturn _result\n")
	fmt.Fprintf(&b, "}()")

	return b.String()
}

// swap(a, b) -> a, b = b, a
func (t *fwTranspiler) emitSwap(n *gotreesitter.Node) string {
	a := t.childByField(n, "a")
	b := t.childByField(n, "b")
	if a == nil || b == nil {
		return t.text(n)
	}
	return fmt.Sprintf("%s, %s = %s, %s", t.emit(a), t.emit(b), t.emit(b), t.emit(a))
}
