// Desktop file-manager commands belong here. Gallery membership and safe path
// resolution remain in gallery.go and internal/gallery; no shell is involved.
package ui

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"
)

// RevealFile selects a file in Finder or Explorer, or opens its folder on Linux.
func RevealFile(path string) error {
	command, err := revealCommand(runtime.GOOS, path)
	if err != nil {
		return err
	}
	return command.Run()
}

func revealCommand(platform, path string) (*exec.Cmd, error) {
	switch platform {
	case "darwin":
		return exec.Command("/usr/bin/open", "-R", path), nil
	case "windows":
		return exec.Command("explorer.exe", "/select,", path), nil
	case "linux":
		return exec.Command("xdg-open", filepath.Dir(path)), nil
	default:
		return nil, fmt.Errorf("file manager is unavailable on %s", platform)
	}
}
