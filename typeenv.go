package ferrouswheel

import "fmt"

// SymbolKind identifies a top-level symbol category for editor features.
type SymbolKind int

const (
	SymFunc SymbolKind = iota
	SymStruct
	SymEnum
	SymVar
	SymConst
	SymImpl
)

// SourceRange tracks a symbol's origin in source.
type SourceRange struct {
	File     string
	StartRow int
	StartCol int
	EndRow   int
	EndCol   int
}

// SymbolInfo describes a top-level declaration collected from source.
type SymbolInfo struct {
	Name     string
	Kind     SymbolKind
	Type     Type
	Location SourceRange
}

// Scope holds variable bindings for a lexical scope.
type Scope struct {
	vars    map[string]Type
	mutable map[string]bool // true = let mut, false = let; absent = Go-native (unrestricted)
	parent  *Scope
}

func newScope(parent *Scope) *Scope {
	return &Scope{vars: make(map[string]Type), mutable: make(map[string]bool), parent: parent}
}

func (s *Scope) set(name string, typ Type) { s.vars[name] = typ }

func (s *Scope) setWithMut(name string, typ Type, mut bool) {
	s.vars[name] = typ
	s.mutable[name] = mut
}

func (s *Scope) isMutable(name string) bool {
	if m, ok := s.mutable[name]; ok {
		return m
	}
	if s.parent != nil {
		return s.parent.isMutable(name)
	}
	return true // Go-native bindings default to mutable
}

func (s *Scope) isLetBinding(name string) bool {
	if _, ok := s.mutable[name]; ok {
		return true
	}
	if s.parent != nil {
		return s.parent.isLetBinding(name)
	}
	return false
}

func (s *Scope) get(name string) (Type, bool) {
	if t, ok := s.vars[name]; ok {
		return t, true
	}
	if s.parent != nil {
		return s.parent.get(name)
	}
	return nil, false
}

// TypeEnv is the type environment for a single file.
type TypeEnv struct {
	scope         *Scope
	funcs         map[string]*FuncType
	structs       map[string]*StructType
	enums         map[string]*EnumType
	imports       map[string]ImportScope // alias -> exported types
	importPathMap map[string]string      // alias -> full import path
	symbols       []SymbolInfo
	symbolIndex   map[string]int
	filename      string
	InferCtx      *InferenceContext // nil until inference is active
}

// ImportScope holds the exported types from one Go package.
type ImportScope struct {
	Funcs map[string]*FuncType
	Types map[string]Type
	Vars  map[string]Type
}

func NewTypeEnv() *TypeEnv {
	return &TypeEnv{
		scope:         newScope(nil),
		funcs:         make(map[string]*FuncType),
		structs:       make(map[string]*StructType),
		enums:         make(map[string]*EnumType),
		imports:       make(map[string]ImportScope),
		importPathMap: make(map[string]string),
		symbolIndex:   make(map[string]int),
	}
}

func (e *TypeEnv) PushScope()                        { e.scope = newScope(e.scope) }
func (e *TypeEnv) PopScope()                         { e.scope = e.scope.parent }
func (e *TypeEnv) RegisterVar(name string, typ Type) { e.scope.set(name, typ) }

func (e *TypeEnv) LookupVar(name string) (Type, error) {
	if t, ok := e.scope.get(name); ok {
		return t, nil
	}
	return nil, fmt.Errorf("undefined variable: %s", name)
}

func (e *TypeEnv) RegisterFunc(name string, typ *FuncType)     { e.funcs[name] = typ }
func (e *TypeEnv) RegisterStruct(name string, typ *StructType) { e.structs[name] = typ }
func (e *TypeEnv) RegisterEnum(name string, typ *EnumType)     { e.enums[name] = typ }

func (e *TypeEnv) LookupFunc(name string) (*FuncType, error) {
	if t, ok := e.funcs[name]; ok {
		return t, nil
	}
	return nil, fmt.Errorf("undefined function: %s", name)
}

