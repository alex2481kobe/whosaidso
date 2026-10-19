//go:build windows

package write

// Windows has no signal a process can send itself, so the signal half of the
// observer test does not run here; cancellation still does.

const runTestCanSignalSelf = false

func runTestSignalSelf() {}
