// Browser launch dispatch is checked headlessly; these tests never start a
// desktop or connect to a browser. UI transport behavior belongs to internal/ui.
package main

import (
	"reflect"
	"strings"
	"testing"
)

func TestViewerBrowserLaunchUsesNativeCommandsWithoutAShell(t *testing.T) {
	url := "http://127.0.0.1:12345/?token=synthetic"
	for _, test := range []struct {
		platform string
		args     []string
	}{
		{"windows", []string{"rundll32.exe", "url.dll,FileProtocolHandler", url}},
		{"darwin", []string{"open", url}},
		{"linux", []string{"xdg-open", url}},
	} {
		command := browserCommand(test.platform, url)
		if !reflect.DeepEqual(command.Args, test.args) {
			t.Fatalf("%s: %v", test.platform, command.Args)
		}
	}
}

func TestViewerHelpDescribesRetractionAdmissionAndDesktopSupport(t *testing.T) {
	text, ok := helpFor("ui")
	if !ok {
		t.Fatal("missing ui help")
	}
	for _, phrase := range []string{"captures and admits", "without deleting", "self-admission policy", "macOS", "Windows", "Linux", "--no-open"} {
		if !strings.Contains(text, phrase) {
			t.Errorf("ui help omits %q", phrase)
		}
	}
	if strings.Contains(text, "read-only") {
		t.Fatal("ui help conceals admitted gallery deletion")
	}
}
