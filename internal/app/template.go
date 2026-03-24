package app

import (
	"bytes"
	"fmt"
	"html/template"
	"io/fs"
	"log"
	"net/http"
	"sync"
)

// PageData holds the data passed to every page template.
type PageData struct {
	Title              string        // <title> content
	Lang               string        // html lang attribute (default "en")
	BodyTag            template.HTML // pre-built <body ...> opening tag
	LiquidGlassEnabled bool          // whether the interactive lens is enabled
}

// pageDataInput is used in pageSpec to configure page defaults before building PageData.
type pageDataInput struct {
	Title     string
	Lang      string
	BodyClass string
	BodyAttrs          string // raw extra attributes like x-data="..." or data-page="..."
	LiquidGlassEnabled bool
}

func buildPageData(in pageDataInput) PageData {
	tag := "<body"
	if in.BodyClass != "" {
		tag += ` class="` + in.BodyClass + `"`
	}
	if in.BodyAttrs != "" {
		tag += " " + in.BodyAttrs
	}
	if in.LiquidGlassEnabled {
		tag += ` data-glass-enabled="true"`
	} else {
		tag += ` data-glass-enabled="false"`
	}
	tag += ">"
	return PageData{
		Title:              in.Title,
		Lang:               in.Lang,
		BodyTag:            template.HTML(tag),
		LiquidGlassEnabled: true, // Default, will be overridden in Render
	}
}

// pageSpec declares page-specific defaults.
type pageSpec struct {
	templateFile string    // path under web/templates/pages/
	input        pageDataInput
}

// pageRegistry maps a page name to its spec.
var pageRegistry = map[string]pageSpec{
	"index": {
		templateFile: "web/templates/pages/index.html",
		input: pageDataInput{
			Title:     "API Monitor (Go)",
			Lang:      "en",
			BodyClass: "bg-zinc-100 dark:bg-zinc-950 text-zinc-900 dark:text-zinc-100 min-h-screen bg-grid transition-colors duration-300",
			BodyAttrs: `x-data="dashboard()"`,
		},
	},
	"analysis": {
		templateFile: "web/templates/pages/analysis.html",
		input: pageDataInput{
			Title:     "API Monitor - Analysis",
			Lang:      "en",
			BodyClass: "bg-zinc-100 dark:bg-zinc-950 text-zinc-900 dark:text-zinc-100 min-h-screen font-sans transition-colors duration-300 bg-grid pb-20",
		},
	},
	"log_viewer": {
		templateFile: "web/templates/pages/log_viewer.html",
		input: pageDataInput{
			Title:     "API Monitor - Log Viewer",
			Lang:      "en",
			BodyClass: "bg-zinc-100 dark:bg-zinc-950 text-zinc-900 dark:text-zinc-100 h-screen bg-grid transition-colors duration-300 overflow-hidden flex flex-col",
			BodyAttrs: `x-data="logViewer()"`,
		},
	},
	"admin": {
		templateFile: "web/templates/pages/admin.html",
		input: pageDataInput{
			Title:     "Admin Panel - API Monitor",
			Lang:      "en",
			BodyClass: "bg-zinc-100 dark:bg-zinc-950 text-zinc-900 dark:text-zinc-100 min-h-screen bg-grid transition-colors duration-300",
			BodyAttrs: `data-page="admin-panel"`,
		},
	},
	"admin_login": {
		templateFile: "web/templates/pages/admin_login.html",
		input: pageDataInput{
			Title:     "Admin Login - API Monitor",
			Lang:      "en",
			BodyClass: "bg-zinc-100 dark:bg-zinc-950 text-zinc-900 dark:text-zinc-100 min-h-screen bg-grid transition-colors duration-300",
			BodyAttrs: `data-page="admin-login"`,
		},
	},
	"proxy_docs": {
		templateFile: "web/templates/pages/proxy_docs.html",
		input: pageDataInput{
			Title:     "API Proxy Docs",
			Lang:      "zh-CN",
			BodyClass: "bg-zinc-100 dark:bg-zinc-950 text-zinc-900 dark:text-zinc-100 min-h-screen bg-grid transition-colors duration-300",
		},
	},
}

// PageRenderer holds pre-parsed page templates.
type PageRenderer struct {
	db    *Database
	mu    sync.RWMutex
	pages map[string]*template.Template
}

// initPageRenderer parses all page templates from the embedded filesystem.
func initPageRenderer(webFS fs.FS, db *Database) *PageRenderer {
	pr := &PageRenderer{
		db:    db,
		pages: make(map[string]*template.Template),
	}

	// Read shared templates (base + partials).
	baseFile := "web/templates/base.html"
	partialFiles := []string{
		"web/templates/partials/head_core.html",
		"web/templates/partials/svg_filter.html",
	}

	baseContent, err := fs.ReadFile(webFS, baseFile)
	if err != nil {
		log.Fatalf("[template] failed to read base template: %v", err)
	}

	partialContents := make([][]byte, 0, len(partialFiles))
	for _, pf := range partialFiles {
		data, err := fs.ReadFile(webFS, pf)
		if err != nil {
			log.Fatalf("[template] failed to read partial %s: %v", pf, err)
		}
		partialContents = append(partialContents, data)
	}

	for name, spec := range pageRegistry {
		pageContent, err := fs.ReadFile(webFS, spec.templateFile)
		if err != nil {
			log.Fatalf("[template] failed to read page template %s: %v", spec.templateFile, err)
		}

		// Parse in order: base → partials → page (page overrides blocks).
		tmpl := template.New("base")
		if _, err := tmpl.Parse(string(baseContent)); err != nil {
			log.Fatalf("[template] failed to parse base for page %s: %v", name, err)
		}
		for i, pc := range partialContents {
			if _, err := tmpl.Parse(string(pc)); err != nil {
				log.Fatalf("[template] failed to parse partial %s for page %s: %v", partialFiles[i], name, err)
			}
		}
		if _, err := tmpl.Parse(string(pageContent)); err != nil {
			log.Fatalf("[template] failed to parse page %s: %v", name, err)
		}

		pr.pages[name] = tmpl
	}

	log.Printf("[template] parsed %d page templates", len(pr.pages))
	return pr
}

// Render writes the named page to w.
func (pr *PageRenderer) Render(w http.ResponseWriter, pageName string) error {
	pr.mu.RLock()
	tmpl, ok := pr.pages[pageName]
	pr.mu.RUnlock()
	if !ok {
		return fmt.Errorf("unknown page: %s", pageName)
	}

	spec, ok := pageRegistry[pageName]
	if !ok {
		return fmt.Errorf("no spec for page: %s", pageName)
	}

	// Fetch dynamic settings from DB.
	if pr.db != nil {
		val, ok, _ := pr.db.GetSetting(settingLiquidGlassEnabled)
		if ok {
			spec.input.LiquidGlassEnabled = parseBoolString(val, true)
		} else {
			spec.input.LiquidGlassEnabled = true // default if not set
		}
	} else {
		spec.input.LiquidGlassEnabled = true
	}

	data := buildPageData(spec.input)

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return fmt.Errorf("template execute error for %s: %w", pageName, err)
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, err := buf.WriteTo(w)
	return err
}

// serveTemplatePage returns an http.HandlerFunc that renders a named page template.
func serveTemplatePage(pr *PageRenderer, pageName string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := pr.Render(w, pageName); err != nil {
			log.Printf("[template] render error: %v", err)
			http.Error(w, "internal server error", 500)
		}
	}
}
