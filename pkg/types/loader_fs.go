package types

import (
	"fmt"
	"io/fs"
	"path"
	"strings"
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
	mod, err := compileModule(modPath, target, "fs:"+fsCanonicalPath(l.fsys, target), content, l.Load)
	if err != nil {
		return nil, err
	}
	l.cache[modPath] = mod
	return mod, nil
}

// fsCanonicalPath is canonicalModulePath for a file system: name with the
// symbolic links inside fsys resolved, so that one file reached through
// different links has one identity. A link that cannot be followed inside
// fsys (absolute, leaving it, unreadable, or a loop) leaves name unchanged;
// such a file cannot be read through an os.Root anyway.
func fsCanonicalPath(fsys fs.FS, name string) string {
	cur := name
	for hops := 0; hops < 40; hops++ {
		parts := strings.Split(cur, "/")
		resolved := ""
		followed := false
		for i, part := range parts {
			p := path.Join(resolved, part)
			fi, err := fs.Lstat(fsys, p)
			if err != nil {
				return name
			}
			if fi.Mode()&fs.ModeSymlink == 0 {
				resolved = p
				continue
			}
			target, err := fs.ReadLink(fsys, p)
			if err != nil || path.IsAbs(target) {
				return name
			}
			next := path.Join(append([]string{path.Dir(p), target}, parts[i+1:]...)...)
			if !fs.ValidPath(next) {
				return name
			}
			cur, followed = next, true
			break
		}
		if !followed {
			return resolved
		}
	}
	return name
}
