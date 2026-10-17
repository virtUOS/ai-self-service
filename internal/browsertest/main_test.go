//go:build browser

package browsertest

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/mxschmitt/playwright-go"

	"github.com/virtuos/keyosk/internal/devseed"
)

// The seeded instance and the browser every test shares. Each test opens
// contexts of its own, so cookies, storage and permissions stay apart, and
// the tests run in parallel.
var (
	seeded  *devseed.Manifest
	browser playwright.Browser
	expect  = playwright.NewPlaywrightAssertions(10000)
)

// The instance listens next to the one `make seed` runs, so both can run at
// once.
const testAddr = "127.0.0.1:8091"

func TestMain(m *testing.M) {
	os.Exit(run(m))
}

func run(m *testing.M) int {
	ctx := context.Background()
	// Only this run's failures leave a trace.
	_ = os.RemoveAll(filepath.Join("testdata", "traces"))
	dir, err := os.MkdirTemp("", "keyosk-browser-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	defer os.RemoveAll(dir)

	inst, err := devseed.NewInstance(dir, testAddr)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	// CI starts no Mailpit; the reminder for the expiring key goes nowhere.
	inst.Env["SMTP_HOST"] = ""
	defer inst.Stop()
	// Whatever happened, the run's keys and users leave LiteLLM with it.
	defer func() {
		if err := devseed.Cleanup(ctx, inst.DBPath(), inst.Prefix); err != nil {
			fmt.Fprintln(os.Stderr, "cleanup:", err)
		}
	}()

	if seeded, err = devseed.Seed(ctx, inst, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "seed:", err)
		if log, err := os.ReadFile(inst.LogPath()); err == nil {
			fmt.Fprintf(os.Stderr, "server log:\n%s", log)
		}
		return 1
	}

	pw, err := playwright.Run(&playwright.RunOptions{Verbose: false})
	if err != nil {
		fmt.Fprintf(os.Stderr, "start Playwright: %v\nRun `make browser-install` once first.\n", err)
		return 1
	}
	defer pw.Stop()
	if browser, err = pw.Chromium.Launch(); err != nil {
		fmt.Fprintf(os.Stderr, "launch Chromium: %v\nRun `make browser-install` once first.\n", err)
		return 1
	}
	defer browser.Close()

	return m.Run()
}

// pageOptions vary the browser a test gets.
type pageOptions struct {
	locale      string
	viewport    *playwright.Size
	clipboard   bool
	colorScheme *playwright.ColorScheme
	// consoleErrors lets a page log errors without failing the test, for
	// tests that make requests fail on purpose.
	consoleErrors bool
}

// newContext opens a browser context for one test. It records a trace, which
// is saved when the test fails, and closes with the test.
func newContext(t *testing.T, o pageOptions) playwright.BrowserContext {
	t.Helper()
	if o.locale == "" {
		o.locale = "en-US"
	}
	opts := playwright.BrowserNewContextOptions{
		BaseURL:     playwright.String(seeded.URL),
		Locale:      playwright.String(o.locale),
		Viewport:    o.viewport,
		ColorScheme: o.colorScheme,
	}
	if o.clipboard {
		opts.Permissions = []string{"clipboard-read", "clipboard-write"}
	}
	bc, err := browser.NewContext(opts)
	if err != nil {
		t.Fatal(err)
	}
	bc.SetDefaultTimeout(10000)
	if err := bc.Tracing().Start(playwright.TracingStartOptions{
		Screenshots: playwright.Bool(true),
		Snapshots:   playwright.Bool(true),
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if t.Failed() {
			path := tracePath(t)
			_ = bc.Tracing().Stop(path)
			t.Logf("trace: go run github.com/mxschmitt/playwright-go/cmd/playwright show-trace %s", path)
		} else {
			_ = bc.Tracing().Stop()
		}
		_ = bc.Close()
	})
	return bc
}

// newPage opens a page in a context of its own and fails the test on any
// script error or console error it reports.
func newPage(t *testing.T, o pageOptions) playwright.Page {
	t.Helper()
	page, err := newContext(t, o).NewPage()
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var problems []string
	page.OnPageError(func(err error) {
		mu.Lock()
		defer mu.Unlock()
		problems = append(problems, "script error: "+err.Error())
	})
	page.OnConsole(func(m playwright.ConsoleMessage) {
		if m.Type() != "error" || o.consoleErrors {
			return
		}
		mu.Lock()
		defer mu.Unlock()
		problems = append(problems, fmt.Sprintf("console error at %s: %s", page.URL(), m.Text()))
	})
	t.Cleanup(func() {
		mu.Lock()
		defer mu.Unlock()
		for _, p := range problems {
			t.Error(p)
		}
	})
	return page
}

// login signs in as sub at the OIDC mock, the way a person does: /login
// sends the browser there, and the mock's form takes a subject.
func login(t *testing.T, page playwright.Page, sub string) {
	t.Helper()
	if _, err := page.Goto("/login"); err != nil {
		t.Fatal(err)
	}
	input := page.Locator("#subject-input")
	if err := input.Fill(sub); err != nil {
		t.Fatalf("the OIDC mock's login form: %v", err)
	}
	if err := input.Press("Enter"); err != nil {
		t.Fatal(err)
	}
	if err := page.WaitForURL(seeded.URL + "/"); err != nil {
		t.Fatalf("login as %s did not reach the dashboard: %v", sub, err)
	}
}

// loggedIn is a page signed in as sub, on the dashboard.
func loggedIn(t *testing.T, sub string, o pageOptions) playwright.Page {
	t.Helper()
	page := newPage(t, o)
	login(t, page, sub)
	return page
}

func goTo(t *testing.T, page playwright.Page, path string) {
	t.Helper()
	if _, err := page.Goto(path); err != nil {
		t.Fatal(err)
	}
}

func click(t *testing.T, l playwright.Locator) {
	t.Helper()
	if err := l.Click(); err != nil {
		t.Fatal(err)
	}
}

// check fails the test on an assertion error, which carries what was found.
func check(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

var unsafeChars = regexp.MustCompile(`[^A-Za-z0-9_.-]+`)

func tracePath(t *testing.T) string {
	dir, _ := filepath.Abs(filepath.Join("testdata", "traces"))
	_ = os.MkdirAll(dir, 0o755)
	return filepath.Join(dir, strings.Trim(unsafeChars.ReplaceAllString(t.Name(), "_"), "_")+".zip")
}
