package agent

import (
	"context"
)

type NativeInheritance struct {
	ParentAnchor string
	ChildAnchor  string
	Hash         string
}

// NativeForkVerifier requires vendor-declared parent identity plus exact native-prefix
// evidence. Similar conversation text alone never establishes a fork relationship.
type NativeForkVerifier interface {
	VerifyNativeFork(context.Context, Host, Install, Summary, Summary) ([]NativeInheritance, error)
}
