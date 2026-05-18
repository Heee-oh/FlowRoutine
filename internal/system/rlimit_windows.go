//go:build windows

package system

import "errors"

func MaximizeFileDescriptorLimit(min uint64) (FileDescriptorLimit, error) {
	return FileDescriptorLimit{}, errors.New("file descriptor limit tuning is not supported on windows")
}
