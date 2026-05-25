package view

import (
	"html/template"
	"strconv"
	"strings"
)

type pageHeaderVariant string

const (
	headerVariantNone   pageHeaderVariant = ""
	headerVariantPublic pageHeaderVariant = "public"
	headerVariantAdmin  pageHeaderVariant = "admin"
	headerVariantTopbar pageHeaderVariant = "topbar"
)

// PageData is the context passed to every template.
type PageData struct {
	Title              string
	Lang               string
	PageName           string
	HeaderVariant      pageHeaderVariant
	ShowFooter         bool
	BodyTag            template.HTML
	LiquidGlassEnabled bool
}

type pageDataInput struct {
	Title              string
	Lang               string
	BodyClass          string
	BodyAttrs          string
	PageName           string
	HeaderVariant      pageHeaderVariant
	ShowFooter         bool
	LiquidGlassEnabled bool
}

// pageSpec stores the template file and its default page metadata.
type pageSpec struct {
	templateFile string
	input        pageDataInput
}

func buildPageData(in pageDataInput) PageData {
	tag := "<body"
	if in.BodyClass != "" {
		tag += ` class="` + in.BodyClass + `"`
	}
	if in.BodyAttrs != "" {
		tag += " " + in.BodyAttrs
	}
	tag += ` data-glass-enabled="`
	if in.LiquidGlassEnabled {
		tag += "true"
	} else {
		tag += "false"
	}
	tag += `">`

	return PageData{
		Title:              in.Title,
		Lang:               in.Lang,
		PageName:           in.PageName,
		HeaderVariant:      in.HeaderVariant,
		ShowFooter:         in.ShowFooter,
		BodyTag:            template.HTML(tag),
		LiquidGlassEnabled: in.LiquidGlassEnabled,
	}
}

func parseBoolString(v string, def bool) bool {
	v = strings.TrimSpace(v)
	if v == "" {
		return def
	}
	parsed, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return parsed
}

var pageRegistry = map[string]pageSpec{
	"index": {
		templateFile: pageTemplatePath("index"),
		input: pageDataInput{
			Title:              "API Monitor (Go)",
			Lang:               "en",
			PageName:           "index",
			HeaderVariant:      headerVariantTopbar,
			BodyClass:          "bg-zinc-100 dark:bg-zinc-950 text-zinc-900 dark:text-zinc-100 min-h-screen bg-grid transition-colors duration-300",
			BodyAttrs:          `x-data="dashboard()"`,
			LiquidGlassEnabled: false,
		},
	},
	"analysis": {
		templateFile: pageTemplatePath("analysis"),
		input: pageDataInput{
			Title:              "API Monitor - Analysis",
			Lang:               "en",
			PageName:           "analysis",
			HeaderVariant:      headerVariantTopbar,
			BodyClass:          "bg-zinc-100 dark:bg-zinc-950 text-zinc-900 dark:text-zinc-100 min-h-screen font-sans transition-colors duration-300 bg-grid pb-20",
			LiquidGlassEnabled: false,
		},
	},
	"log_viewer": {
		templateFile: pageTemplatePath("log_viewer"),
		input: pageDataInput{
			Title:              "API Monitor - Log Viewer",
			Lang:               "en",
			PageName:           "log_viewer",
			HeaderVariant:      headerVariantTopbar,
			BodyClass:          "bg-zinc-100 dark:bg-zinc-950 text-zinc-900 dark:text-zinc-100 h-screen bg-grid transition-colors duration-300 overflow-hidden flex flex-col",
			BodyAttrs:          `x-data="logViewer()"`,
			LiquidGlassEnabled: false,
		},
	},
	"admin": {
		templateFile: pageTemplatePath("admin"),
		input: pageDataInput{
			Title:              "Admin Panel - API Monitor",
			Lang:               "en",
			PageName:           "admin",
			HeaderVariant:      headerVariantAdmin,
			BodyClass:          "bg-zinc-100 dark:bg-zinc-950 text-zinc-900 dark:text-zinc-100 min-h-screen bg-grid transition-colors duration-300",
			BodyAttrs:          `data-page="admin-panel"`,
			LiquidGlassEnabled: false,
		},
	},
	"admin_login": {
		templateFile: pageTemplatePath("admin_login"),
		input: pageDataInput{
			Title:              "Admin Login - API Monitor",
			Lang:               "en",
			PageName:           "admin_login",
			HeaderVariant:      headerVariantPublic,
			BodyClass:          "bg-zinc-100 dark:bg-zinc-950 text-zinc-900 dark:text-zinc-100 min-h-screen bg-grid transition-colors duration-300",
			BodyAttrs:          `data-page="admin-login"`,
			LiquidGlassEnabled: false,
		},
	},
	"proxy_docs": {
		templateFile: pageTemplatePath("proxy_docs"),
		input: pageDataInput{
			Title:              "API Proxy Docs",
			Lang:               "zh-CN",
			PageName:           "proxy_docs",
			HeaderVariant:      headerVariantPublic,
			BodyClass:          "bg-zinc-100 dark:bg-zinc-950 text-zinc-900 dark:text-zinc-100 min-h-screen bg-grid transition-colors duration-300",
			ShowFooter:         true,
			LiquidGlassEnabled: false,
		},
	},
}
