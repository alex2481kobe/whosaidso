package acceptance_test

// The file name a built test binary needs on this platform lives here.

import "runtime"

// exeName is name as an executable here: Windows runs only files with an
// executable extension, so a binary built without one cannot be started.
func exeName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}
