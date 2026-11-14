package move

import (
	"context"
	"errors"
	"fmt"

	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/journal"
	"github.com/roeehrl/hopsesh/internal/core/launch"
	"github.com/roeehrl/hopsesh/internal/core/lineage"
	"github.com/roeehrl/hopsesh/sdk/agent"
	"github.com/roeehrl/hopsesh/sdk/ir"
)

// Sessions agents keep in a database (OpenCode): the move's sides reach them through their
// modules (agent.SessionStore), for the journal's stamps and undo.

// SideSessions is a journal.Sessions over the sides of an operation: a session is reached
// through the side on its machine whose module is its agent.
type SideSessions []Side

// sessionsOf reaches the sessions of a move's sides, and of the source agent's own copy a
// continuation keeps here.
func sessionsOf(p *Plan, in Input) SideSessions {
	sides := SideSessions{in.Source, in.Target}
	if p != nil && p.native != nil && p.nativeIn.Target.Module != nil {
		sides = append(sides, p.nativeIn.Target)
	}
	return sides
}

// reachOf is how a move's journal reaches its machines and their agents' sessions.
func reachOf(ctx context.Context, p *Plan, in Input) journal.Reach {
	return journal.Reach{FS: machinesOf(ctx, in), Sessions: sessionsOf(p, in)}
}

// renamedSessions is SideSessions for a journal written when the machines had other names
// (recovering an interrupted operation after a rename): old names lead to the sides.
type renamedSessions struct {
	SideSessions
	names map[string]string
}

func (r renamedSessions) name(machine string) string {
	if n, ok := r.names[machine]; ok {
		return n
	}
	return machine
}

func (r renamedSessions) Stamp(ctx context.Context, machine string, key agent.SessionKey) (agent.SessionStamp, error) {
	return r.SideSessions.Stamp(ctx, r.name(machine), key)
}

func (r renamedSessions) Delete(ctx context.Context, machine string, key agent.SessionKey, expect agent.SessionStamp) error {
	return r.SideSessions.Delete(ctx, r.name(machine), key, expect)
}

func (r renamedSessions) Restore(ctx context.Context, machine string, key agent.SessionKey, backup string) (agent.SessionStamp, error) {
	return r.SideSessions.Restore(ctx, r.name(machine), key, backup)
}

func (s SideSessions) side(ctx context.Context, machine string, key agent.SessionKey) (Side, agent.Host, error) {
	for _, sd := range s {
		if sd.Machine == nil || sd.Module == nil || sd.Machine.Name != machine || sd.Module.Spec().ID != key.Agent {
			continue
		}
		if key.Profile != "" && sd.Install.ProfileID() != "" && key.Profile != sd.Install.ProfileID() {
			continue
		}
		h, err := sd.Machine.For(ctx, sd.Module.Spec(), sd.Install, nil)
		return sd, h, err
	}
	return Side{}, nil, fmt.Errorf("%w: the %s session %s on %s is not part of this operation", journal.ErrOutOfReach, key.Agent, key.Session, machine)
}

func (s SideSessions) Stamp(ctx context.Context, machine string, key agent.SessionKey) (agent.SessionStamp, error) {
	sd, h, err := s.side(ctx, machine, key)
	if err != nil {
		return "", err
	}
	st, ok, err := StampNow(ctx, sd.Module, h, sd.Install, key)
	if err == nil && !ok {
		err = fmt.Errorf("%w: %s does not stamp its sessions", agent.ErrUnsupported, sd.Module.Spec().Name)
	}
	return st, err
}

func (s SideSessions) Delete(ctx context.Context, machine string, key agent.SessionKey, expect agent.SessionStamp) error {
	sd, h, err := s.side(ctx, machine, key)
	if err != nil {
		return err
	}
	store, ok := sd.Module.(agent.SessionStore)
	if !ok {
		return fmt.Errorf("%w: %s cannot delete its sessions", agent.ErrUnsupported, sd.Module.Spec().Name)
	}
	return store.Delete(ctx, h, sd.Install, key.Session, expect)
}

func (s SideSessions) Restore(ctx context.Context, machine string, key agent.SessionKey, backup string) (agent.SessionStamp, error) {
	sd, h, err := s.side(ctx, machine, key)
	if err != nil {
		return "", err
	}
	store, ok := sd.Module.(agent.SessionStore)
	if !ok {
		return "", fmt.Errorf("%w: %s cannot restore its sessions", agent.ErrUnsupported, sd.Module.Spec().Name)
	}
	return store.Restore(ctx, h, sd.Install, key.Session, backup)
}

