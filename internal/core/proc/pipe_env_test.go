package proc

import (
	"slices"
	"strings"
	"testing"
)

func TestSSHDescriptorEnvironmentPreservesSecurityAndAuthentication(t *testing.T) {
	want := []string{
		"PATH=tools", "SSH_AUTH_SOCK=agent", "SSH_ASKPASS=helper",
		"SSH_CONNECTION=fixture", "c28fc6f98a2c44abbbd89d6a3037d0d9_POSIX_CHROOT=keep",
		"OPENSSH_STDIO_MODE_OTHER=keep", "VALUE=OPENSSH_STDIO_MODE=nonsock",
		"=C:=C:\\fixture", "SystemRoot=C:\\Windows",
	}
	in := append(slices.Clone(want), windowsSSHDescriptorState+"=AAAAAAICAgA=",
		strings.ToLower(windowsSSHDescriptorState)+"=duplicate",
		"OPENSSH_STDIO_MODE=nonsock", "openssh_stdio_mode=sock")
	before := slices.Clone(in)
	got := freshDescriptorEnv(in)
	if !slices.Equal(got, want) {
		t.Fatalf("unexpected child environment: %q", got)
	}
	if !slices.Equal(in, before) {
		t.Fatal("changed the caller's environment")
	}
}
