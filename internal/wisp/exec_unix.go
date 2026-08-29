package wisp

import "syscall"

// syscallExec replaces the current process image. Isolated in its own file so the unix-only
// syscall.Exec dependency is visible, and so a future Windows port has one obvious seam.
func syscallExec(path string, args, env []string) error {
	return syscall.Exec(path, args, env)
}
