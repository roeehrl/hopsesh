package termapp

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/roeehrl/hopsesh/internal/core/host"
	"github.com/roeehrl/hopsesh/internal/core/proc"
)

// Procs is what finding a session's tab needs to know about this machine's processes.
type Procs interface {
	Alive(pid int) bool
	// TTY is the process's controlling terminal ("/dev/ttys003"; "" for none).
	TTY(ctx context.Context, pid int) string
}

// SystemProcs reads this machine's process table (ps for terminals; none on Windows).
func SystemProcs() Procs { return systemProcs{} }

type systemProcs struct{}

func (systemProcs) Alive(pid int) bool {
	if pid <= 0 {
		return false
	}
	m, err := (&host.Machine{Local: true}).Procs().Alive(context.Background(), []int{pid})
	return err == nil && m[pid]
}

func (systemProcs) TTY(ctx context.Context, pid int) string {
	if pid <= 0 || runtime.GOOS == "windows" {
		return ""
	}
	ps := "ps"
	if _, err := os.Stat("/bin/ps"); err == nil {
		ps = "/bin/ps" // not whatever a PATH has
	}
	out, err := proc.CommandContext(ctx, ps, "-o", "tty=", "-p", strconv.Itoa(pid)).Output()
	var ee *exec.ExitError
	if err != nil && !errors.As(err, &ee) {
		return ""
	}
	return DevTTY(string(out))
}

var psTTY = regexp.MustCompile(`^(ttys[0-9]{1,4}|pts/[0-9]{1,5})$`)

// DevTTY is the device of a terminal as ps names it ("ttys003" → "/dev/ttys003", "pts/3"
// → "/dev/pts/3"); "" for none ("??", "?") or anything else.
func DevTTY(ps string) string {
	ps = strings.TrimSpace(ps)
	if !psTTY.MatchString(ps) {
		return ""
	}
	return "/dev/" + ps
}
