package main

import (
	"html"
	"net/url"
	"os"
	"path/filepath"
	"testing"
)

func TestDecodeEmbeddedJSONRaw(t *testing.T) {
	body := []byte(`<html><script>window.__STATE__={"query":"go","vacancies":[{"vacancyId":1,"name":"Go Dev"}]}</script></html>`)

	var vacancies []Vacancy
	if err := decodeEmbeddedJSON(body, `,"vacancies":`, &vacancies); err != nil {
		t.Fatalf("raw decode failed: %v", err)
	}
	if len(vacancies) != 1 || vacancies[0].ID != 1 || vacancies[0].Name != "Go Dev" {
		t.Fatalf("unexpected vacancies: %+v", vacancies)
	}
}

func TestDecodeEmbeddedJSONHTMLEscaped(t *testing.T) {
	// hh.ru wraps its initial state in HTML, so quotes end up escaped as
	// entities and a marker that contains quotes never matches the raw body.
	raw := `<html><template>{"query":"go","vacancies":[{"vacancyId":2,"name":"Escaped"}]}</template></html>`
	escaped := html.EscapeString(raw)
	if escaped == raw {
		t.Fatal("expected html.EscapeString to change the body")
	}

	var vacancies []Vacancy
	if err := decodeEmbeddedJSON([]byte(escaped), `,"vacancies":`, &vacancies); err != nil {
		t.Fatalf("escaped decode failed: %v", err)
	}
	if len(vacancies) != 1 || vacancies[0].ID != 2 || vacancies[0].Name != "Escaped" {
		t.Fatalf("unexpected vacancies: %+v", vacancies)
	}
}

func TestDecodeEmbeddedJSONFirstKey(t *testing.T) {
	// The field can also be the first key of an object, so the leading comma in
	// the marker is optional.
	body := []byte(`<script>window.__STATE__={"vacancies":[{"vacancyId":3}]}</script>`)

	var vacancies []Vacancy
	if err := decodeEmbeddedJSON(body, `,"vacancies":`, &vacancies); err != nil {
		t.Fatalf("first-key decode failed: %v", err)
	}
	if len(vacancies) != 1 || vacancies[0].ID != 3 {
		t.Fatalf("unexpected vacancies: %+v", vacancies)
	}
}

func TestIsValidCookieValue(t *testing.T) {
	valid := []string{"abc", "a-b_c.d", "9_6r1fVOVOIpVBd1rTyYiqd8xk0w", "Europe%2FMoscow", "a+b/c="}
	for _, v := range valid {
		if !isValidCookieValue(v) {
			t.Errorf("expected %q to be a valid cookie value", v)
		}
	}

	invalid := []string{`{"showFilters":false}`, "a;b", `a\b`, "a\nb", "caf\u00e9"}
	for _, v := range invalid {
		if isValidCookieValue(v) {
			t.Errorf("expected %q to be an invalid cookie value", v)
		}
	}
}

func TestJarDoesNotSendInvalidCookieValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cookies.txt")
	content := "# Netscape HTTP Cookie File\n" +
		"hh.ru\tTRUE\t/\tFALSE\t0\t_xsrf\tabc123\n" +
		"hh.ru\tTRUE\t/\tFALSE\t0\tfilters1\t{\"showFilters\":false}\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}

	jar, err := NewMemoryPersistentJar(path)
	if err != nil {
		t.Fatal(err)
	}

	u, err := url.Parse("https://hh.ru")
	if err != nil {
		t.Fatal(err)
	}

	sent := make(map[string]bool)
	for _, c := range jar.Cookies(u) {
		sent[c.Name] = true
	}

	if !sent["_xsrf"] {
		t.Fatalf("expected _xsrf to be sent, got %v", sent)
	}
	if sent["filters1"] {
		t.Fatalf("cookie with invalid value must not be sent, got %v", sent)
	}

	// The invalid cookie is still persisted for later use.
	reloaded, err := NewMemoryPersistentJar(path)
	if err != nil {
		t.Fatal(err)
	}
	persisted := false
	for _, list := range reloaded.cookies {
		for _, c := range list {
			if c.Name == "filters1" {
				persisted = true
			}
		}
	}
	if !persisted {
		t.Fatal("invalid cookie should remain persisted in the jar")
	}
}

func TestIsVacancySearchConfigured(t *testing.T) {
	cases := []struct {
		name      string
		searchURL string
		want      bool
	}{
		{"empty", "", false},
		{"spaces", "   ", false},
		{"whitespace only", "\t\n ", false},
		{"url", "https://hh.ru/search/vacancy?text=go", true},
		{"trimmed url", "  https://hh.ru/search/vacancy  ", true},
	}

	for _, tc := range cases {
		if got := isVacancySearchConfigured(tc.searchURL); got != tc.want {
			t.Errorf("%s: isVacancySearchConfigured(%q) = %v, want %v",
				tc.name, tc.searchURL, got, tc.want)
		}
	}
}

func TestResumeSearchParamsChatOnlyMode(t *testing.T) {
	// Without a search URL the app must not fabricate search params from the
	// selected resume, otherwise it silently starts searching and applying.
	params := resumeSearchParams(false, nil, "resume-hash")
	if len(params) != 0 {
		t.Fatalf("expected no search params in chat-only mode, got %v", params)
	}
}

func TestResumeSearchParamsAddsResumeWhenEmpty(t *testing.T) {
	params := resumeSearchParams(true, nil, "resume-hash")
	if got := params.Get("resume"); got != "resume-hash" {
		t.Fatalf("expected resume=resume-hash, got %q", got)
	}
}

func TestResumeSearchParamsKeepsExplicitFilters(t *testing.T) {
	// An explicit search URL already carries its own query string, so the
	// resume fallback must not overwrite it.
	explicit := url.Values{}
	explicit.Set("text", "go")
	explicit.Set("resume", "explicit-hash")

	params := resumeSearchParams(true, explicit, "resume-hash")
	if got := params.Get("text"); got != "go" {
		t.Fatalf("expected text=go to be preserved, got %q", got)
	}
	if got := params.Get("resume"); got != "explicit-hash" {
		t.Fatalf("expected explicit resume to be preserved, got %q", got)
	}
}
