package handlers

import (
	"html/template"
	"log/slog"
	"net/http"

	"github.com/virtuos/keyosk/internal/config"
	"github.com/virtuos/keyosk/internal/database"
	"github.com/virtuos/keyosk/internal/i18n"
	"github.com/virtuos/keyosk/web"
)

func parsePrivacyTemplate() *template.Template {
	return template.Must(template.New("privacy.html").
		Funcs(langFuncs()).
		ParseFS(web.TemplateFS, "templates/privacy.html", "templates/layout.html"))
}

// privacyData is what privacy.html renders.
type privacyData struct {
	Lang      i18n.Lang
	Theme     Theme
	Path      string
	CSRFToken string
	Notice    template.HTML
	// NoticeLang is the language the notice is written in, which differs
	// from Lang when the reader's language has none.
	NoticeLang i18n.Lang
	// Tabs are the languages an anonymous reader can switch the notice to,
	// empty unless there is more than one. A signed-in reader has chosen a
	// language in the account settings and gets that.
	Tabs     []i18n.Lang
	TitleKey string
	// User is nil for an anonymous reader, who gets no account menu.
	User          *database.User
	IsAdmin       bool
	PrivacyNotice bool
}

// noticeIn is the deployment's notice in lang, empty when none was written.
//
// The markup is trusted as-is. It comes from a file the operator deploys beside
// the portal, the same trust as the portal's own templates, never from a user.
func noticeIn(cfg *config.Config, lang i18n.Lang) template.HTML {
	switch lang {
	case i18n.DE:
		return template.HTML(cfg.PrivacyNoticeDE)
	case i18n.EN:
		return template.HTML(cfg.PrivacyNoticeEN)
	}
	return ""
}

// noticeLangs are the languages the deployment has a notice in, in the
// order the portal lists its languages.
func noticeLangs(cfg *config.Config) []i18n.Lang {
	var langs []i18n.Lang
	for _, l := range i18n.Supported {
		if noticeIn(cfg, l) != "" {
			langs = append(langs, l)
		}
	}
	return langs
}

// privacyNotice is the deployment's notice in the reader's language, or in
// another one when that was not written: a notice in German beats none for an
// English reader. The second result is the language it is in. The notice is
// empty when the deployment has none.
func privacyNotice(cfg *config.Config, lang i18n.Lang) (template.HTML, i18n.Lang) {
	if notice := noticeIn(cfg, lang); notice != "" {
		return notice, lang
	}
	if langs := noticeLangs(cfg); len(langs) > 0 {
		return noticeIn(cfg, langs[0]), langs[0]
	}
	return "", lang
}

// hasPrivacyNotice reports whether /privacy has something to show, so pages
// link it rather than a 404.
func hasPrivacyNotice(cfg *config.Config) bool {
	return len(noticeLangs(cfg)) > 0
}

// Privacy renders the deployment's privacy notice, or 404 when it has none.
//
// It needs no login: signing in is where the data the notice describes starts
// being collected, so it has to be readable before that, and other sites link
// to it.
//
// An anonymous reader gets one tab per language the notice is written in,
// each a link with ?lang. The parameter only picks what this page shows and
// stores nothing, so the GET stays free of side effects.
func (u *UI) Privacy(w http.ResponseWriter, r *http.Request) {
	if !hasPrivacyNotice(u.cfg) {
		http.NotFound(w, r)
		return
	}
	data := privacyData{
		Theme:         ThemeFromRequest(r),
		Path:          r.URL.Path,
		CSRFToken:     u.csrf.Token(w, r),
		TitleKey:      "app.title",
		PrivacyNotice: true,
	}
	data.Lang = i18n.FromRequest(r)
	// A signed-in reader keeps the account menu. The Admin entry follows the
	// same flag as the /admin gate, as on the dashboard.
	if su, err := u.requireSession(r); err == nil && su != nil {
		data.User = su.User
		data.IsAdmin = su.User.IsAdmin
	} else {
		if l := i18n.Lang(r.URL.Query().Get("lang")); i18n.Valid(l) {
			data.Lang = l
		}
		if langs := noticeLangs(u.cfg); len(langs) > 1 {
			data.Tabs = langs
		}
	}
	data.Notice, data.NoticeLang = privacyNotice(u.cfg, data.Lang)
	if err := u.privacyTmpl.Execute(w, data); err != nil {
		slog.Error("privacy template", "err", err)
	}
}
