package types

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"les/pkg/lexer"
	"les/pkg/parser"
)

// FileModuleLoader loads other .les files and infers their exported types.
type FileModuleLoader struct {
	baseDir string
	cache   map[string]*ModuleType
	loading map[string]bool // circular import detection
}

func NewFileModuleLoader(baseDir string) *FileModuleLoader {
	return &FileModuleLoader{
		baseDir: baseDir,
		cache:   make(map[string]*ModuleType),
		loading: make(map[string]bool),
	}
}

// Load resolves, parses, and type-checks a module, returning its ModuleType with exported symbols.
func (fml *FileModuleLoader) Load(modPath string) (*ModuleType, error) {
	if mod, ok := fml.cache[modPath]; ok {
		return mod, nil
	}

	if fml.loading[modPath] {
		return nil, fmt.Errorf("circular import detected for module '%s'", modPath)
	}
	fml.loading[modPath] = true
	defer func() {
		delete(fml.loading, modPath)
	}()

	// Resolve file path
	targetFile := filepath.Join(fml.baseDir, modPath+".les")
	if _, err := os.Stat(targetFile); os.IsNotExist(err) {
		// Try directory module: modPath/module.les or modPath/main.les
		targetFile = filepath.Join(fml.baseDir, modPath, "module.les")
		if _, err2 := os.Stat(targetFile); os.IsNotExist(err2) {
			targetFile = filepath.Join(fml.baseDir, modPath, "main.les")
			if _, err3 := os.Stat(targetFile); os.IsNotExist(err3) {
				return nil, fmt.Errorf("module file not found: '%s.les' or '%s/module.les'", modPath, modPath)
			}
		}
	}

	contentBytes, err := os.ReadFile(targetFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read module file %s: %w", targetFile, err)
	}

	modVal, err := compileModule(modPath, targetFile, canonicalModulePath(targetFile), contentBytes, fml.Load)
	if err != nil {
		return nil, err
	}

	fml.cache[modPath] = modVal
	return modVal, nil
}

// compileModule parses and type-checks one module source and returns its
// exported symbols (identifiers starting with [A-Z]). identity distinguishes
// the module file from every other module (see StructType.DeclModule). load
// resolves the module's own imports.
func compileModule(modPath, targetFile, identity string, contentBytes []byte, load func(string) (*ModuleType, error)) (*ModuleType, error) {
	// Lex & Parse
	l := lexer.NewLexer(string(contentBytes))
	p := parser.NewParser(l)
	p.SetFilename(targetFile)
	prog := p.ParseProgram()
	if len(p.Errors()) > 0 {
		return nil, fmt.Errorf("syntax error in module '%s': %s", modPath, strings.Join(p.Errors(), "; "))
	}

	// Module name: explicit declaration or path basename
	modName := prog.ModuleName
	if modName == "" {
		modName = filepath.Base(modPath)
	}

	// Type inference in dedicated module inferencer
	modInf := NewInferencer()
	modInf.declModule = identity
	modInf.declModuleName = modName
	modInf.SetModuleLoader(load)
	if err := modInf.InferProgram(prog); err != nil {
		return nil, fmt.Errorf("type error in module '%s': %w", modName, err)
	}

	// Collect exported members: uppercase identifiers [A-Z]
	exports := make(map[string]Type)
	typesMap := make(map[string]Type)
	for name, typ := range modInf.scope.bindings {
		if len(name) > 0 && name[0] >= 'A' && name[0] <= 'Z' {
			exports[name] = typ
			switch typ.(type) {
			case *StructType, *InterfaceType:
				typesMap[name] = typ
			}
		}
	}

	return &ModuleType{
		Name:    modName,
		Exports: exports,
		Types:   typesMap,
	}, nil
}

// canonicalModulePath returns the identity of a module file: its absolute
// path with symlinks resolved when possible. It does not depend on the import
// path or alias used to reach the module.
func canonicalModulePath(file string) string {
	abs, err := filepath.Abs(file)
	if err != nil {
		return filepath.Clean(file)
	}
	if real, err := filepath.EvalSymlinks(abs); err == nil {
		return real
	}
	return abs
}
