// Package repos matches a session's working directory to a git repository.
//
// Identity is the normalized remote (host/owner/repo, ssh or https, with or without
// .git, read without insteadOf rewriting). One batched probe per machine reports each
// directory's branch, worktrees, upstream, unpushed commits and uncommitted files. It
// finds local checkouts, clones into the configured repos folder when asked, and adds
// worktrees or switches branches for the moved session.
package repos
