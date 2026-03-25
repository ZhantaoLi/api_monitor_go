package view

import (
	"bytes"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"sync"
)

// SettingGetter is the minimal config access surface used by the renderer.
type SettingGetter interface {
	GetSetting(key string) (string, bool, error)
}

// RendererOptions configures optional runtime lookups.
type RendererOptions struct {
	Settings              SettingGetter
	LiquidGlassSettingKey string
}

// Renderer holds pre-parsed page templates.
type Renderer struct {
	settings              SettingGetter
	liquidGlassSettingKey string
	mu                    sync.RWMutex
	pages                 map[string]*template.Template
}

// NewRenderer parses all page templates from the embedded filesystem.
func NewRenderer(webFS fs.FS, opts RendererOptions) (*Renderer, error) {
	r := &Renderer{
		settings:              opts.Settings,
		liquidGlassSettingKey: opts.LiquidGlassSettingKey,
		pages:                 make(map[string]*template.Template),
	}

	for pageName, spec := range pageRegistry {
		tmpl, err := parsePageTemplate(webFS, spec)
		if err != nil {
			return nil, fmt.Errorf("parse template for %s: %w", pageName, err)
		}
		r.pages[pageName] = tmpl
	}

	log.Printf("[view] parsed %d page templates", len(r.pages))
	return r, nil
}

func parsePageTemplate(webFS fs.FS, spec pageSpec) (*template.Template, error) {
	tmpl := template.New("base")
	for _, file := range sharedTemplateFiles {
		data, err := fs.ReadFile(webFS, file)
		if err != nil {
			return nil, err
		}
		if _, err := tmpl.Parse(string(data)); err != nil {
			return nil, err
		}
	}
	pageData, err := fs.ReadFile(webFS, spec.templateFile)
	if err != nil {
		return nil, err
	}
	if _, err := tmpl.Parse(string(pageData)); err != nil {
		return nil, err
	}
	return tmpl, nil
}

// Render writes the named page to w.
func (r *Renderer) Render(w http.ResponseWriter, pageName string) error {
	r.mu.RLock()
	tmpl, ok := r.pages[pageName]
	r.mu.RUnlock()
	if !ok {
		return fmt.Errorf("unknown page: %s", pageName)
	}

	spec, ok := pageRegistry[pageName]
	if !ok {
		return fmt.Errorf("no spec for page: %s", pageName)
	}

	input := spec.input
	if r.settings != nil && r.liquidGlassSettingKey != "" {
		if val, ok, err := r.settings.GetSetting(r.liquidGlassSettingKey); err == nil && ok {
			input.LiquidGlassEnabled = parseBoolString(val, true)
		}
	}
	data := buildPageData(input)

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return fmt.Errorf("template execute error for %s: %w", pageName, err)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, err := buf.WriteTo(w)
	return err
}

// Handler returns an http.HandlerFunc that renders a named page template.
func (r *Renderer) Handler(pageName string) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		if err := r.Render(w, pageName); err != nil {
			log.Printf("[view] render error: %v", err)
			http.Error(w, "internal server error", http.StatusInternalServerError)
		}
	}
}
