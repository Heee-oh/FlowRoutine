//go:build windows

package system

import "errors"

func OpenFileDescriptorCount() (int, error) {
	return -1, errors.New("file descriptor count is not supported on windows")
}
