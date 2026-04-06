package ferrouswheel

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	gotreesitter "github.com/odvcencio/gotreesitter"
)

// CollectTypes parses FW/Go source and walks the CST to register all
// top-level declarations (functions, structs, enums, imports, impl blocks)
// into a TypeEnv.
func CollectTypes(src []byte) (*TypeEnv, error) {
	return CollectTypesMulti(map[string][]byte{"": src})
}

func collectTypes(src []byte) (*TypeEnv, error) {
	env := NewTypeEnv()
	if err := collectIntoEnv(env, "", src); err != nil {
		return nil, err
	}
	env.bindNamedTypes()
	return env, nil
}

// CollectTypesMulti parses multiple files into a shared type environment.
func CollectTypesMulti(files map[string][]byte) (*TypeEnv, error) {
	env := NewTypeEnv()
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if err := collectIntoEnv(env, name, files[name]); err != nil {
			return nil, err
		}
	}
	env.bindNamedTypes()
	return env, nil
}

// CollectTypesFromDir loads all .fw files from a directory into one environment.
func CollectTypesFromDir(dir string) (*TypeEnv, error) {
	matches, err := filepath.Glob(filepath.Join(dir, "*.fw"))
	if err != nil {
		return nil, fmt.Errorf("glob %s: %w", dir, err)
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("no .fw files found in %s", dir)
	}
	files := make(map[string][]byte, len(matches))
	for _, match := range matches {
		src, readErr := os.ReadFile(match)
		if readErr != nil {
			return nil, fmt.Errorf("read %s: %w", match, readErr)
		}
		files[match] = src
	}
	return CollectTypesMulti(files)
}

func collectIntoEnv(env *TypeEnv, filename string, src []byte) error {
	lang, err := getFWLanguage()
	if err != nil {
		return fmt.Errorf("generate language: %w", err)
	}

	parser := gotreesitter.NewParser(lang)
	tree, err := parser.Parse(src)
	if err != nil {
		return fmt.Errorf("parse: %w", err)
	}

	root := tree.RootNode()
	c := &collector{env: env, src: src, lang: lang, file: filename}
	c.walkChildren(root)
	if c.err != nil {
		return c.err
	}
	return nil
}

// collector walks the CST and populates a TypeEnv.
type collector struct {
	env  *TypeEnv
	src  []byte
	lang *gotreesitter.Language
	file string
	err  error
}

func (c *collector) text(n *gotreesitter.Node) string {
	return string(c.src[n.StartByte():n.EndByte()])
}

func (c *collector) nodeType(n *gotreesitter.Node) string {
	return n.Type(c.lang)
}

func (c *collector) childByField(n *gotreesitter.Node, field string) *gotreesitter.Node {
	return n.ChildByFieldName(field, c.lang)
}

func (c *collector) fail(err error) {
	if c.err == nil {
		c.err = err
	}
}

func (c *collector) sourceRange(n *gotreesitter.Node) SourceRange {
	if n == nil {
		return SourceRange{File: c.file}
	}
	start := n.StartPoint()
	end := n.EndPoint()
	return SourceRange{
		File:     c.file,
		StartRow: int(start.Row),
		StartCol: int(start.Column),
		EndRow:   int(end.Row),
		EndCol:   int(end.Column),
	}
}

func (c *collector) registerSymbol(name string, kind SymbolKind, typ Type, node *gotreesitter.Node) bool {
	if err := c.env.registerSymbol(SymbolInfo{
		Name:     name,
		Kind:     kind,
		Type:     typ,
		Location: c.sourceRange(node),
	}); err != nil {
		c.fail(err)
		return false
	}
	return true
}

func (c *collector) registerFunc(name string, kind SymbolKind, typ *FuncType, node *gotreesitter.Node) {
	if !c.registerSymbol(name, kind, typ, node) {
		return
	}
	c.env.RegisterFunc(name, typ)
}

func (c *collector) registerStruct(name string, typ *StructType, node *gotreesitter.Node) {
	if !c.registerSymbol(name, SymStruct, typ, node) {
		return
	}
	c.env.RegisterStruct(name, typ)
}