// StampNow is a session's stamp as its module gives it now: SessionStamper's, else, for a
// virtual module, that of a fresh listing's summary (never a cached one). ok is false for
// a module that has neither. A session gone is agent.ErrNotFound.
func StampNow(ctx context.Context, m agent.Module, h agent.Host, in agent.Install, key agent.SessionKey) (agent.SessionStamp, bool, error) {
	if _, ok := m.(agent.SessionStamper); ok || !agent.IsVirtual(m) {
		return agent.StampOf(ctx, m, h, in, agent.Summary{Key: key})
	}
	l, err := m.List(ctx, h, in)
	if err != nil {
		return "", true, err
	}
	for _, s := range l.Sessions {
		if s.Key.Session == key.Session {
			return agent.StampOf(ctx, m, h, in, s)
		}
	}
	return "", true, fmt.Errorf("%w: %s", agent.ErrNotFound, key.Session)
}

// sessionPresent reports whether a virtual module's agent has a session: its anchor file
// may be missing while the session is there. An agent with no database yet has none.
func sessionPresent(ctx context.Context, side Side, key agent.SessionKey) (bool, error) {
	h, err := side.Machine.For(ctx, side.Module.Spec(), side.Install, nil)
	if err != nil {
		return false, err
	}
	_, ok, err := StampNow(ctx, side.Module, h, side.Install, key)
	switch {
	case errors.Is(err, agent.ErrNotFound), errors.Is(err, agent.ErrNotInstalled):
		return false, nil
	case err != nil:
		return false, err
	}
	return ok, nil
}

// vendorInstall has a module whose agent installs its own sessions (VendorInstaller) import
// the copy a move placed for pl.Key, after journaling the session it makes, so undo
// deletes it, and records its stamp once it is made. It runs before lineage is recorded,
// and its failure is the operation's. It is idempotent: recovering an interrupted
// operation runs it again. A write needs none: the writer imports (agent.Writer).
//
// An import that finds another session at the id (ErrDiverged) made nothing: unless this
// operation made that session before (its stamp is recorded), undo leaves it alone.
func vendorInstall(ctx context.Context, side Side, h agent.Host, j *journal.Journal, pl agent.Placement) error {
	if !agent.InstallsViaVendor(side.Module) {
		return nil
	}
	if err := j.RecordCreatedSession(side.Machine.Name, pl.Key); err != nil {
		return err
	}
	if err := side.Module.(agent.PostInstaller).AfterInstall(ctx, h, side.Install, pl.Key, pl); err != nil {
		if errors.Is(err, agent.ErrDiverged) {
			if e := j.ForgetSession(side.Machine.Name, pl.Key, true); e != nil {
				return e
			}
		}
		err = fmt.Errorf("%s did not import the session: %w", side.Module.Spec().Name, err)
		if j.SetAsideSession(side.Machine.Name, pl.Key) {
			err = fmt.Errorf("%w; the session it replaces was set aside and is backed up: undo puts it back, and running this again retries the import", err)
		}
		return err
	}
	stampMade(ctx, j, side, h, pl.Key)
	return nil
}

// stampMade records the stamp of a session the agent just made for the operation, so a
// later Seal (a recovery's) does not take work done since for the operation's. A stamp it
// cannot read now is left to Seal.
func stampMade(ctx context.Context, j *journal.Journal, side Side, h agent.Host, key agent.SessionKey) {
	if st, ok, err := StampNow(ctx, side.Module, h, side.Install, key); err == nil && ok {
		_ = j.StampCreatedSession(side.Machine.Name, key, st)
	}
}

// recordVendorWrite journals the session a write is about to make through the agent's own
// program (a SessionStore module writing a new session), so undo deletes it while unused.
func recordVendorWrite(j *journal.Journal, m agent.Module, machine string, key agent.SessionKey, req ir.WriteRequest) error {
	if !vendorWrite(m, req) {
		return nil
	}
	key.Session = agent.SessionID(req.SessionID)
	return j.RecordCreatedSession(machine, key)
}

// vendorWrite reports whether a write makes a new session through the agent's own program.
func vendorWrite(m agent.Module, req ir.WriteRequest) bool {
	_, ok := m.(agent.SessionStore)
	return ok && req.Mode == ir.WriteNew && req.SessionID != ""
}

