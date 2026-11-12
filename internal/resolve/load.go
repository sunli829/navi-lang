package resolve

import (
	"io/fs"
	"path"
	"sync"

	"navi/internal/syntax"
)

// parsedFile is a file loaded for a module, keyed by its path in the FS.
type parsedFile struct {
	sf *syntax.SourceFile
}

// load parses the crate's files and builds its module tree. Files are found
// by following `mod name;` declarations from the root file, using Rust's
// rules: in a crate root or mod.navi file, children live in the same
// directory; in name.navi they live in name/.
func (r *resolver) load(c *Crate, root string) {
	files := r.parseTree(c, root)
	rootFile := files[root]
	if rootFile == nil {
		return
	}
	c.Root = newModule(c, nil, c.Name)
	c.Root.Dir = path.Dir(root)
	rootDef := r.newDef(DefMod, c.Name, rootFile.sf.File, nil, 0, nil, c)
	rootDef.Module = c.Root
	c.Root.Def = rootDef
	c.Root.file = rootFile.sf.File
	c.Files = append(c.Files, rootFile.sf)
	if r.cfgEnabled(rootFile.sf.File, rootFile.sf.Attrs) {
		r.collectItems(c.Root, rootFile.sf.File, rootFile.sf.Items, files)
	}
}

// modFile returns the file that holds module name declared in a module
// whose child directory is dir, or "" with the candidate paths if none or
// both exist.
func modFile(fsys fs.FS, dir, name string) (string, []string) {
	a := path.Join(dir, name+".navi")
	b := path.Join(dir, name, "mod.navi")
	_, errA := fs.Stat(fsys, a)
	_, errB := fs.Stat(fsys, b)
	switch {
	case errA == nil && errB == nil:
		return "", []string{a, b}
	case errA == nil:
		return a, nil
	case errB == nil:
		return b, nil
	}
	return "", []string{a, b}
}

// childDir returns the directory for modules declared in the file at p.
func childDir(p string, isRoot bool) string {
	if isRoot || path.Base(p) == "mod.navi" {
		return path.Dir(p)
	}
	return path.Join(path.Dir(p), path.Base(p[:len(p)-len(".navi")]))
}

// parseTree parses the root file and every file reachable through mod
// declarations, in parallel. Missing files are reported when the module
// tree is built.
func (r *resolver) parseTree(c *Crate, root string) map[string]*parsedFile {
	var (
		mu    sync.Mutex
		wg    sync.WaitGroup
		files = map[string]*parsedFile{}
	)
	var parse func(p, dir string)
	parse = func(p, dir string) {
		defer wg.Done()
		src, err := fs.ReadFile(c.FS, p)
		if err != nil {
			r.errorf(nil, 0, "crate %s: %v", c.Name, err)
			return
		}
		sf, errs := syntax.ParseFile(syntax.NewFile(path.Join(c.Dir, p), src))
		for _, d := range errs.Diags {
			r.errorf(sf.File, d.Pos, "%s", d.Msg)
		}
		mu.Lock()
		files[p] = &parsedFile{sf: sf}
		mu.Unlock()
		if !r.cfgEnabledQuiet(sf.Attrs) {
			return
		}
		r.findModFiles(c, sf.File, sf.Items, dir, func(child, childDir string) {
			mu.Lock()
			_, seen := files[child]
			if !seen {
				files[child] = nil // reserve
			}
			mu.Unlock()
			if !seen {
				wg.Add(1)
				go parse(child, childDir)
			}
		})
	}
	wg.Add(1)
	parse(root, childDir(root, true))
	wg.Wait()
	return files
}

// findModFiles calls found for every `mod name;` (with an enabled cfg)
// among items, including inside inline modules.
func (r *resolver) findModFiles(c *Crate, file *syntax.File, items []syntax.Item, dir string, found func(p, dir string)) {
	for _, it := range items {
		m, ok := it.(*syntax.ModDecl)
		if !ok || !r.cfgEnabledQuiet(m.Attrs) {
			continue
		}
		if m.Inline {
			r.findModFiles(c, file, m.Items, path.Join(dir, m.Name.Value), found)
			continue
		}
		if p, _ := modFile(c.FS, dir, m.Name.Value); p != "" {
			found(p, childDir(p, false))
		}
	}
}
