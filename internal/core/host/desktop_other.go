//go:build !windows

package host

import (
	"context"
	"os/exec"
	"runtime"
	"strings"
	"time"
)

func registeredDesktopProtocol(ctx context.Context, scheme string) bool {
	if runtime.GOOS != "linux" || strings.ContainsAny(scheme, `\/:`) {
		return false
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	b, err := exec.CommandContext(ctx, "xdg-mime", "query", "default", "x-scheme-handler/"+scheme).Output()
	return err == nil && strings.HasSuffix(strings.TrimSpace(string(b)), ".desktop")
}