func (c *collector) registerEnum(name string, typ *EnumType, node *gotreesitter.Node) {
	if !c.registerSymbol(name, SymEnum, typ, node) {
		return
	}
	c.env.RegisterEnum(name, typ)
}

func (c *collector) registerVarLike(name string, kind SymbolKind, typ Type, node *gotreesitter.Node) {
	if !c.registerSymbol(name, kind, typ, node) {
		return
	}
	if typ != nil {
		c.env.RegisterVar(name, typ)
	}
}

func (c *collector) inferExprType(n *gotreesitter.Node) Type {
	if n == nil {
		return nil
	}
	switch c.nodeType(n) {
	case "int_literal":
		return Primitive("int")
	case "float_literal":
		return Primitive("float64")
	case "interpreted_string_literal", "raw_string_literal":
		return Primitive("string")
	case "true", "false":
		return Primitive("bool")
	case "rune_literal":
		return Primitive("rune")
	case "composite_literal":
		return parseTypeString(c.text(c.childByField(n, "type")))
	case "unary_expression":
		operator := c.childByField(n, "operator")
		operand := c.childByField(n, "operand")
		if operator == nil || operand == nil {
			return nil
		}
		if c.text(operator) == "&" {
			if elem := c.inferExprType(operand); elem != nil {
				return &PointerType{Elem: elem}
			}
		}
	}
	return nil
}

// walkChildren visits all named children of a node.
func (c *collector) walkChildren(n *gotreesitter.Node) {
	if c.err != nil || n == nil {
		return
	}
	count := int(n.NamedChildCount())
	for i := 0; i < count; i++ {
		child := n.NamedChild(i)
		c.visitNode(child)
		if c.err != nil {
			return
		}
	}
	// Handle top-level type declarations that the FW grammar cannot parse
	// as proper type_declaration nodes. The pattern is:
	//   ERROR("type") + identifier("Name") + struct_type/type_identifier
	// at the root level.
	c.collectOrphanedTypeDecls(n)
}

// visitNode dispatches on node type and registers declarations.
func (c *collector) visitNode(n *gotreesitter.Node) {
	if c.err != nil || n == nil {
		return
	}
	switch c.nodeType(n) {
	case "function_declaration":
		c.collectFunction(n)
	case "type_declaration":
		c.collectTypeDecl(n)
	case "enum_declaration":
		c.collectEnum(n)
	case "var_declaration":
		c.collectVarDecl(n)
	case "const_declaration":
		c.collectConstDecl(n)
	case "import_declaration":
		c.collectImport(n)
	case "impl_block":
		c.collectImpl(n)
	default:
		// Recurse into blocks, source_file, etc. to find nested declarations
		c.walkChildren(n)
	}
}

// collectOrphanedTypeDecls handles `type X struct {...}` at the top level.
// The FW grammar's _top_level_declaration doesn't include _declaration,
// so `type` declarations at file scope parse as ERROR nodes. We detect
// the pattern: ERROR("type") followed by identifier then struct_type.
func (c *collector) collectOrphanedTypeDecls(n *gotreesitter.Node) {
	count := int(n.NamedChildCount())
	for i := 0; i < count; i++ {
		child := n.NamedChild(i)
		// Look for an ERROR node whose text is "type"
		if c.nodeType(child) == "ERROR" && c.text(child) == "type" {
			// Next sibling should be an identifier (the type name)
			if i+1 >= count {
				continue
			}
			nameNode := n.NamedChild(i + 1)
			if c.nodeType(nameNode) != "identifier" {
				continue
			}
			name := c.text(nameNode)

			// The sibling after that should contain the struct_type
			if i+2 >= count {
				continue
			}
			typeNode := n.NamedChild(i + 2)
			structNode := c.findStructType(typeNode)
			if structNode != nil {
				fields := c.extractStructFields(structNode)
				c.registerStruct(name, &StructType{
					Name:       name,
					Fields:     fields,
					Comparable: true,
				}, nameNode)
			}
		}
	}
}

