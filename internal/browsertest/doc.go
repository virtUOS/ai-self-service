// Package browsertest drives the app in a real browser: Chromium, through
// Playwright. It covers what the handler tests cannot see, the pages'
// JavaScript and how the pages behave together with the OIDC mock and the
// LiteLLM gateway.
//
// The tests are built only with the browser tag. They start an instance of
// their own, seeded by internal/devseed, against the dev stack:
//
//	make browser-install   # once: the Playwright driver and Chromium
//	make browser
package browsertest
