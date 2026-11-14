package opencode

import "github.com/roeehrl/hopsesh/sdk/agent"

// The rewrite policy for a session's JSON lines (§3.8 of the design).

// protectKeys are values the path rewriter never changes: provider replay data
// (native, and its names before OpenCode 2.1, which the module upgrades on reading but a
// file may still carry), ids, signatures and model names. A media reference
// ("media:<sha256>") or a hash has no path in it to rewrite.
var protectKeys = []string{"native", "providerState", "providerResultState", "providerContext", "id", "callID", "call_id", "hash", "signature", "providerID", "model"}

// renameKeys give a keep-both copy its new session id, also where they are protected.
// Message and call ids stay: OpenCode's import gives a copy fresh ones when it needs them.
var renameKeys = []string{"id", "sessionID", "parentID"}

func policy() agent.RewritePolicy {
	return agent.RewritePolicy{Protect: append([]string(nil), protectKeys...)}
}

// Sanitize is the extra policy for a move to a machine signed in to another account:
// reasoning items, which a provider may bind to the account that produced them, are
// dropped from every assistant message. The rest keeps its order.
func (*Module) Sanitize() agent.RewritePolicy {
	return agent.RewritePolicy{DropElems: []agent.ElemMatch{{Array: "content", Field: "type", Values: []string{"reasoning"}}}}
}
