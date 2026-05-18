package system

import (
	"runtime"
	"testing"
)

func TestOpenFileDescriptorCount(t *testing.T) {
	count, err := OpenFileDescriptorCount()
	if runtime.GOOS == "windows" {
		if err == nil {
			t.Fatal("expected unsupported error on windows")
		}
		return
	}
	if err != nil {
		t.Skipf("fd count unsupported in this environment: %v", err)
	}
	if count <= 0 {
		t.Fatalf("got fd count %d, want > 0", count)
	}
}
