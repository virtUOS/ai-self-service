package i18n

// messages maps a key to its translations. Keys are dotted and grouped by the
// page they appear on, so an untranslated string is easy to spot.
//
// A missing key falls back to the English text and, failing that, the key
// itself — a visibly wrong label is better than a blank page.
var messages = map[string]map[Lang]string{
	// ── Shared ──────────────────────────────────────────────────────────
	"app.title":     {DE: "KI-API-Schlüssel-Portal", EN: "AI API Key Portal"},
	"nav.admin":     {DE: "Administration", EN: "Admin"},
	"nav.dashboard": {DE: "Übersicht", EN: "Dashboard"},
	"nav.signout":   {DE: "Abmelden", EN: "Sign out"},
	"lang.label":    {DE: "Sprache", EN: "Language"},

	// ── Dashboard ───────────────────────────────────────────────────────
	"dash.account":         {DE: "Ihr Konto", EN: "Your account"},
	"dash.name":            {DE: "Name", EN: "Name"},
	"dash.email":           {DE: "E-Mail", EN: "Email"},
	"dash.profile":         {DE: "Profil", EN: "Profile"},
	"dash.profile.help":    {DE: "Ihr Profil bestimmt, welche Modelle Sie nutzen können, wie lange Ihr Schlüssel gilt und wie viele Token Sie pro Zeitraum verbrauchen dürfen. Es wird von der Administration zugewiesen.", EN: "Your profile decides which models you can use, how long your key lasts and how many tokens you may use per period. An administrator assigns it."},
	"dash.usagelimit":      {DE: "Nutzungslimit", EN: "Usage limit"},
	"dash.usagelimit.help": {DE: "Ihr Kontingent zur fairen Nutzung. Anfragen schlagen fehl, sobald es aufgebraucht ist, und funktionieren nach dem nächsten Zurücksetzen automatisch wieder. Die Zurücksetzung erfolgt zu UTC-Zeiten.", EN: "Your fair-use allowance. Requests stop working once it is used up and start again automatically at the next reset. Resets happen on UTC boundaries."},
	"dash.quota.note":      {DE: "Anfragen schlagen fehl, sobald das Limit erreicht ist, und funktionieren beim nächsten Zurücksetzen automatisch wieder (UTC: täglich um Mitternacht UTC, wöchentlich montags, monatlich am 1.).", EN: "Requests fail once the limit is reached and resume automatically at the next reset (UTC boundaries: daily at midnight UTC, weekly on Monday, monthly on the 1st)."},

	"dash.apikey":       {DE: "API-Schlüssel", EN: "API key"},
	"dash.key":          {DE: "Schlüssel", EN: "Key"},
	"dash.expires":      {DE: "Gültig bis", EN: "Expires"},
	"dash.expires.help": {DE: "Wann dieser Schlüssel ungültig wird. Mit „Verlängern“ erhalten Sie jederzeit einen vollen neuen Zeitraum, beliebig oft.", EN: "When this key stops working. Click Extend for a full new period — you can do that at any time, as often as you like."},
	"dash.nokey":        {DE: "Sie haben noch keinen API-Schlüssel.", EN: "You don't have an API key yet."},
	"dash.generate":     {DE: "API-Schlüssel erzeugen", EN: "Generate API key"},
	"dash.extend":       {DE: "Verlängern", EN: "Extend"},
	"dash.extend.until": {DE: "gültig bis", EN: "until"},
	"dash.regenerate":   {DE: "Neu erzeugen", EN: "Regenerate"},
	"dash.delete":       {DE: "Löschen", EN: "Delete"},
	"dash.days":         {DE: "Tage", EN: "days"},
	"dash.copy":         {DE: "Kopieren", EN: "Copy"},
	"dash.copied":       {DE: "Kopiert!", EN: "Copied!"},

	"dash.newkey.warning": {DE: "Speichern Sie Ihren API-Schlüssel jetzt.", EN: "Save your API key now."},
	"dash.newkey.note":    {DE: "Er wird nach dem Verlassen dieser Seite nicht erneut angezeigt.", EN: "It will not be shown again after you leave this page."},

	"dash.usage":            {DE: "Schlüssel verwenden", EN: "Using your key"},
	"dash.usage.note":       {DE: "Konfigurieren Sie Ihren KI-Client mit diesen Einstellungen:", EN: "Configure your AI client with these settings:"},
	"dash.baseurl":          {DE: "Basis-URL", EN: "Base URL"},
	"dash.models":           {DE: "Verfügbare Modelle", EN: "Available models"},
	"dash.usagestats":       {DE: "Ihr Verbrauch", EN: "Your usage"},
	"dash.usagestats.note":  {DE: "Tokenverbrauch dieses Schlüssels in den letzten 30 Tagen. Ein neu erzeugter Schlüssel beginnt wieder bei null.", EN: "Tokens used by this key over the last 30 days. A newly generated key starts again at zero."},
	"dash.usagestats.total": {DE: "Gesamt", EN: "Total"},
	"dash.usagestats.none":  {DE: "Für diesen Schlüssel wurde noch kein Verbrauch verzeichnet.", EN: "No usage recorded for this key yet."},
	"dash.tokens":           {DE: "Tokens", EN: "tokens"},
	"dash.models.help":      {DE: "Die Modelle, die Ihr Schlüssel verwenden darf. Geben Sie den Namen genau so an, wie er hier steht.", EN: "The models your key may use. Pass the name exactly as shown here."},
	"dash.baseurl.help":     {DE: "Richten Sie Ihren KI-Client auf diese Adresse und hinterlegen Sie Ihren API-Schlüssel. Die Schnittstelle ist OpenAI-kompatibel, funktioniert also mit den üblichen Werkzeugen.", EN: "Point your AI client at this address and give it your API key. It speaks the OpenAI API, so any OpenAI-compatible tool works."},

	"dash.confirm.regenerate": {DE: "Damit wird Ihr aktueller Schlüssel ungültig und ein neuer erzeugt. Fortfahren?", EN: "This will revoke your current key and generate a new one. Continue?"},
	"dash.confirm.delete":     {DE: "API-Schlüssel löschen? Sie verlieren sofort den Zugriff.", EN: "Delete your API key? You will lose access immediately."},

	"dash.expiry.expired":      {DE: "Ihr Schlüssel ist abgelaufen.", EN: "Your key has expired."},
	"dash.expiry.expired.note": {DE: "Erzeugen Sie einen neuen, um den Zugriff wiederherzustellen.", EN: "Generate a new one to restore access."},
	"dash.expiry.today":        {DE: "Ihr Schlüssel läuft heute ab.", EN: "Your key expires today."},
	"dash.expiry.today.note":   {DE: "Verlängern Sie ihn, um den Zugriff zu behalten.", EN: "Extend it to keep your access."},
	"dash.expiry.soon.note":    {DE: "Verlängern Sie ihn jetzt, um Unterbrechungen zu vermeiden.", EN: "Extend it now to avoid interruption."},

	"badge.expired": {DE: "abgelaufen", EN: "expired"},
	"badge.today":   {DE: "heute", EN: "Today"},
	"badge.left":    {DE: "T übrig", EN: "d left"},

	// ── Admin ───────────────────────────────────────────────────────────
	"admin.title":    {DE: "KI-API-Schlüssel-Portal — Administration", EN: "AI API Key Portal — Admin"},
	"admin.profiles": {DE: "Profile", EN: "Profiles"},
	"admin.users":    {DE: "Benutzende", EN: "Users"},
	"admin.audit":    {DE: "Audit-Log", EN: "Audit log"},

	"admin.col.name":          {DE: "Name", EN: "Name"},
	"admin.col.description":   {DE: "Beschreibung", EN: "Description"},
	"admin.col.models":        {DE: "Modelle", EN: "Models"},
	"admin.col.expiry":        {DE: "Gültigkeit", EN: "Expiry"},
	"admin.col.quota":         {DE: "Kontingent", EN: "Quota"},
	"admin.col.default":       {DE: "Standard", EN: "Default"},
	"admin.col.email":         {DE: "E-Mail", EN: "Email"},
	"admin.col.profile":       {DE: "Profil", EN: "Profile"},
	"admin.col.apikey":        {DE: "API-Schlüssel", EN: "API key"},
	"admin.col.changeprofile": {DE: "Profil ändern", EN: "Change profile"},
	"admin.col.when":          {DE: "Zeitpunkt", EN: "When"},
	"admin.col.action":        {DE: "Aktion", EN: "Action"},
	"admin.col.actor":         {DE: "Ausgeführt von", EN: "Actor"},
	"admin.col.subject":       {DE: "Betroffen", EN: "Subject"},
	"admin.col.detail":        {DE: "Detail", EN: "Detail"},

	"admin.all":        {DE: "alle", EN: "all"},
	"admin.none":       {DE: "keiner", EN: "none"},
	"admin.default":    {DE: "Standard", EN: "default"},
	"admin.edit":       {DE: "Bearbeiten", EN: "Edit"},
	"admin.delete":     {DE: "Löschen", EN: "Delete"},
	"admin.revoke":     {DE: "Widerrufen", EN: "Revoke"},
	"admin.save":       {DE: "Speichern", EN: "Save"},
	"admin.reset":      {DE: "Zurücksetzen", EN: "Reset"},
	"admin.noprofiles": {DE: "Noch keine Profile.", EN: "No profiles yet."},
	"admin.nousers":    {DE: "Noch keine Benutzenden.", EN: "No users yet."},
	"admin.noevents":   {DE: "Noch keine Ereignisse erfasst.", EN: "No events recorded yet."},
	"admin.audit.note": {DE: "Die 50 jüngsten Schlüssel- und Profiländerungen.", EN: "The 50 most recent key and profile changes."},

	"admin.form.new":           {DE: "Neues Profil", EN: "New profile"},
	"admin.form.edit":          {DE: "Profil bearbeiten:", EN: "Edit profile:"},
	"admin.form.create":        {DE: "Profil anlegen", EN: "Create profile"},
	"admin.form.update":        {DE: "Profil aktualisieren", EN: "Update profile"},
	"admin.form.models":        {DE: "Erlaubte Modelle", EN: "Allowed models"},
	"admin.form.models.hint":   {DE: "Leer lassen, um alle Modelle zu erlauben.", EN: "Leave blank to allow all models."},
	"admin.form.tpm":           {DE: "TPM-Limit", EN: "TPM limit"},
	"admin.form.rpm":           {DE: "RPM-Limit", EN: "RPM limit"},
	"admin.form.validity":      {DE: "Gültigkeit (Tage)", EN: "Key validity (days)"},
	"admin.form.validity.hint": {DE: "Wie lange ein erzeugter Schlüssel gilt. Leer = Server-Standard.", EN: "How long a generated key lasts. Blank uses the server default."},
	"admin.form.quota":         {DE: "Nutzungslimit (Token)", EN: "Usage limit (tokens)"},
	"admin.form.quota.hint":    {DE: "Kontingent pro Zeitraum. Leer bedeutet kein Limit.", EN: "Fair-use allowance per period. Blank means no limit."},
	"admin.form.period":        {DE: "Limit zurücksetzen", EN: "Limit resets"},
	"admin.form.period.hint":   {DE: "Anfragen schlagen fehl, sobald das Limit erreicht ist, und funktionieren beim nächsten Zurücksetzen wieder. Zeitpunkte in UTC (täglich = Mitternacht UTC).", EN: "Requests fail once the limit is hit, then resume automatically at the next reset. Boundaries are UTC (daily = midnight UTC)."},
	"admin.form.default":       {DE: "Standardprofil", EN: "Default profile"},
	"admin.form.unlimited":     {DE: "unbegrenzt", EN: "unlimited"},
	"admin.form.serverdefault": {DE: "Server-Standard", EN: "server default"},

	"period.none":    {DE: "kein Limit", EN: "no limit"},
	"period.hourly":  {DE: "stündlich", EN: "hourly"},
	"period.daily":   {DE: "täglich", EN: "daily"},
	"period.weekly":  {DE: "wöchentlich", EN: "weekly"},
	"period.monthly": {DE: "monatlich", EN: "monthly"},

	"period.perhour":  {DE: "pro Stunde", EN: "per hour"},
	"period.perday":   {DE: "pro Tag", EN: "per day"},
	"period.perweek":  {DE: "pro Woche", EN: "per week"},
	"period.permonth": {DE: "pro Monat", EN: "per month"},

	// ── Tooltips ────────────────────────────────────────────────────────
	"help.tpm":            {DE: "Token pro Minute. Begrenzt, wie schnell ein Schlüssel Token verbrauchen darf, und glättet Lastspitzen. Das Gesamtvolumen begrenzt das Nutzungslimit.", EN: "Tokens per minute. Caps how fast a key can consume tokens, smoothing out bursts. It does not limit the total — use the usage limit for that."},
	"help.rpm":            {DE: "Anfragen pro Minute. Begrenzt die Anzahl der API-Aufrufe pro Minute, unabhängig von deren Größe.", EN: "Requests per minute. Caps how many API calls a key can make each minute, regardless of their size."},
	"help.expiry":         {DE: "Wie lange ein neu erzeugter Schlüssel gültig bleibt. Nutzende können ihn selbst um einen vollen Zeitraum verlängern.", EN: "How long a newly generated key stays valid. Users can extend it themselves for another full period."},
	"help.quota":          {DE: "Token-Kontingent und wie oft es zurückgesetzt wird. Anfragen schlagen fehl, sobald es aufgebraucht ist, und funktionieren nach dem Zurücksetzen wieder.", EN: "Fair-use token allowance and how often it resets. Requests fail once it is used up, then resume at the next reset."},
	"help.models":         {DE: "Beschränkt dieses Profil auf bestimmte Modelle. Leer lassen, um alle Modelle des Gateways zu erlauben.", EN: "Restrict this profile to specific models. Leave blank to allow every model the gateway offers."},
	"help.validity":       {DE: "Wie viele Tage ein erzeugter Schlüssel gilt. Studierende z. B. 30, Beschäftigte 365. Nutzende können jederzeit um einen vollen Zeitraum verlängern. Leer = Server-Standard.", EN: "How many days a generated key lasts. Students might get 30, staff 365. Users can click Extend for another full period at any time. Blank uses the server default."},
	"help.usagelimit":     {DE: "Gesamtzahl der Token pro Zeitraum, z. B. 1000000 für eine Million. Ist das Kontingent verbraucht, schlagen Anfragen fehl, bis der Zeitraum zurückgesetzt wird. Leer = kein Limit. Wirkt nur mit einem Zeitraum.", EN: "Total tokens allowed per reset period, e.g. 1000000 for a million tokens. Once spent, requests fail until the period resets. Blank means no limit. Needs a reset period to take effect."},
	"help.period":         {DE: "Wie oft das Nutzungslimit auf null zurückgesetzt wird. Die Zurücksetzung erfolgt zu festen UTC-Zeitpunkten — täglich um Mitternacht UTC, wöchentlich montags, monatlich am 1. — nicht rollierend ab Erstellung des Schlüssels. Nur ein Zeitraum pro Profil: Das Gateway kann kein Tages- und Monatslimit gleichzeitig durchsetzen.", EN: "How often the usage limit goes back to zero. Resets happen on fixed UTC boundaries — daily at midnight UTC, weekly on Monday, monthly on the 1st — not a rolling window from when the key was made. Only one period per profile: the gateway cannot enforce a daily and a monthly cap at the same time."},
	"help.defaultprofile": {DE: "Gilt für alle Nutzenden ohne ausdrücklich zugewiesenes Profil. Genau ein Profil ist Standard; ein neues zu markieren verschiebt ihn.", EN: "Applied to every user who has not been assigned a profile explicitly. Exactly one profile is the default; marking a new one moves it."},

	// ── Flash messages ──────────────────────────────────────────────────
	"flash.profile.created": {DE: "Profil angelegt", EN: "Profile created"},
	"flash.profile.updated": {DE: "Profil aktualisiert", EN: "Profile updated"},
	"flash.profile.deleted": {DE: "Profil gelöscht", EN: "Profile deleted"},
	"flash.user.updated":    {DE: "Profil der Person aktualisiert", EN: "User profile updated"},
	"flash.key.revoked":     {DE: "Schlüssel widerrufen", EN: "Key revoked"},
	"flash.name.required":   {DE: "Name ist erforderlich", EN: "Name is required"},
	"flash.period.invalid":  {DE: "Ungültiger Zeitraum", EN: "Invalid quota period"},
	"flash.nokey":           {DE: "Diese Person hat keinen Schlüssel", EN: "User has no key"},
	"flash.failed":          {DE: "Aktion fehlgeschlagen", EN: "Action failed"},
}

// T returns the message for key in lang, falling back to English and then to
// the key itself so a missing translation is visible rather than blank.
func T(lang Lang, key string) string {
	m, ok := messages[key]
	if !ok {
		return key
	}
	if s, ok := m[lang]; ok && s != "" {
		return s
	}
	if s, ok := m[EN]; ok && s != "" {
		return s
	}
	return key
}
