//go:build darwin

package readerfacade

import "golang.org/x/sys/unix"

// renameNoReplaceDir publishes `to` only when absent, resolved inside the
// SELECTED directory identified by dirfd. Relative names plus the anchored
// directory FD mean a replaced/removed parent path can never retarget the
// publication the way an AT_FDCWD full-path rename could.
func renameNoReplaceDir(dirfd int, from, to string) error {
	return unix.RenameatxNp(dirfd, from, dirfd, to, unix.RENAME_EXCL)
}
