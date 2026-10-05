//go:build !windows

package justlog3

import "os"

func openAppend(name string) (*os.File, error) {
	return os.OpenFile(name, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
}
