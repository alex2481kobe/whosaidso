// Legacy absolute locators may move with a project. Only paths anchored in the
// current project directory's name are candidates; opens still enforce containment.
package gallery

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func absoluteLocator(locator string) bool {
	return filepath.IsAbs(locator) || strings.HasPrefix(locator, "/") || strings.HasPrefix(locator, "\\") || len(locator) > 2 && locator[1] == ':' && (locator[2] == '/' || locator[2] == '\\')
}

func relocatedLocator(root, locator string) (string, error) {
	anchor := filepath.Base(filepath.Clean(root))
	parts := strings.Split(strings.ReplaceAll(locator, "\\", "/"), "/")
	candidate := ""
	for i, part := range parts {
		if part != anchor || i == len(parts)-1 {
			continue
		}
		rel := filepath.FromSlash(strings.Join(parts[i+1:], "/"))
		if !filepath.IsLocal(rel) {
			continue
		}
		f, err := Open(root, rel)
		if err != nil {
			continue
		}
		f.Close()
		if candidate != "" && candidate != rel {
			return "", fmt.Errorf("ambiguous project-relative gallery location")
		}
		candidate = rel
	}
	if candidate == "" {
		return "", os.ErrNotExist
	}
	return candidate, nil
}
