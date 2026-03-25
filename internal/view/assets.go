package view

const (
	templatesRootDir = "web/templates"

	layoutBaseTemplate = templatesRootDir + "/layouts/base.html"

	partialHeadCoreTemplate  = templatesRootDir + "/partials/head_core.html"
	partialNavPublicTemplate = templatesRootDir + "/partials/nav_public.html"
	partialNavAdminTemplate  = templatesRootDir + "/partials/nav_admin.html"
	partialTopbarTemplate    = templatesRootDir + "/partials/topbar.html"
	partialFooterTemplate    = templatesRootDir + "/partials/footer.html"
	partialSVGFilterTemplate = templatesRootDir + "/partials/svg_filter.html"
)

var sharedTemplateFiles = []string{
	layoutBaseTemplate,
	partialHeadCoreTemplate,
	partialNavPublicTemplate,
	partialNavAdminTemplate,
	partialTopbarTemplate,
	partialFooterTemplate,
	partialSVGFilterTemplate,
}

func pageTemplatePath(pageName string) string {
	return templatesRootDir + "/pages/" + pageName + ".html"
}
