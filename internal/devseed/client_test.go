package devseed

import "testing"

func TestCSRFToken(t *testing.T) {
	page := `<form method="POST" action="/logout">
  <input type="hidden" name="csrf_token" value="ab-c_d&#43;e">
</form>`
	if got := CSRFToken(page); got != "ab-c_d+e" {
		t.Errorf("CSRFToken = %q, want the unescaped value", got)
	}
	if got := CSRFToken("<p>no form</p>"); got != "" {
		t.Errorf("CSRFToken of a page without a form = %q, want empty", got)
	}
}

func TestNewKey(t *testing.T) {
	page := `<div class="new-key"><span id="new-key-val">sk-abc123</span>
<button class="copy-btn">Copy</button></div>`
	if got := NewKey(page); got != "sk-abc123" {
		t.Errorf("NewKey = %q, want sk-abc123", got)
	}
	if got := NewKey(`<code>sk-abc…</code>`); got != "" {
		t.Errorf("NewKey of a page showing only the prefix = %q, want empty", got)
	}
}

func TestFlash(t *testing.T) {
	cases := map[string]string{
		"/admin?flash=Profile+created":            "Profile created",
		"/admin?flash=User+profile+updated#users": "User profile updated",
		"/admin": "",
	}
	for location, want := range cases {
		if got := Flash(location); got != want {
			t.Errorf("Flash(%q) = %q, want %q", location, got, want)
		}
	}
}
