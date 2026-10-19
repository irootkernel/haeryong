//go:build linux

package readerfacade

import "golang.org/x/sys/unix"

// renameNoReplaceDir publishes `to` only when absent, resolved inside the
// SELECTED directory identified by dirfd (see the Darwin variant).
func renameNoReplaceDir(dirfd int, from, to string) error {
	return unix.Renameat2(dirfd, from, dirfd, to, unix.RENAME_NOREPLACE)
}
