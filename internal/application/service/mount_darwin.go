package service

import (
	"errors"
	"syscall"
)

func requireMount(path string) error {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return err
	}
	b := make([]byte, 0, len(stat.Mntonname))
	for _, c := range stat.Mntonname {
		if c == 0 {
			break
		}
		b = append(b, byte(c))
	}
	if string(b) != path {
		return errors.New("configured volume is not mounted")
	}
	return nil
}
