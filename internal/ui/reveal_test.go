// Native command tests check platform dispatch and argument boundaries without
// launching a desktop. Transport tests separately check the admitted file path.
package ui

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestRevealUsesEachPlatformsFileManagerWithoutAShell(t *testing.T) {
	path := filepath.Join(t.TempDir(), "folder with spaces", "image & quote's.png")
	for _, test := range []struct {
		platform string
		args     []string
	}{
		{"darwin", []string{"/usr/bin/open", "-R", path}},
		{"windows", []string{"explorer.exe", "/select,", path}},
		{"linux", []string{"xdg-open", filepath.Dir(path)}},
	} {
		command, err := revealCommand(test.platform, path)
		if err != nil || !reflect.DeepEqual(command.Args, test.args) {
			t.Fatalf("%s: %v %v", test.platform, command, err)
		}
	}
	if command, err := revealCommand("unsupported", path); err == nil || command != nil {
		t.Fatal("unsupported platform silently succeeded")
	}
}
