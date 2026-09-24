package acceptance_test

import "os/exec"

// bindProjectHome binds the project at cmd.Dir as its own home before a CLI
// call (reads and admission refuse in an unbound project). A failed bind is not hidden: the call that follows then
// refuses with home-unbound, so no test can pass on a missing binding.
func bindProjectHome(cmd *exec.Cmd) {
	b := exec.Command(cmd.Path, "home", ".")
	b.Dir, b.Env = cmd.Dir, cmd.Env
	_ = b.Run()
}
