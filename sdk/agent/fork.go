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

// NativeForkOrphans is implemented by a module whose native forks can hold their whole
// inherited history. A fork whose declared parent is absent from the listing starts its
// own family only when the module reports the parent definitively gone from the install
// (not archived, unreadable or skipped) and the fork does not read the parent's history.
type NativeForkOrphans interface {
	NativeParentGone(context.Context, Host, Install, Summary) (bool, error)
}