// findStructType searches a node and its children for a struct_type node.
func (c *collector) findStructType(n *gotreesitter.Node) *gotreesitter.Node {
	if c.nodeType(n) == "struct_type" {
		return n
	}
	for i := 0; i < int(n.NamedChildCount()); i++ {
		if found := c.findStructType(n.NamedChild(i)); found != nil {
			return found
		}
	}
	return nil
}

// collectFunction extracts function name, parameters, and return types.
//
// Grammar:
//
//	function_declaration: func name(parameters) result { body }
//	  - field "name": identifier
//	  - field "parameters": parameter_list
//	  - field "result": parameter_list | _simple_type | blank
func (c *collector) collectFunction(n *gotreesitter.Node) {
	nameNode := c.childByField(n, "name")
	if nameNode == nil {
		return
	}
	name := c.text(nameNode)

	params := c.extractParamTypes(c.childByField(n, "parameters"))
	results := c.extractResultTypes(c.childByField(n, "result"))

	c.registerFunc(name, SymFunc, &FuncType{
		Params:  params,
		Results: results,
	}, nameNode)
}

// extractParamTypes walks a parameter_list and extracts the types.
// Each child is a parameter_declaration with optional name(s) and a type field.
func (c *collector) extractParamTypes(paramList *gotreesitter.Node) []Type {
	if paramList == nil {
		return nil
	}
	var types []Type
	for i := 0; i < int(paramList.NamedChildCount()); i++ {
		child := paramList.NamedChild(i)
		nt := c.nodeType(child)
		switch nt {
		case "parameter_declaration":
			typeNode := c.childByField(child, "type")
			if typeNode == nil {
				continue
			}
			typeName := c.text(typeNode)
			// Count how many name identifiers are in this declaration.
			// Go allows "a, b int" which declares two params of the same type.
			nameCount := 0
			for j := 0; j < int(child.NamedChildCount()); j++ {
				gc := child.NamedChild(j)
				if c.nodeType(gc) == "identifier" {
					nameCount++
				}
			}
			if nameCount == 0 {
				// Unnamed parameter (just type): func(int, string)
				nameCount = 1
			}
			for k := 0; k < nameCount; k++ {
				if pt := parseTypeString(typeName); pt != nil {
					types = append(types, pt)
				}
			}
		case "variadic_parameter_declaration":
			typeNode := c.childByField(child, "type")
			if typeNode == nil {
				continue
			}
			typeName := c.text(typeNode)
			if elem := parseTypeString(typeName); elem != nil {
				types = append(types, &SliceType{Elem: elem})
			}
		}
	}
	return types
}

// extractResultTypes extracts return types from the result field.
// The result can be a single type, a parameter_list (for multi-return), or nil.
func (c *collector) extractResultTypes(resultNode *gotreesitter.Node) []Type {
	if resultNode == nil {
		return nil
	}
	nt := c.nodeType(resultNode)
	switch nt {
	case "parameter_list":
		// Multi-return: (int, error)
		// Each child is a parameter_declaration or just a type
		var types []Type
		for i := 0; i < int(resultNode.NamedChildCount()); i++ {
			child := resultNode.NamedChild(i)
			childType := c.nodeType(child)
			if childType == "parameter_declaration" {
				typeNode := c.childByField(child, "type")
				if typeNode != nil {
					if pt := parseTypeString(c.text(typeNode)); pt != nil {
						types = append(types, pt)
					}
				}
			} else {
				// Bare type in result list
				if pt := parseTypeString(c.text(child)); pt != nil {
					types = append(types, pt)
				}
			}
		}
		return types
	default:
		// Single return type (e.g., "int", "string", "*Foo")
		if pt := parseTypeString(c.text(resultNode)); pt != nil {
			return []Type{pt}
		}
		return nil
	}
}

// collectTypeDecl handles type declarations which may contain struct types.
//
// Grammar:
//
//	type_declaration: type (type_spec | type_alias | (...))
//	type_spec: name type_parameters type
func (c *collector) collectTypeDecl(n *gotreesitter.Node) {
	for i := 0; i < int(n.NamedChildCount()); i++ {
		child := n.NamedChild(i)
		nt := c.nodeType(child)
		if nt == "type_spec" {
			c.collectTypeSpec(child)
		}
	}
}

