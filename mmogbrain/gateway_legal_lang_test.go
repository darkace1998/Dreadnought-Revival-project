package main

import (
	"net/http/httptest"
	"strings"
	"testing"
)

// A German client asks /legal/de/text; it must get the same answer as en, with
// the Code field (a bare {} stalled its sign-in).
func TestLegalTextForEveryLanguage(t *testing.T) {
	for _, lang := range []string{"en", "de", "fr"} {
		rec := httptest.NewRecorder()
		handleGWLegalByLanguage(rec, httptest.NewRequest("GET", "/api/v1/account/legal/"+lang+"/text", nil), nil)
		if !strings.Contains(rec.Body.String(), `"Code"`) {
			t.Errorf("%s: body %q has no Code", lang, rec.Body.String())
		}
	}
}
