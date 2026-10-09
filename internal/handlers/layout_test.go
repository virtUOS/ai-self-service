package handlers

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/virtuos/ai-self-service/web"
)

func stylesheet(t *testing.T) string {
	t.Helper()
	css, err := fs.ReadFile(web.StaticFS, "style.css")
	if err != nil {
		t.Fatal(err)
	}
	return string(css)
}

// The dashboard is long enough — account, key, usage, models — that the header
// scrolls out of reach. It holds the language switch, the admin link and
// logout, so it follows the page down rather than making users scroll back up.
func TestHeaderSticksToTheTop(t *testing.T) {
	css := stylesheet(t)

	header := css[strings.Index(css, "header {"):]
	header = header[:strings.Index(header, "}")]

	if !strings.Contains(header, "position: sticky") {
		t.Error("header does not stay in view when the page scrolls")
	}
	if !strings.Contains(header, "top: 0") {
		t.Error("a sticky header without a top offset never sticks")
	}
	// Cards scroll underneath it, so it has to win the stacking order.
	if !strings.Contains(header, "z-index") {
		t.Error("header has no z-index, so content scrolls over it")
	}
}

// The header used to wrap onto two or three lines on a phone. Everything but
// the language switch now lives in the account menu, so it keeps one row of
// fixed height, and a long username is cut instead of forcing a wrap.
func TestHeaderStaysOneRowOnNarrowScreens(t *testing.T) {
	css := stylesheet(t)

	i := strings.Index(css, "@media (max-width: 600px)")
	if i < 0 {
		t.Fatal("no mobile breakpoint")
	}
	mobile := css[i:]
	j := strings.Index(mobile, "header {")
	if j < 0 {
		t.Fatal("no mobile header rule")
	}
	header := mobile[j : j+strings.Index(mobile[j:], "}")]
	for _, unwanted := range []string{"height: auto", "flex-wrap: wrap"} {
		if strings.Contains(header, unwanted) {
			t.Errorf("mobile header has %q, so it wraps again", unwanted)
		}
	}

	k := strings.Index(css, ".account-name {")
	if k < 0 {
		t.Fatal("no .account-name rule")
	}
	if name := css[k : k+strings.Index(css[k:], "}")]; !strings.Contains(name, "text-overflow: ellipsis") {
		t.Error("a long username is not truncated")
	}
}

// The account card is a two-column grid whose label column sizes to its
// content. On a narrow screen a long label ("Nutzungslimit") leaves the value
// squeezed against the edge, so the columns stack instead.
func TestInfoGridStacksOnNarrowScreens(t *testing.T) {
	css := stylesheet(t)

	i := strings.Index(css, "@media (max-width: 600px)")
	if i < 0 {
		t.Fatal("no mobile breakpoint")
	}
	if !strings.Contains(css[i:], ".info-grid { grid-template-columns: 1fr;") {
		t.Error("the info grid keeps two columns on a narrow screen")
	}
}

// The example request keeps its line breaks — it is a shell command with
// backslash continuations — while still wrapping long model names rather than
// forcing the card to scroll sideways.
func TestCurlExampleWraps(t *testing.T) {
	css := stylesheet(t)

	i := strings.Index(css, ".curl-code {")
	if i < 0 {
		t.Fatal("no .curl-code rule")
	}
	rule := css[i:]
	rule = rule[:strings.Index(rule, "}")]

	if !strings.Contains(rule, "white-space: pre-wrap") {
		t.Error("the command loses its line breaks, or does not wrap")
	}
	if !strings.Contains(rule, "word-break: break-word") {
		t.Error("a long model name would push the card sideways")
	}
	if !strings.Contains(rule, "background: var(--code-bg)") {
		t.Error("the code shares the panel background and blends into the prose around it")
	}
}

// Everything copied verbatim shares one monospace. Courier New renders too
// thin to set code apart from the prose around it.
func TestOneMonospace(t *testing.T) {
	if strings.Contains(stylesheet(t), "Courier New") {
		t.Error("a rule still uses Courier New instead of var(--font-mono)")
	}
}

// Pointer targets need 24x24 CSS px (WCAG 2.5.8). The help dot and the copy
// buttons are sized for a mouse, which leaves them fiddly on a phone, so the
// mobile breakpoint grows their hit area.
func TestTouchTargetsAreLargeEnoughOnMobile(t *testing.T) {
	css := stylesheet(t)

	i := strings.Index(css, "@media (max-width: 600px)")
	if i < 0 {
		t.Fatal("no mobile breakpoint")
	}
	mobile := css[i:]

	// The dot keeps its drawn size and gains an invisible tap area, so only
	// the hit box grows — enlarging the circle itself would shout.
	if !strings.Contains(mobile, ".help::after") {
		t.Error("the help dot has no enlarged tap area on mobile")
	}
	for _, want := range []string{
		".copy-btn { min-height: 24px; }",
		".checkbox-row input[type=checkbox] { width: 24px; height: 24px; }",
	} {
		if !strings.Contains(mobile, want) {
			t.Errorf("mobile stylesheet is missing %q", want)
		}
	}
}
