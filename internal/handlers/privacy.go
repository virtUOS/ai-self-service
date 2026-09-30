package handlers

import (
	"html/template"
	"log/slog"
	"net/http"

	"github.com/virtuos/ai-self-service/internal/config"
	"github.com/virtuos/ai-self-service/internal/i18n"
	"github.com/virtuos/ai-self-service/web"
)

func parsePrivacyTemplate() *template.Template {
	return template.Must(template.New("privacy.html").
		Funcs(langFuncs()).
		ParseFS(web.TemplateFS, "templates/privacy.html"))
}

// privacyData is what privacy.html renders.
type privacyData struct {
	Lang      i18n.Lang
	Langs     []i18n.Lang
	Path      string
	CSRFToken string
	Notice    template.HTML
}

// privacyNotice is the deployment's notice in the reader's language, or in the
// other one when only one was written: a notice in German beats none for an
// English reader. Empty when the deployment has no notice.
//
// The markup is trusted as-is. It comes from a file the operator deploys beside
// the portal, the same trust as the portal's own templates, never from a user.
func privacyNotice(cfg *config.Config, lang i18n.Lang) template.HTML {
	notice, other := cfg.PrivacyNoticeDE, cfg.PrivacyNoticeEN
	if lang == i18n.EN {
		notice, other = other, notice
	}
	if notice == "" {
		notice = other
	}
	return template.HTML(notice)
}

// Privacy renders the deployment's privacy notice, or 404 when it has none.
//
// It needs no login: signing in is where the data the notice describes starts
// being collected, so it has to be readable before that, and other sites link
// to it.
func (u *UI) Privacy(w http.ResponseWriter, r *http.Request) {
	lang := i18n.FromRequest(r)
	notice := privacyNotice(u.cfg, lang)
	if notice == "" {
		http.NotFound(w, r)
		return
	}
	if err := u.privacyTmpl.Execute(w, privacyData{
		Lang:      lang,
		Langs:     i18n.Supported,
		Path:      r.URL.Path,
		CSRFToken: u.csrf.Token(w, r),
		Notice:    notice,
	}); err != nil {
		slog.Error("privacy template", "err", err)
	}
}