func (e *TypeEnv) LookupStruct(name string) (*StructType, error) {
	if t, ok := e.structs[name]; ok {
		return t, nil
	}
	return nil, fmt.Errorf("undefined struct: %s", name)
}

func (e *TypeEnv) LookupEnum(name string) (*EnumType, error) {
	if t, ok := e.enums[name]; ok {
		return t, nil
	}
	return nil, fmt.Errorf("undefined enum: %s", name)
}

func (e *TypeEnv) LookupFieldType(structName, fieldName string) (Type, error) {
	st, err := e.LookupStruct(structName)
	if err != nil {
		return nil, err
	}
	ft, ok := st.Fields[fieldName]
	if !ok {
		return nil, fmt.Errorf("struct %s has no field %s", structName, fieldName)
	}
	return ft, nil
}

// SetFilename sets the file name used in error messages.
func (e *TypeEnv) SetFilename(name string) { e.filename = name }

// Funcs returns the registered function types.
func (e *TypeEnv) Funcs() map[string]*FuncType { return e.funcs }

// Structs returns the registered struct types.
func (e *TypeEnv) Structs() map[string]*StructType { return e.structs }

// Enums returns the registered enum types.
func (e *TypeEnv) Enums() map[string]*EnumType { return e.enums }

// Imports returns the import scopes.
func (e *TypeEnv) Imports() map[string]ImportScope { return e.imports }

// Symbols returns the collected top-level symbols.
func (e *TypeEnv) Symbols() []SymbolInfo {
	out := make([]SymbolInfo, len(e.symbols))
	copy(out, e.symbols)
	return out
}

// FindSymbol returns the first symbol with the given name.
func (e *TypeEnv) FindSymbol(name string) (SymbolInfo, bool) {
	for _, symbol := range e.symbols {
		if symbol.Name == name {
			return symbol, true
		}
	}
	return SymbolInfo{}, false
}

// FindFileSymbols returns all symbols declared in the given source file.
func (e *TypeEnv) FindFileSymbols(file string) []SymbolInfo {
	var out []SymbolInfo
	for _, symbol := range e.symbols {
		if symbol.Location.File == file {
			out = append(out, symbol)
		}
	}
	return out
}

// importPaths returns the full import paths collected during type collection.
func (e *TypeEnv) importPaths() []string {
	seen := make(map[string]struct{}, len(e.importPathMap))
	paths := make([]string, 0, len(e.importPathMap))
	for _, p := range e.importPathMap {
		if _, ok := seen[p]; ok {
			continue
		}
		seen[p] = struct{}{}
		paths = append(paths, p)
	}
	return paths
}

// LoadCollectedImports loads the packages referenced by collected import specs.
func (e *TypeEnv) LoadCollectedImports(moduleDir string) error {
	return e.LoadImports(e.importPaths(), moduleDir)
}

func (e *TypeEnv) bindNamedTypes() {
	for name, fn := range e.funcs {
		e.funcs[name] = e.bindType(fn).(*FuncType)
	}
	for _, st := range e.structs {
		for fieldName, fieldType := range st.Fields {
			st.Fields[fieldName] = e.bindType(fieldType)
		}
	}
	for _, en := range e.enums {
		for variant, payloads := range en.Variants {
			for i, payload := range payloads {
				payloads[i] = e.bindType(payload)
			}
			en.Variants[variant] = payloads
		}
	}
}

func cloneScope(scope *Scope) *Scope {
	if scope == nil {
		return nil
	}
	cloned := &Scope{
		vars:    make(map[string]Type, len(scope.vars)),
		mutable: make(map[string]bool, len(scope.mutable)),
		parent:  cloneScope(scope.parent),
	}
	for name, typ := range scope.vars {
		cloned.vars[name] = typ
	}
	for name, mut := range scope.mutable {
		cloned.mutable[name] = mut
	}
	return cloned
}

