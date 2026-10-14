package authingress

import (
	_ "embed"
	"errors"
	"net/http"
	"os"
	"strings"
)

//go:embed ui.html
var connectPage []byte

// LocalControl initializes or reads a credential inside the caller's private
// fixture directory. The credential never belongs in arguments, URLs or logs.
func LocalControl(path string) (string, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err == nil {
		value := random()
		_, writeErr := f.WriteString(value + "\n")
		syncErr := f.Sync()
		closeErr := f.Close()
		if err := errors.Join(writeErr, syncErr, closeErr); err != nil {
			return "", err
		}
		return value, nil
	}
	if !errors.Is(err, os.ErrExist) {
		return "", err
	}
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		return "", errors.New("local control must be a regular owner-only file")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	value := strings.TrimSpace(string(data))
	if len(value) != 64 {
		return "", errors.New("invalid local control credential")
	}
	return value, nil
}

// Fixture constructs the disposable browser proof. It starts no listener.
func Fixture(database, control string, hosts, origins map[string]bool) (http.Handler, func() error, error) {
	bootstrap, err := LocalControl(control)
	if err != nil {
		return nil, nil, err
	}
	s, err := open(database)
	if err != nil {
		return nil, nil, err
	}
	return browserHandler(s, bootstrap, hosts, origins, connectPage), s.db.Close, nil
}
