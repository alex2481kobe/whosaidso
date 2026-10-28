// Viewer chrome keeps workspace navigation and one theme action visible.
// Runtime persistence and icon changes are checked by the headless modules.
package ui

import (
	"strings"
	"testing"
)

func TestNavigationOmitsProductBrandAndThemeIsOneButton(t *testing.T) {
	raw, err := files.ReadFile("index.html")
	if err != nil {
		t.Fatal(err)
	}
	page := string(raw)
	_, sidebar, ok := strings.Cut(page, `<header class="topbar">`)
	if !ok {
		t.Fatal("missing navigation")
	}
	sidebar, _, _ = strings.Cut(sidebar, "</header>")
	if strings.Contains(sidebar, "WhoSaidSo") || strings.Contains(sidebar, "owl.png") || strings.Contains(sidebar, `class="brand"`) {
		t.Fatal("product branding remains in navigation")
	}
	for _, route := range []string{"#/home", "#/tasks", "#/records", "#/history"} {
		if !strings.Contains(sidebar, `href="`+route+`"`) {
			t.Errorf("missing screen link %s", route)
		}
	}
	_, theme, ok := strings.Cut(page, `<div class="theme-control" hidden>`)
	if !ok {
		t.Fatal("missing theme action")
	}
	theme, _, _ = strings.Cut(theme, "</div>")
	if strings.Count(theme, "<button ") != 1 || strings.Contains(theme, "<span") || !strings.Contains(theme, `id="theme-toggle"`) {
		t.Fatalf("theme must be one icon button: %s", theme)
	}
}
