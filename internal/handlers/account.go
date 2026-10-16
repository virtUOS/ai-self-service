package handlers

import (
	"html/template"
	"log/slog"
	"net/http"

	"github.com/virtuos/keyosk/internal/database"
	"github.com/virtuos/keyosk/internal/i18n"
	"github.com/virtuos/keyosk/web"
)

func parseAccountTemplate() *template.Template {
	return template.Must(template.New("account.html").
		Funcs(langFuncs()).
		ParseFS(web.TemplateFS, "templates/account.html", "templates/layout.html"))
}

// accountData is what account.html renders.
type accountData struct {
	Lang      i18n.Lang
	Theme     Theme
	Path      string
	CSRFToken string
	TitleKey  string
	// User, IsAdmin and PrivacyNotice feed the shared header and footer.
	User          *database.User
	IsAdmin       bool
	PrivacyNotice bool

	// Langs and Themes are the choices the settings offer, besides the
	// system's. LangPref is the language chosen here, empty when the page
	// follows the browser, so the form can tell the two apart even when the
	// browser asks for the language that was chosen.
	Langs    []i18n.Lang
	Themes   []Theme
	LangPref i18n.Lang
	// Saved confirms the settings just stored.
	Saved bool
}

// langPref is the language chosen in the settings, empty for none.
func langPref(r *http.Request) i18n.Lang {
	if c, err := r.Cookie(i18n.CookieName); err == nil {
		if l := i18n.Lang(c.Value); i18n.Valid(l) {
			return l
		}
	}
	return ""
}

// Account renders the account settings: who the portal knows the user as,
// and the appearance choices.
func (u *UI) Account(w http.ResponseWriter, r *http.Request) {
	su, err := u.requireSession(r)
	if err != nil {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	if err := u.accountTmpl.Execute(w, accountData{
		Lang:          i18n.FromRequest(r),
		Theme:         ThemeFromRequest(r),
		Path:          r.URL.Path,
		CSRFToken:     u.csrf.Token(w, r),
		TitleKey:      "account.title",
		User:          su.User,
		IsAdmin:       su.User.IsAdmin,
		PrivacyNotice: hasPrivacyNotice(u.cfg),
		Langs:         i18n.Supported,
		Themes:        Themes[1:],
		LangPref:      langPref(r),
		Saved:         r.URL.Query().Get("saved") == "1",
	}); err != nil {
		slog.Error("account template", "err", err)
	}
}

// SaveAccount stores the appearance settings. Both live in cookies rather
// than on the account: they apply before login too, on the public privacy
// page, and are a matter of the device as much as of the person.
//
// An empty value follows the system and deletes the cookie. Anything else the
// form does not offer only comes from a crafted request and is refused.
func (u *UI) SaveAccount(w http.ResponseWriter, r *http.Request) {
	if _, err := u.requireSession(r); err != nil {
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	lang := i18n.Lang(r.FormValue("lang"))
	theme := Theme(r.FormValue("theme"))
	if (lang != "" && !i18n.Valid(lang)) || !validTheme(theme) {
		http.Error(w, "invalid setting", http.StatusBadRequest)
		return
	}
	setPrefCookie(w, i18n.CookieName, string(lang), u.cfg.CookieSecure)
	setPrefCookie(w, themeCookie, string(theme), u.cfg.CookieSecure)
	http.Redirect(w, r, "/account?saved=1", http.StatusFound)
}
