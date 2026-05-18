//go:build !windows

package system

import "syscall"

func MaximizeFileDescriptorLimit(min uint64) (FileDescriptorLimit, error) {
	var lim syscall.Rlimit
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &lim); err != nil {
		return FileDescriptorLimit{}, err
	}

	status := FileDescriptorLimit{
		Initial: lim.Cur,
		Current: lim.Cur,
		Maximum: lim.Max,
	}

	target := lim.Max
	if target < min {
		target = min
	}
	if target > lim.Max {
		target = lim.Max
	}
	if lim.Cur >= target {
		return status, nil
	}

	next := lim
	next.Cur = target
	if err := syscall.Setrlimit(syscall.RLIMIT_NOFILE, &next); err != nil {
		return status, err
	}
	if err := syscall.Getrlimit(syscall.RLIMIT_NOFILE, &lim); err != nil {
		return status, err
	}

	status.Current = lim.Cur
	status.Maximum = lim.Max
	status.Raised = status.Current > status.Initial
	return status, nil
}
