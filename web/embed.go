package web

import "io/fs"
import "embed"

//go:embed templates static
var assets embed.FS

// TemplateFS exposes the templates sub-directory to html/template.ParseFS.
var TemplateFS, _ = fs.Sub(assets, ".")

// StaticFS exposes the static sub-directory for serving CSS/JS.
var StaticFS, _ = fs.Sub(assets, "static")
