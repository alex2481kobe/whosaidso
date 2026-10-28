// Static page design rules belong here; HTTP transport guards do not.
package ui

import (
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// The page uses small rounded rectangles, no dot separators, checkboxes,
// profile images or saved views. Product branding stays out of navigation.
func TestPageKeepsTheOwnersDesignRules(t *testing.T) {
	page := []string{"index.html", "ui.css", "ui-screens.css", "ui.js", "ui-icons.js", "ui-parts.js", "ui-shell.js", "ui-browser.js", "ui-detail.js", "ui-kinds.js", "ui-gallery-actions.js", "ui-gallery-media.js", "ui-gallery-inspect.js", "ui-gallery.js", "ui-gallery.css"}
	images := regexp.MustCompile(`/[A-Za-z0-9_-]+\.(png|jpe?g|gif|webp|svg)\b`)
	radius := regexp.MustCompile(`border-radius:\s*(\d+)px`)
	capsules := 0
	for _, file := range page {
		b, err := files.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		text := string(b)
		lower := strings.ToLower(text)
		for _, banned := range []string{"·", "•", "<template", "type=\"checkbox\"", "type = \"checkbox\"", "type: \"checkbox\"", "avatar", "profile", "saved view"} {
			if strings.Contains(lower, strings.ToLower(banned)) {
				t.Errorf("%s carries %q", file, banned)
			}
		}
		for _, image := range images.FindAllString(text, -1) {
			if image != "/favicon-32.png" && image != "/favicon-180.png" {
				t.Errorf("%s draws %s; only the favicons are embedded pictures", file, image)
			}
		}
		for _, m := range radius.FindAllStringSubmatch(text, -1) {
			if px, _ := strconv.Atoi(m[1]); px >= 99 {
				capsules++
			} else if px > 16 {
				t.Errorf("%s rounds a corner %dpx, rounder than a rounded rectangle", file, px)
			}
		}
	}
	if capsules != 0 {
		t.Errorf("%d capsule shapes; controls use rounded rectangles", capsules)
	}
}
