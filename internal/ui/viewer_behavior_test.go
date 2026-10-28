// Headless behavior executes the embedded UI modules with synthetic responses.
// These checks do not claim layout, browser paint or native OS execution.
package ui

import (
	"os/exec"
	"testing"
)

func TestViewerStartupAndGalleryInteractions(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("headless JavaScript runtime is unavailable")
	}
	output, err := exec.Command(node, "--experimental-vm-modules", "testdata/viewer.mjs").CombinedOutput()
	if err != nil {
		t.Fatalf("headless viewer: %v\n%s", err, output)
	}
	t.Log(string(output))
}