func (c *collector) collectTypeSpec(n *gotreesitter.Node) {
	nameNode := c.childByField(n, "name")
	typeNode := c.childByField(n, "type")
	if nameNode == nil || typeNode == nil {
		return
	}
	name := c.text(nameNode)

	if c.nodeType(typeNode) == "struct_type" {
		fields := c.extractStructFields(typeNode)
		c.registerStruct(name, &StructType{
			Name:       name,
			Fields:     fields,
			Comparable: true, // default; refined later by deeper analysis
		}, nameNode)
	}
	// Other type specs (interfaces, aliases) can be added later
}

// extractStructFields walks a struct_type's field_declaration_list.
func (c *collector) extractStructFields(structNode *gotreesitter.Node) map[string]Type {
	fields := make(map[string]Type)
	// struct_type -> "struct" field_declaration_list
	// field_declaration_list -> { field_declaration* }
	for i := 0; i < int(structNode.NamedChildCount()); i++ {
		child := structNode.NamedChild(i)
		if c.nodeType(child) == "field_declaration_list" {
			c.extractFieldsFromList(child, fields)
			break
		}
	}
	return fields
}

func (c *collector) extractFieldsFromList(listNode *gotreesitter.Node, fields map[string]Type) {
	for i := 0; i < int(listNode.NamedChildCount()); i++ {
		child := listNode.NamedChild(i)
		if c.nodeType(child) == "field_declaration" {
			c.extractField(child, fields)
		}
	}
}

func (c *collector) extractField(fieldNode *gotreesitter.Node, fields map[string]Type) {
	typeNode := c.childByField(fieldNode, "type")
	if typeNode == nil {
		return
	}
	typeName := c.text(typeNode)

	// Collect all field names (there can be multiple: "x, y float64")
	var names []string
	for i := 0; i < int(fieldNode.NamedChildCount()); i++ {
		child := fieldNode.NamedChild(i)
		if c.nodeType(child) == "field_identifier" {
			names = append(names, c.text(child))
		}
	}

	for _, name := range names {
		if pt := parseTypeString(typeName); pt != nil {
			fields[name] = pt
		}
	}
}

// collectEnum extracts enum name and variant information.
//
// Grammar:
//
//	enum_declaration: enum name { variant, variant, ... }
//	enum_variant: name | name(type, type, ...)
func (c *collector) collectEnum(n *gotreesitter.Node) {
	nameNode := c.childByField(n, "name")
	if nameNode == nil {
		return
	}
	name := c.text(nameNode)

	variants := make(map[string][]Type)
	for i := 0; i < int(n.NamedChildCount()); i++ {
		child := n.NamedChild(i)
		if c.nodeType(child) == "enum_variant" {
			vName, vTypes := c.extractVariant(child)
			if vName != "" {
				variants[vName] = vTypes
			}
		}
	}

	c.registerEnum(name, &EnumType{
		Name:     name,
		Variants: variants,
	}, nameNode)
}

func (c *collector) extractVariant(n *gotreesitter.Node) (string, []Type) {
	variantName := ""
	nameNode := c.childByField(n, "name")
	if nameNode != nil {
		variantName = c.text(nameNode)
	}

	// Payload types are any named children that are NOT the name identifier
	var types []Type
	for i := 0; i < int(n.NamedChildCount()); i++ {
		child := n.NamedChild(i)
		if c.nodeType(child) != "identifier" {
			if pt := parseTypeString(c.text(child)); pt != nil {
				types = append(types, pt)
			}
		}
	}
	return variantName, types
}

func (c *collector) collectVarDecl(n *gotreesitter.Node) {
	for i := 0; i < int(n.NamedChildCount()); i++ {
		spec := n.NamedChild(i)
		if spec == nil || c.nodeType(spec) != "var_spec" {
			continue
		}
		explicit := parseTypeString(c.text(c.childByField(spec, "type")))
		value := c.childByField(spec, "value")
		inferred := c.inferExprType(value)
		for j := 0; j < int(spec.ChildCount()); j++ {
			if spec.FieldNameForChild(j, c.lang) != "name" {
				continue
			}
			nameNode := spec.Child(j)
			if nameNode == nil {
				continue
			}
			typ := explicit
			if typ == nil {
				typ = inferred
			}
			c.registerVarLike(c.text(nameNode), SymVar, typ, nameNode)
		}
	}
}

