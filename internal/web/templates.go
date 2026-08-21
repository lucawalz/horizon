package web

import (
	"embed"
	"fmt"
	"html/template"
)

//go:embed templates
var templateFS embed.FS

//go:embed assets
var assetFS embed.FS

type page string

const (
	leasesPage   page = "leases"
	leasePage    page = "lease"
	machinesPage page = "machines"
	errorPage    page = "error"
)

const (
	layoutTemplate     = "layout"
	leaseTableTemplate = "leaseTable"
	leaseBodyTemplate  = "leaseBody"
)

const layoutFile = "templates/layout.html"

func parsePages() (map[page]*template.Template, error) {
	pages := map[page]*template.Template{}
	for _, name := range []page{leasesPage, leasePage, machinesPage, errorPage} {
		set, err := template.New(string(name)).ParseFS(templateFS, layoutFile, fmt.Sprintf("templates/%s.html", name))
		if err != nil {
			return nil, fmt.Errorf("web: parse the %s page: %w", name, err)
		}
		pages[name] = set
	}
	return pages, nil
}
