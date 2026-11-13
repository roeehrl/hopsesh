package agent

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"
)

// ListingHooks are supplied by the core for browsing only. Bundle, import and
// transfer validation never use this cache. Found may be called concurrently.
type ListingHooks struct {
	Load  func(path, signature string) (*Summary, bool)
	Save  func(path, signature string, summary *Summary)
	Found func(Summary)
}

type listingContextKey struct{}

func WithListingHooks(ctx context.Context, hooks ListingHooks) context.Context {
	return context.WithValue(ctx, listingContextKey{}, hooks)
}

// ListingSummary reuses a parsed summary only while all its dependencies match.
// A missing dependency is part of the signature; other stat failures disable
// caching. Modules version their parsers in salt.
func ListingSummary(ctx context.Context, h Host, file string, info fs.FileInfo, salt string, dependencies []string, parse func() (*Summary, error)) (*Summary, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	hooks, _ := ctx.Value(listingContextKey{}).(ListingHooks)
	signatureFor := func() (string, bool) {
		var signature strings.Builder
		fmt.Fprintf(&signature, "%s:%d:%d:%v", salt, info.Size(), info.ModTime().UnixNano(), info.Mode())
		cacheable := true
		for _, p := range dependencies {
			st, err := h.FS().Stat(p)
			switch {
			case err == nil:
				fmt.Fprintf(&signature, "|%s:%d:%d:%v", p, st.Size(), st.ModTime().UnixNano(), st.Mode())
			case errors.Is(err, fs.ErrNotExist):
				fmt.Fprintf(&signature, "|%s:missing", p)
			default:
				cacheable = false
			}
		}
		return signature.String(), cacheable
	}
	sig, cacheable := signatureFor()
	var sum *Summary
	var hit bool
	if cacheable && hooks.Load != nil {
		sum, hit = hooks.Load(file, sig)
	}
	if !hit {
		var err error
		sum, err = parse()
		if err != nil {
			return nil, err
		}
		if current, err := h.FS().Stat(file); err != nil || current.Size() != info.Size() || !current.ModTime().Equal(info.ModTime()) || current.Mode() != info.Mode() {
			cacheable = false
		}
		if after, valid := signatureFor(); !valid || after != sig {
			cacheable = false
		}
		if ctx.Err() == nil && cacheable && hooks.Save != nil {
			hooks.Save(file, sig, sum)
		}
	}
	if sum != nil && hooks.Found != nil && ctx.Err() == nil {
		hooks.Found(*sum)
	}
	return sum, ctx.Err()
}

// ListingSummaryKey is ListingSummary for a session that is not one file (a row of a
// database): key names it in the cache ("<db>#<session id>") and signature is what the
// module read that changes with it (OpenCode: id|time_updated|settled count|part count),
// read before parse. Modules version their parsers in salt.
func ListingSummaryKey(ctx context.Context, _ Host, key, signature, salt string, parse func() (*Summary, error)) (*Summary, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	hooks, _ := ctx.Value(listingContextKey{}).(ListingHooks)
	sig := salt + ":" + signature
	var sum *Summary
	var hit bool
	if signature != "" && hooks.Load != nil {
		sum, hit = hooks.Load(key, sig)
	}
	if !hit {
		var err error
		if sum, err = parse(); err != nil {
			return nil, err
		}
		// A change after the signature was read leaves the entry under a signature that
		// will not be asked for again.
		if signature != "" && ctx.Err() == nil && hooks.Save != nil {
			hooks.Save(key, sig, sum)
		}
	}
	if sum != nil && hooks.Found != nil && ctx.Err() == nil {
		hooks.Found(*sum)
	}
	return sum, ctx.Err()
}

// SessionWatchProvider narrows filesystem notifications to session and metadata
// paths, excluding vendor diagnostics that a listing probe itself may write.
// Paths are absolute and use the host's path rules; missing paths are supported.
type SessionWatchProvider interface {
	SessionWatchPaths(Install, Path) []string
}
