package proc

import (
	"runtime"
	"strings"
)

const windowsSSHDescriptorState = "c28fc6f98a2c44abbbd89d6a3037d0d9_POSIX_FD_STATE"

// PipeEnvironment preserves the supplied environment except Windows OpenSSH's
// inherited I/O metadata. Use only for children whose standard handles are
// replaced with Go pipes or the null device, including Git's SSH subprocesses.
// Authentication, routing and chroot settings are deliberately preserved.
func PipeEnvironment(in []string) []string {
	if runtime.GOOS != "windows" {
		return in
	}
	return freshDescriptorEnv(in)
}

func freshDescriptorEnv(in []string) []string {
	out := make([]string, 0, len(in))
	for _, entry := range in {
		name, _, _ := strings.Cut(entry, "=")
		if strings.EqualFold(name, windowsSSHDescriptorState) || strings.EqualFold(name, "OPENSSH_STDIO_MODE") {
			continue
		}
		out = append(out, entry)
	}
	return out
}
