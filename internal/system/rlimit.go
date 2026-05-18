package system

type FileDescriptorLimit struct {
	Initial uint64
	Current uint64
	Maximum uint64
	Raised  bool
}
