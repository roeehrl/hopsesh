//go:build windows

package host

import (
	"context"
	"golang.org/x/sys/windows/registry"
	"strings"
)

// URL protocols include MSIX/Store installations, whose executable paths are versioned.
func registeredDesktopProtocol(_ context.Context, scheme string) bool {
	if strings.ContainsAny(scheme, `\/:`) {
		return false
	}
	key, err := registry.OpenKey(registry.CLASSES_ROOT, scheme+`\shell\open\command`, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer key.Close()
	value, _, err := key.GetStringValue("")
	return err == nil && strings.TrimSpace(value) != ""
}
