package i18n

import "testing"

// A handler that forgets to set Lang must still render readable text rather
// than raw catalogue keys, so the failure is cosmetic instead of a broken page.
func TestZeroLangFallsBackToEnglish(t *testing.T) {
	var zero Lang
	if got := T(zero, "help.tpm"); got == "help.tpm" || got == "" {
		t.Fatalf("zero Lang produced %q", got)
	}
}