// Clone returns a shallow copy of the type environment with an independent scope stack.
func (e *TypeEnv) Clone() *TypeEnv {
	if e == nil {
		return nil
	}
	cloned := &TypeEnv{
		scope:         cloneScope(e.scope),
		funcs:         make(map[string]*FuncType, len(e.funcs)),
		structs:       make(map[string]*StructType, len(e.structs)),
		enums:         make(map[string]*EnumType, len(e.enums)),
		imports:       make(map[string]ImportScope, len(e.imports)),
		importPathMap: make(map[string]string, len(e.importPathMap)),
		symbols:       make([]SymbolInfo, len(e.symbols)),
		symbolIndex:   make(map[string]int, len(e.symbolIndex)),
		filename:      e.filename,
		InferCtx:      e.InferCtx,
	}
	for name, fn := range e.funcs {
		cloned.funcs[name] = fn
	}
	for name, st := range e.structs {
		cloned.structs[name] = st
	}
	for name, enum := range e.enums {
		cloned.enums[name] = enum
	}
	for name, imp := range e.imports {
		cloned.imports[name] = imp
	}
	for name, path := range e.importPathMap {
		cloned.importPathMap[name] = path
	}
	copy(cloned.symbols, e.symbols)
	for key, idx := range e.symbolIndex {
		cloned.symbolIndex[key] = idx
	}
	return cloned
}

func (e *TypeEnv) registerSymbol(symbol SymbolInfo) error {
	if symbol.Name == "" {
		return nil
	}
	if idx, ok := e.symbolIndex[symbol.Name]; ok {
		existing := e.symbols[idx]
		return fmt.Errorf(
			"%s '%s' declared in both %s (line %d) and %s (line %d)",
			symbolKindLabel(symbol.Kind),
			symbol.Name,
			locationLabel(existing.Location.File),
			existing.Location.StartRow+1,
			locationLabel(symbol.Location.File),
			symbol.Location.StartRow+1,
		)
	}
	e.symbolIndex[symbol.Name] = len(e.symbols)
	e.symbols = append(e.symbols, symbol)
	return nil
}

func symbolKindLabel(kind SymbolKind) string {
	switch kind {
	case SymFunc:
		return "function"
	case SymStruct:
		return "struct"
	case SymEnum:
		return "enum"
	case SymVar:
		return "var"
	case SymConst:
		return "const"
	case SymImpl:
		return "method"
	default:
		return "symbol"
	}
}

func locationLabel(file string) string {
	if file == "" {
		return "<unknown>"
	}
	return file
}

func (e *TypeEnv) bindType(typ Type) Type {
	switch t := typ.(type) {
	case nil, Primitive, *StructType, *EnumType, *InterfaceType, *UntypedConstType, *UnresolvedType, *TypeParamType:
		return typ
	case *PointerType:
		t.Elem = e.bindType(t.Elem)
		return t
	case *SliceType:
		t.Elem = e.bindType(t.Elem)
		return t
	case *MapType:
		t.Key = e.bindType(t.Key)
		t.Value = e.bindType(t.Value)
		return t
	case *ChanType:
		t.Elem = e.bindType(t.Elem)
		return t
	case *FuncType:
		for i, param := range t.Params {
			t.Params[i] = e.bindType(param)
		}
		for i, result := range t.Results {
			t.Results[i] = e.bindType(result)
		}
		return t
	case *TupleType:
		for i, elem := range t.Elems {
			t.Elems[i] = e.bindType(elem)
		}
		return t
	case *GenericType:
		for i, arg := range t.TypeParams {
			t.TypeParams[i] = e.bindType(arg)
		}
		return t
	case *NamedType:
		if t.Underlying != nil {
			t.Underlying = e.bindType(t.Underlying)
			return t
		}
		if t.Pkg != "" {
			if imported, err := e.LookupImportedType(t.Pkg, t.Name); err == nil {
				return imported
			}
			return t
		}
		if st, err := e.LookupStruct(t.Name); err == nil {
			t.Underlying = st
			return t
		}
		if en, err := e.LookupEnum(t.Name); err == nil {
			t.Underlying = en
			return t
		}
		return t
	default:
		return typ
	}
}
