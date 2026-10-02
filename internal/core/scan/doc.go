// Package scan finds likely secrets in transcripts before they move.
//
// It uses gitleaks-compatible rules (MIT) and can produce a redacted copy. Results are
// shown to the user in the plan; nothing is ever sent anywhere.
package scan
