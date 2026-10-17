package devseed

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
)

// Client is one person's browser, as far as the seed needs one: a cookie jar,
// and the forms the app's pages post. It follows no redirect on its own, so
// the seed can read the flash message an admin action redirects with.
type Client struct {
	base string
	http *http.Client
	csrf string
}

// NewClient returns a client for the app at base, with no session yet.
func NewClient(base string) *Client {
	jar, _ := cookiejar.New(nil)
	return &Client{
		base: strings.TrimRight(base, "/"),
		http: &http.Client{
			Jar: jar,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// Login signs in as sub through the OIDC mock: the app's /login sends the
// browser to the mock's authorize page, whose form posts the subject back to
// the same URL, and the mock redirects to the app's callback.
func (c *Client) Login(ctx context.Context, sub string) error {
	resp, err := c.do(ctx, http.MethodGet, c.base+"/login", nil)
	if err != nil {
		return err
	}
	authorize, err := location(resp)
	if err != nil {
		return fmt.Errorf("login: %w", err)
	}

	form := url.Values{"sub": {sub}}
	resp, err = c.do(ctx, http.MethodPost, authorize, form)
	if err != nil {
		return err
	}
	callback, err := location(resp)
	if err != nil {
		return fmt.Errorf("login at the OIDC mock: %w", err)
	}

	resp, err = c.do(ctx, http.MethodGet, callback, nil)
	if err != nil {
		return err
	}
	if next, err := location(resp); err != nil || next != "/" {
		return fmt.Errorf("login callback for %s: redirect %q, %v; see the server log", sub, next, err)
	}
	_, err = c.Page(ctx, "/")
	return err
}

// Page fetches a page and remembers the CSRF token it carries for the forms
// posted after it.
func (c *Client) Page(ctx context.Context, path string) (string, error) {
	resp, err := c.do(ctx, http.MethodGet, c.base+path, nil)
	if err != nil {
		return "", err
	}
	body, err := readBody(resp)
	if err != nil {
		return "", err
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GET %s: %d", path, resp.StatusCode)
	}
	if tok := CSRFToken(body); tok != "" {
		c.csrf = tok
	}
	return body, nil
}

// Post submits a form with the CSRF token and returns where the app redirects.
func (c *Client) Post(ctx context.Context, path string, form url.Values) (string, error) {
	if c.csrf == "" {
		return "", errors.New("no CSRF token yet: fetch a page first")
	}
	if form == nil {
		form = url.Values{}
	}
	form.Set("csrf_token", c.csrf)
	resp, err := c.do(ctx, http.MethodPost, c.base+path, form)
	if err != nil {
		return "", err
	}
	next, err := location(resp)
	if err != nil {
		return "", fmt.Errorf("POST %s: %w", path, err)
	}
	return next, nil
}

// Admin submits an admin form and checks the flash message it redirects with,
// which is the only thing that says whether it worked.
func (c *Client) Admin(ctx context.Context, path string, form url.Values, wantFlash string) error {
	next, err := c.Post(ctx, path, form)
	if err != nil {
		return err
	}
	if got := Flash(next); got != wantFlash {
		return fmt.Errorf("POST %s: flash %q, want %q", path, got, wantFlash)
	}
	return nil
}

// GenerateKey generates the user's key and returns its secret, which the
// dashboard shows once.
func (c *Client) GenerateKey(ctx context.Context) (string, error) {
	next, err := c.Post(ctx, "/key/generate", nil)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(next, "/?k=") {
		return "", fmt.Errorf("generate key: redirect %q, want the new key's page", next)
	}
	body, err := c.Page(ctx, next)
	if err != nil {
		return "", err
	}
	secret := NewKey(body)
	if secret == "" {
		return "", errors.New("generate key: the page shows no new key")
	}
	return secret, nil
}

func (c *Client) do(ctx context.Context, method, target string, form url.Values) (*http.Response, error) {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, target, body)
	if err != nil {
		return nil, err
	}
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, target, err)
	}
	return resp, nil
}

// location is where a redirect points; anything but a redirect is an error.
func location(resp *http.Response) (string, error) {
	body, _ := readBody(resp)
	if resp.StatusCode < 300 || resp.StatusCode > 399 {
		return "", fmt.Errorf("status %d, want a redirect: %.200s", resp.StatusCode, body)
	}
	return resp.Header.Get("Location"), nil
}

func readBody(resp *http.Response) (string, error) {
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	return string(b), err
}

var (
	csrfRe   = regexp.MustCompile(`name="csrf_token" value="([^"]+)"`)
	newKeyRe = regexp.MustCompile(`<span id="new-key-val">([^<]+)</span>`)
)

// CSRFToken is the token in a page's forms, or "" when it has none.
func CSRFToken(page string) string {
	if m := csrfRe.FindStringSubmatch(page); m != nil {
		return html.UnescapeString(m[1])
	}
	return ""
}

// NewKey is the secret a dashboard shows right after generating a key, or ""
// when it shows none.
func NewKey(page string) string {
	if m := newKeyRe.FindStringSubmatch(page); m != nil {
		return html.UnescapeString(m[1])
	}
	return ""
}

// Flash is the message an admin action redirects with.
func Flash(location string) string {
	u, err := url.Parse(location)
	if err != nil {
		return ""
	}
	return u.Query().Get("flash")
}

// Complete sends one chat request with a key, as a client of the gateway
// would. max_tokens keeps LiteLLM from reserving the model's whole output
// limit against a small budget, which would refuse the next request.
func Complete(ctx context.Context, gateway, secret, model string) error {
	body, _ := json.Marshal(map[string]any{
		"model":      model,
		"messages":   []map[string]string{{"role": "user", "content": "hello"}},
		"max_tokens": 20,
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		strings.TrimRight(gateway, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+secret)
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("chat request: %w", err)
	}
	b, _ := readBody(resp)
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("chat request on %s: %d: %.200s", model, resp.StatusCode, b)
	}
	return nil
}
