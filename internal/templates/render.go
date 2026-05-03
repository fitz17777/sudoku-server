package templates

import (
	"fmt"
	"html/template"
	"io"
	"io/fs"
	"path/filepath"
	"strings"
)

// Renderer holds a base template set (layout + partials) that is never executed
// directly. Every Render and RenderPartial call clones the base first, which
// keeps the base perpetually un-executed so clones always succeed.
type Renderer struct {
	base  *template.Template // layout + partials — never executed directly
	pages map[string]string  // page name → raw source, parsed into a clone per request
}

// New builds a Renderer from the provided filesystem.
// Files under "pages/" are stored as raw sources for clone-per-request rendering.
// All other files (layout/, partials/) are parsed into the base set.
func New(fsys fs.FS) (*Renderer, error) {
	base := template.New("").Funcs(FuncMap())
	pages := map[string]string{}

	err := fs.WalkDir(fsys, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || filepath.Ext(path) != ".html" {
			return err
		}
		b, err := fs.ReadFile(fsys, path)
		if err != nil {
			return fmt.Errorf("reading %s: %w", path, err)
		}
		dir := strings.SplitN(path, "/", 2)[0]
		if dir == "pages" {
			pages[path] = string(b)
		} else {
			if _, err := base.New(path).Parse(string(b)); err != nil {
				return fmt.Errorf("parsing base template %s: %w", path, err)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &Renderer{base: base, pages: pages}, nil
}

// Render executes a full page template. It clones the base set, parses the page
// source into the clone, then executes it. The base set is never mutated.
func (r *Renderer) Render(w io.Writer, name string, data interface{}) error {
	src, ok := r.pages[name]
	if !ok {
		return fmt.Errorf("page template %q not found", name)
	}
	t, err := r.base.Clone()
	if err != nil {
		return fmt.Errorf("cloning base for page %q: %w", name, err)
	}
	if _, err := t.New(name).Parse(src); err != nil {
		return fmt.Errorf("parsing page %q: %w", name, err)
	}
	return t.ExecuteTemplate(w, name, data)
}

// RenderPartial executes a partial or layout template. It clones the base set
// before executing so the base remains un-executed for future clones.
func (r *Renderer) RenderPartial(w io.Writer, name string, data interface{}) error {
	t, err := r.base.Clone()
	if err != nil {
		return fmt.Errorf("cloning base for partial %q: %w", name, err)
	}
	pt := t.Lookup(name)
	if pt == nil {
		return fmt.Errorf("partial template %q not found", name)
	}
	return pt.Execute(w, data)
}

// RenderPageBlock clones the base, parses the named page source into the clone,
// then executes a specific named define (blockName) within that page — without
// the surrounding layout. Used by the WebSocket hub to render game views as
// HTML fragments for injection into the lobby page.
func (r *Renderer) RenderPageBlock(w io.Writer, pageName, blockName string, data interface{}) error {
	src, ok := r.pages[pageName]
	if !ok {
		return fmt.Errorf("page template %q not found", pageName)
	}
	t, err := r.base.Clone()
	if err != nil {
		return fmt.Errorf("cloning base for page block %q: %w", pageName, err)
	}
	if _, err := t.New(pageName).Parse(src); err != nil {
		return fmt.Errorf("parsing page %q: %w", pageName, err)
	}
	return t.ExecuteTemplate(w, blockName, data)
}
