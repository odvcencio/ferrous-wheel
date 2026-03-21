package ferrouswheel

import "fmt"

// Scope holds variable bindings for a lexical scope.
type Scope struct {
	vars   map[string]Type
	parent *Scope
}

func newScope(parent *Scope) *Scope {
	return &Scope{vars: make(map[string]Type), parent: parent}
}

func (s *Scope) set(name string, typ Type) { s.vars[name] = typ }

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
	filename      string
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
	}
}

func (e *TypeEnv) PushScope()                      { e.scope = newScope(e.scope) }
func (e *TypeEnv) PopScope()                        { e.scope = e.scope.parent }
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

// importPaths returns the full import paths collected during type collection.
func (e *TypeEnv) importPaths() []string {
	paths := make([]string, 0, len(e.importPathMap))
	for _, p := range e.importPathMap {
		paths = append(paths, p)
	}
	return paths
}
