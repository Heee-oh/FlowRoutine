//go:build !windows

package system

import "os"

func OpenFileDescriptorCount() (int, error) {
	count, err := countDirNames("/dev/fd")
	if err == nil {
		return count, nil
	}
	count, err = countDirNames("/proc/self/fd")
	if err != nil {
		return -1, err
	}
	return count, nil
}

func countDirNames(path string) (int, error) {
	dir, err := os.Open(path)
	if err != nil {
		return -1, err
	}
	defer dir.Close()

	names, err := dir.Readdirnames(-1)
	if err != nil {
		return -1, err
	}
	return len(names), nil
}
