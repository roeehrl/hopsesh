// Package scan finds likely secrets in transcripts before they move.
//
// It uses a small set of strict rules (API keys, tokens, private keys) written for
// hopsesh, and can produce a redacted copy. Results are shown to the user in the plan;
// nothing is ever sent anywhere.
package scan