// recordVendorWritten journals the session a write made when the writer chose another id
// than the one asked for, and records the stamp the write left it at.
func recordVendorWritten(ctx context.Context, j *journal.Journal, side Side, h agent.Host, key agent.SessionKey, req ir.WriteRequest, w ir.WriteResult) error {
	if w.SessionID != "" && w.SessionID != req.SessionID {
		req.SessionID = w.SessionID
		if err := recordVendorWrite(j, side.Module, side.Machine.Name, key, req); err != nil {
			return err
		}
	}
	if vendorWrite(side.Module, req) {
		key.Session = agent.SessionID(req.SessionID)
		stampMade(ctx, j, side, h, key)
	}
	return nil
}

// writeNativeReceipt writes lineage beside an installed copy, guarded by its file; for a
// session its agent keeps in a database (whose file is an anchor that does not change when
// it is used), by its stamp now.
func writeNativeReceipt(ctx context.Context, j *journal.Journal, fsys host.FS, side Side, machine, native string, key agent.SessionKey, body []byte) error {
	if agent.IsVirtual(side.Module) {
		h, err := side.Machine.For(ctx, side.Module.Spec(), side.Install, nil)
		if err != nil {
			return err
		}
		st, ok, err := StampNow(ctx, side.Module, h, side.Install, key)
		if err != nil {
			return fmt.Errorf("the state of the %s session %s: %w", side.Module.Spec().Name, key.Session, err)
		}
		if ok {
			return j.WriteSessionReceipt(fsys, machine, lineage.PathFor(native), body, key, st)
		}
	}
	return j.WriteReceipt(fsys, machine, lineage.PathFor(native), body, true)
}

// setAsideSession backs up a session an agent keeps in a database and deletes it through
// its program, journaled first, so undo imports the backup again. A session already gone
// needs nothing.
func setAsideSession(ctx context.Context, side Side, h agent.Host, j *journal.Journal, key agent.SessionKey) error {
	store, ok := side.Module.(agent.SessionStore)
	if !ok {
		return nil
	}
	backup, stamp, err := store.Backup(ctx, h, side.Install, key.Session)
	if errors.Is(err, agent.ErrNotFound) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("backing up the %s session %s: %w", side.Module.Spec().Name, key.Session, err)
	}
	if err := j.DeletedSession(side.Machine.Name, key, backup, stamp); err != nil {
		return err
	}
	if err := store.Delete(ctx, h, side.Install, key.Session, stamp); err != nil {
		if errors.Is(err, agent.ErrDiverged) {
			// Used since its backup: the agent kept it, with that work. Undo leaves it.
			if e := j.ForgetSession(side.Machine.Name, key, false); e != nil {
				return e
			}
		}
		return fmt.Errorf("setting aside the %s session %s: %w", side.Module.Spec().Name, key.Session, err)
	}
	return nil
}

// resumeNow is the command that resumes a session now on side's machine: the module's
// HostResumer's, which checks the machine as it is now, else planned (made before the
// session was there). A resumer that refuses leaves planned, with its reason as a warning,
// and ok false: that command is for the user to read, not for hopsesh to run (OpenCode's
// would restart a background service of another version).
func resumeNow(ctx context.Context, side Side, h agent.Host, pl agent.Placement, o agent.ResumeOptions, planned agent.Command, res *Result) (c agent.Command, ok bool) {
	hr, is := side.Module.(agent.HostResumer)
	if !is || o.App {
		return planned, true
	}
	c, err := hr.ResumeOn(ctx, h, side.Install, pl.Key, pl, o)
	if err != nil {
		res.Warnings = append(res.Warnings, "before you resume it: "+err.Error())
		return planned, false
	}
	return c, true
}

// finishResume sets the move's resume command once the session is there (resumeNow). A
// command the target refused is shown (Result.Command) but not runnable: Result.Run and
// the plan's Resume are empty, so no interface opens it.
func finishResume(ctx context.Context, p *Plan, tgt Side, h agent.Host, res *Result) {
	c, ok := resumeNow(ctx, tgt, h, p.Placement, p.resumeOpts, p.Resume, res)
	res.Command, res.Run, p.Resume = launch.Shell(c, res.PromptFile, launch.DefaultShell()), c, c
	if !ok {
		res.Run, p.Resume = agent.Command{}, agent.Command{}
	}
}
