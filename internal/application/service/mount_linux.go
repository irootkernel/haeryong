package service

import (
	"bufio"
	"errors"
	"os"
	"strings"
)

func requireMount(path string) error {
	f, err := os.Open("/proc/self/mountinfo")
	if err != nil {
		return err
	}
	defer f.Close()
	scan := bufio.NewScanner(f)
	unescape := strings.NewReplacer(`\040`, " ", `\011`, "\t", `\012`, "\n", `\134`, `\`)
	for scan.Scan() {
		fields := strings.Fields(scan.Text())
		if len(fields) > 4 && unescape.Replace(fields[4]) == path {
			return nil
		}
	}
	if err := scan.Err(); err != nil {
		return err
	}
	return errors.New("configured volume is not mounted")
}