func (c *collector) collectConstDecl(n *gotreesitter.Node) {
	for i := 0; i < int(n.NamedChildCount()); i++ {
		spec := n.NamedChild(i)
		if spec == nil || c.nodeType(spec) != "const_spec" {
			continue
		}
		explicit := parseTypeString(c.text(c.childByField(spec, "type")))
		value := c.childByField(spec, "value")
		inferred := c.inferExprType(value)
		for j := 0; j < int(spec.ChildCount()); j++ {
			if spec.FieldNameForChild(j, c.lang) != "name" {
				continue
			}
			nameNode := spec.Child(j)
			if nameNode == nil {
				continue
			}
			typ := explicit
			if typ == nil {
				typ = inferred
			}
			c.registerVarLike(c.text(nameNode), SymConst, typ, nameNode)
		}
	}
}

// collectImport records import paths for later resolution.
//
// Grammar:
//
//	import_declaration: import (import_spec | import_spec_list)
//	import_spec: name? path
func (c *collector) collectImport(n *gotreesitter.Node) {
	for i := 0; i < int(n.NamedChildCount()); i++ {
		child := n.NamedChild(i)
		nt := c.nodeType(child)
		switch nt {
		case "import_spec":
			c.collectImportSpec(child)
		case "import_spec_list":
			// Walk children of the list
			for j := 0; j < int(child.NamedChildCount()); j++ {
				spec := child.NamedChild(j)
				if c.nodeType(spec) == "import_spec" {
					c.collectImportSpec(spec)
				}
			}
		}
	}
}

func (c *collector) collectImportSpec(n *gotreesitter.Node) {
	pathNode := c.childByField(n, "path")
	if pathNode == nil {
		return
	}
	// Path is a string literal like "fmt" -- strip quotes
	path := c.text(pathNode)
	path = strings.Trim(path, `"`)

	// Determine the local name: explicit alias or last path component
	alias := ""
	nameNode := c.childByField(n, "name")
	if nameNode != nil {
		alias = c.text(nameNode)
	} else {
		parts := strings.Split(path, "/")
		alias = parts[len(parts)-1]
	}

	c.env.imports[alias] = ImportScope{
		Funcs: make(map[string]*FuncType),
		Types: make(map[string]Type),
		Vars:  make(map[string]Type),
	}
	c.env.importPathMap[alias] = path
}

// collectImpl extracts the receiver type and registers methods.
//
// Grammar:
//
//	impl_block: impl type { block }
func (c *collector) collectImpl(n *gotreesitter.Node) {
	typeNode := c.childByField(n, "type")
	if typeNode == nil {
		return
	}
	receiverType := c.text(typeNode)

	// Walk the block to find function_declarations
	for i := 0; i < int(n.NamedChildCount()); i++ {
		child := n.NamedChild(i)
		if c.nodeType(child) == "block" {
			c.collectImplMethods(child, receiverType)
			break
		}
	}
}

func (c *collector) collectImplMethods(block *gotreesitter.Node, receiverType string) {
	// Inside impl blocks, the FW grammar parses functions as func_literal
	// inside expression_statement nodes. We need to walk the tree to find them.
	c.walkForImplMethods(block, receiverType)
}

