package ir

import (
	"context"
	"errors"
	"fmt"
)

// Limits is one operation's history and resource policy. Each budget is separate:
// memory a native decoder may hold, one native record, the portable archive on disk,
// whole native files read for fork/recovery/verification, and the receiving context.
// A zero field means the default; Normalize clamps every field to its hard ceiling.
// Limits travel with an operation (Options, context), never as mutable globals.
type Limits struct {
	ReadBytes       int64 `json:"readBytes,omitempty"`
	RecordBytes     int64 `json:"recordBytes,omitempty"`
	ArchiveBytes    int64 `json:"archiveBytes,omitempty"`
	NativeFileBytes int64 `json:"nativeFileBytes,omitempty"`
	// ContextBudget optionally lowers the receiving context (upper-estimate units, as
	// Capacity.Allowance). 0 is automatic. It can never raise the model's allowance.
	ContextBudget int `json:"contextBudget,omitempty"`
	// Older is how history older than the recent turns is carried: OlderExtract (default)
	// or OlderRecent. Omitted history always stays in the portable archive.
	Older string `json:"older,omitempty"`
}

// Older-context choices.
const (
	OlderExtract = "extract" // a labeled extract of older history plus recent turns
	OlderRecent  = "recent"  // recent complete turns only
)

// Defaults and hard ceilings. Defaults are the limits hopsesh shipped before they
// were configurable; ceilings bound what a setting may ask for.
const (
	DefaultReadBytes       int64 = MaxTranscriptBytes
	DefaultRecordBytes     int64 = 32 << 20
	DefaultArchiveBytes    int64 = MaxTranscriptBytes
	DefaultNativeFileBytes int64 = 1 << 30

	CeilingReadBytes       int64 = 4 << 30
	CeilingRecordBytes     int64 = 256 << 20
	CeilingArchiveBytes    int64 = 8 << 30
	CeilingNativeFileBytes int64 = 8 << 30
	// MinContextBudget keeps a user budget large enough for a briefing and a request.
	MinContextBudget = 4096
)

// DefaultLimits are the limits when the user chose none.
func DefaultLimits() Limits { return Limits{}.Normalize() }

// Normalize fills defaults and clamps to the hard ceilings.
func (l Limits) Normalize() Limits {
	fill := func(v, def, ceiling int64) int64 {
		if v <= 0 {
			return def
		}
		return min(v, ceiling)
	}
	l.ReadBytes = fill(l.ReadBytes, DefaultReadBytes, CeilingReadBytes)
	l.RecordBytes = fill(l.RecordBytes, DefaultRecordBytes, CeilingRecordBytes)
	l.ArchiveBytes = fill(l.ArchiveBytes, DefaultArchiveBytes, CeilingArchiveBytes)
	l.NativeFileBytes = fill(l.NativeFileBytes, DefaultNativeFileBytes, CeilingNativeFileBytes)
	switch {
	case l.ContextBudget < 0:
		l.ContextBudget = 0
	case l.ContextBudget > 0:
		l.ContextBudget = max(l.ContextBudget, MinContextBudget)
	}
	if l.Older != OlderRecent {
		l.Older = OlderExtract
	}
	return l
}

// Check reports a limit that is outside its range (for settings validation).
func (l Limits) Check() error {
	check := func(name string, v, ceiling int64) error {
		if v < 0 || v > ceiling {
			return fmt.Errorf("%s is %d bytes: use up to %d MiB", name, v, ceiling>>20)
		}
		return nil
	}
	if err := errors.Join(
		check("read memory", l.ReadBytes, CeilingReadBytes),
		check("record size", l.RecordBytes, CeilingRecordBytes),
		check("archive size", l.ArchiveBytes, CeilingArchiveBytes),
		check("native file size", l.NativeFileBytes, CeilingNativeFileBytes),
	); err != nil {
		return err
	}
	if l.ContextBudget != 0 && l.ContextBudget < MinContextBudget {
		return fmt.Errorf("context budget is %d: use 0 (automatic) or at least %d", l.ContextBudget, MinContextBudget)
	}
	switch l.Older {
	case "", OlderExtract, OlderRecent:
	default:
		return fmt.Errorf("older context is %q: use %q or %q", l.Older, OlderExtract, OlderRecent)
	}
	return nil
}

// Allowance applies the user's optional context budget to a model allowance.
func (l Limits) Allowance(model int) int {
	if l.ContextBudget > 0 {
		return min(model, l.ContextBudget)
	}
	return model
}

type limitsKey struct{}

// WithLimits scopes limits to one operation.
func WithLimits(ctx context.Context, l Limits) context.Context {
	return context.WithValue(ctx, limitsKey{}, l.Normalize())
}

// LimitsFrom returns the operation's limits, or the defaults.
func LimitsFrom(ctx context.Context) Limits {
	if ctx != nil {
		if l, ok := ctx.Value(limitsKey{}).(Limits); ok {
			return l
		}
	}
	return DefaultLimits()
}

// Limit stages, as LimitError reports them.
const (
	StageRead    = "read"    // decoding a native transcript
	StageRecord  = "record"  // one native record
	StageArchive = "archive" // building or merging the portable archive
	StageNative  = "native"  // reading a whole native file (fork, recovery, verification)
	StageContext = "context" // the receiving agent's context
)

// Settings that raise each stage's limit (config keys under [history]).
var limitSettings = map[string]string{
	StageRead:    "history.read_memory_mb",
	StageRecord:  "history.record_mb",
	StageArchive: "history.archive_mb",
	StageNative:  "history.native_file_mb",
	StageContext: "history.context_budget",
}

// ErrLimit matches every LimitError.
var ErrLimit = errors.New("history limit reached")

// LimitError is a reached limit. Canonical history is never silently truncated: the
// operation stops, sources and existing destinations stay unchanged, and the error
// names the stage and the setting that governs it.
type LimitError struct {
	Stage  string `json:"stage"`
	Limit  int64  `json:"limit"`
	Size   int64  `json:"size,omitempty"` // bytes seen when it stopped (a lower bound)
	Detail string `json:"detail,omitempty"`
}

// Setting is the configuration key that governs the stage.
func (e *LimitError) Setting() string { return limitSettings[e.Stage] }

func (e *LimitError) Error() string {
	what := map[string]string{
		StageRead:    "transcript exceeds the analysis memory limit",
		StageRecord:  "a native record exceeds the record size limit",
		StageArchive: "portable archive exceeds the archive size limit",
		StageNative:  "native file exceeds the native file size limit",
		StageContext: "incoming context exceeds the context budget",
	}[e.Stage]
	if what == "" {
		what = e.Stage + " limit reached"
	}
	unit := fmt.Sprintf("%d MiB", e.Limit>>20)
	if e.Stage == StageContext {
		unit = fmt.Sprint(e.Limit)
	}
	msg := fmt.Sprintf("%s (%s", what, unit)
	if s := e.Setting(); s != "" {
		msg += "; setting " + s
	}
	msg += ")"
	if e.Detail != "" {
		msg += ": " + e.Detail
	}
	return msg
}

func (e *LimitError) Is(target error) bool { return target == ErrLimit }

// AsLimit returns the LimitError in err's chain, if any.
func AsLimit(err error) (*LimitError, bool) {
	var le *LimitError
	ok := errors.As(err, &le)
	return le, ok
}
