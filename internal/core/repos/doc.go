// Package repos matches a session's working directory to a git repository.
//
// Identity is the normalized remote (host/owner/repo, ssh or https, with or without
// .git), confirmed by the root commit when available. It finds local checkouts,
// clones into the configured repos folder when asked, and reports unpushed or
// uncommitted work on the source that a clone would not bring along.
package repos