// walkForImplMethods recursively finds func_literal nodes inside an impl block
// and registers them as methods.
func (c *collector) walkForImplMethods(n *gotreesitter.Node, receiverType string) {
	for i := 0; i < int(n.NamedChildCount()); i++ {
		child := n.NamedChild(i)
		nt := c.nodeType(child)

		if nt == "function_declaration" {
			// If the grammar does produce a function_declaration, handle it
			nameNode := c.childByField(child, "name")
			if nameNode == nil {
				continue
			}
			name := c.text(nameNode)
			params := c.extractParamTypes(c.childByField(child, "parameters"))
			results := c.extractResultTypes(c.childByField(child, "result"))
			c.registerFunc(receiverType+"."+name, SymImpl, &FuncType{
				Params:  params,
				Results: results,
			}, nameNode)
		} else if nt == "func_literal" {
			// Inside impl blocks, "func getX() float64 { ... }" parses as a
			// func_literal. The method name appears in the source text between
			// "func " and the parameter_list, but is not a named child.
			c.collectFuncLiteralAsMethod(child, receiverType)
		} else {
			// Recurse into statement_list, expression_statement, etc.
			c.walkForImplMethods(child, receiverType)
		}
	}
}

// collectFuncLiteralAsMethod extracts a method from a func_literal node
// found inside an impl block. The method name is between "func " and "("
// in the source text.
func (c *collector) collectFuncLiteralAsMethod(n *gotreesitter.Node, receiverType string) {
	text := c.text(n)
	if !strings.HasPrefix(text, "func ") {
		return
	}

	// Extract method name: text between "func " and first "("
	rest := text[5:] // skip "func "
	parenIdx := strings.Index(rest, "(")
	if parenIdx <= 0 {
		return
	}
	name := strings.TrimSpace(rest[:parenIdx])
	if name == "" {
		return
	}

	// Extract parameter types from parameter_list child
	var params []Type
	var results []Type
	for i := 0; i < int(n.NamedChildCount()); i++ {
		child := n.NamedChild(i)
		nt := c.nodeType(child)
		if nt == "parameter_list" {
			params = c.extractParamTypes(child)
		} else if nt == "type_identifier" || nt == "pointer_type" ||
			nt == "slice_type" || nt == "map_type" || nt == "qualified_type" {
			// Single return type appears as a type node after the parameter_list
			if pt := parseTypeString(c.text(child)); pt != nil {
				results = []Type{pt}
			}
		}
	}

	c.registerFunc(receiverType+"."+name, SymImpl, &FuncType{
		Params:  params,
		Results: results,
	}, n)
}

// parseTypeString converts a type name string from the CST into a Type.
// Handles common patterns: primitives, pointers, slices, maps.
func parseTypeString(s string) Type {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}

	// Pointer: *T
	if strings.HasPrefix(s, "*") {
		return &PointerType{Elem: parseTypeString(s[1:])}
	}

	// Slice: []T
	if strings.HasPrefix(s, "[]") {
		return &SliceType{Elem: parseTypeString(s[2:])}
	}

	// Map: map[K]V
	if strings.HasPrefix(s, "map[") {
		rest := s[4:]
		depth := 1
		idx := 0
		for idx < len(rest) && depth > 0 {
			if rest[idx] == '[' {
				depth++
			} else if rest[idx] == ']' {
				depth--
			}
			idx++
		}
		if depth == 0 {
			key := rest[:idx-1]
			val := rest[idx:]
			return &MapType{
				Key:   parseTypeString(key),
				Value: parseTypeString(val),
			}
		}
	}

	// Chan types
	if strings.HasPrefix(s, "chan<- ") {
		return &ChanType{Elem: parseTypeString(s[6:]), Dir: ChanSend}
	}
	if strings.HasPrefix(s, "<-chan ") {
		return &ChanType{Elem: parseTypeString(s[6:]), Dir: ChanRecv}
	}
	if strings.HasPrefix(s, "chan ") {
		return &ChanType{Elem: parseTypeString(s[4:]), Dir: ChanBidi}
	}

	// Func type: func(T1, T2) R or func(T1, T2) (R1, R2)
	if strings.HasPrefix(s, "func(") {
		if ft := parseFuncTypeString(s); ft != nil {
			return ft
		}
	}

	if base, args, ok := splitGenericTypeString(s); ok {
		typeArgs := make([]Type, 0, len(args))
		for _, arg := range args {
			typeArgs = append(typeArgs, parseTypeString(arg))
		}
		return &GenericType{Name: base, TypeParams: typeArgs}
	}

	if pkg, name, ok := splitQualifiedTypeString(s); ok {
		return &NamedType{Pkg: pkg, Name: name}
	}

	if isBuiltinTypeName(s) {
		return Primitive(s)
	}

	return &NamedType{Name: s}
}

