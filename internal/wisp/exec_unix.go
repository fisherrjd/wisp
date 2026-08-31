//go:build unix

package wisp

import "syscall"

// syscallExec replaces the current process image. Isolated in its own file so the unix-only
// syscall.Exec dependency is visible, and so a future Windows port has one obvious seam.
//
// The constraint has to be written out: `_unix` is not one of the filename suffixes Go turns into
// a build constraint on its own (only GOOS and GOARCH names are), so without this line the file
// was compiled everywhere and the seam it describes did not exist.
func syscallExec(path string, args, env []string) error {
	return syscall.Exec(path, args, env)
}
