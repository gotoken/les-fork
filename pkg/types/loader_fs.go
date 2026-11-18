package types

import (
	"fmt"
	"io/fs"
	"path"
)

// FSModuleLoader loads modules from a file system, typically a directory
// opened with os.OpenRoot so that symbolic links cannot escape it. Module
// paths must be valid fs paths: relative, slash-separated, without "." or
// ".." elements. It resolves modPath like FileModuleLoader: modPath.les,
// then modPath/module.les, then modPath/main.les.
type FSModuleLoader struct {
	fsys    fs.FS
	cache   map[string]*ModuleType
	loading map[string]bool
}

// NewFSModuleLoader returns a loader that reads modules from fsys only.
func NewFSModuleLoader(fsys fs.FS) *FSModuleLoader {
	return &FSModuleLoader{fsys: fsys, cache: make(map[string]*ModuleType), loading: make(map[string]bool)}
}

// Load resolves, parses and type-checks a module.
func (l *FSModuleLoader) Load(modPath string) (*ModuleType, error) {
	if mod, ok := l.cache[modPath]; ok {
		return mod, nil
	}
	if l.loading[modPath] {
		return nil, fmt.Errorf("circular import detected for module '%s'", modPath)
	}
	if !fs.ValidPath(modPath) || modPath == "." {
		return nil, fmt.Errorf("module path '%s' is not allowed: it must be relative and stay inside the module root", modPath)
	}
	l.loading[modPath] = true
	defer delete(l.loading, modPath)

	var target string
	var content []byte
	for _, cand := range []string{modPath + ".les", path.Join(modPath, "module.les"), path.Join(modPath, "main.les")} {
		b, err := fs.ReadFile(l.fsys, cand)
		if err == nil {
			target, content = cand, b
			break
		}
	}
	if target == "" {
		return nil, fmt.Errorf("module file not found: '%s.les' or '%s/module.les'", modPath, modPath)
	}
	mod, err := compileModule(modPath, target, content, l.Load)
	if err != nil {
		return nil, err
	}
	l.cache[modPath] = mod
	return mod, nil
}