// parseFuncTypeString parses "func(T1, T2) R" into a *FuncType.
// Handles func(), func(T), func(T1, T2), func(T) R, func(T1, T2) (R1, R2).
func parseFuncTypeString(s string) *FuncType {
	if !strings.HasPrefix(s, "func(") {
		return nil
	}

	// Find the matching closing paren for the parameter list.
	depth := 0
	paramEnd := -1
	for i := 4; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				paramEnd = i
				break
			}
		}
		if paramEnd >= 0 {
			break
		}
	}
	if paramEnd < 0 {
		return nil
	}

	// Parse parameter types
	paramStr := strings.TrimSpace(s[5:paramEnd])
	var params []Type
	if paramStr != "" {
		paramParts := splitTypeList(paramStr)
		for _, part := range paramParts {
			pt := parseTypeString(strings.TrimSpace(part))
			if pt == nil {
				return nil
			}
			params = append(params, pt)
		}
	}

	// Parse result types
	resultStr := strings.TrimSpace(s[paramEnd+1:])
	var results []Type
	if resultStr != "" {
		if strings.HasPrefix(resultStr, "(") && strings.HasSuffix(resultStr, ")") {
			// Multiple return types: (R1, R2)
			inner := resultStr[1 : len(resultStr)-1]
			parts := splitTypeList(inner)
			for _, part := range parts {
				rt := parseTypeString(strings.TrimSpace(part))
				if rt == nil {
					return nil
				}
				results = append(results, rt)
			}
		} else {
			rt := parseTypeString(resultStr)
			if rt == nil {
				return nil
			}
			results = []Type{rt}
		}
	}

	return &FuncType{Params: params, Results: results}
}

// splitTypeList splits a comma-separated type list, respecting nested brackets and parens.
func splitTypeList(s string) []string {
	var parts []string
	depth := 0
	start := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(', '[':
			depth++
		case ')', ']':
			depth--
		case ',':
			if depth == 0 {
				parts = append(parts, s[start:i])
				start = i + 1
			}
		}
	}
	parts = append(parts, s[start:])
	return parts
}

func isBuiltinTypeName(s string) bool {
	switch s {
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64",
		"float32", "float64", "byte", "rune", "uintptr",
		"string", "bool", "error", "any":
		return true
	default:
		return false
	}
}

func splitQualifiedTypeString(s string) (pkg string, name string, ok bool) {
	idx := strings.LastIndexByte(s, '.')
	if idx <= 0 || idx >= len(s)-1 {
		return "", "", false
	}
	if strings.ContainsAny(s[:idx], "[]*(){} ,") || strings.ContainsAny(s[idx+1:], "[]*(){} ,") {
		return "", "", false
	}
	return s[:idx], s[idx+1:], true
}

func splitGenericTypeString(s string) (base string, args []string, ok bool) {
	start := strings.IndexByte(s, '[')
	if start <= 0 || !strings.HasSuffix(s, "]") {
		return "", nil, false
	}

	depth := 0
	for i := start; i < len(s); i++ {
		switch s[i] {
		case '[':
			depth++
		case ']':
			depth--
			if depth == 0 {
				if i != len(s)-1 {
					return "", nil, false
				}
				typeArgs := splitTopLevelTypeArgs(s[start+1 : i])
				if len(typeArgs) == 0 {
					return "", nil, false
				}
				return strings.TrimSpace(s[:start]), typeArgs, true
			}
		}
	}
	return "", nil, false
}

func splitTopLevelTypeArgs(s string) []string {
	var (
		args  []string
		start int
		depth int
	)
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '[':
			depth++
		case ']':
			depth--
		case ',':
			if depth == 0 {
				arg := strings.TrimSpace(s[start:i])
				if arg != "" {
					args = append(args, arg)
				}
				start = i + 1
			}
		}
	}
	last := strings.TrimSpace(s[start:])
	if last != "" {
		args = append(args, last)
	}
	return args
}
