//go:build !windows

package host

import (
	"bufio"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"

	"github.com/roeehrl/hopsesh/sdk/agent"
)

func processExists(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = p.Signal(syscall.Signal(0))
	return err == nil || errors.Is(err, syscall.EPERM)
}

// terminate sends SIGTERM, which lets an agent finish writing its session and exit.
func terminate(pid int) error { return syscall.Kill(pid, syscall.SIGTERM) }

// probeLock tries to take a shared lock without blocking and releases it at once: a
// writer holding an exclusive lock makes it fail. The file is opened read-only and never
// created.
func probeLock(p string) agent.LockState {
	f, err := os.Open(p)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return agent.LockFree
		}
		return agent.LockUnknown
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_SH|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return agent.LockHeld
		}
		return agent.LockUnknown
	}
	_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
	return agent.LockFree
}

// lockHolders finds the processes holding a lock on p: from /proc/locks on Linux (by the
// file's inode), from lsof elsewhere (the processes that have it open).
func lockHolders(ctx context.Context, p string) ([]int, error) {
	if runtime.GOOS == "linux" {
		fi, err := os.Stat(p)
		if err != nil {
			return nil, err
		}
		st, ok := fi.Sys().(*syscall.Stat_t)
		if !ok {
			return nil, errors.New("no inode for " + p)
		}
		f, err := os.Open("/proc/locks")
		if err != nil {
			return nil, err
		}
		defer f.Close()
		return parseProcLocks(bufio.NewScanner(f), st.Ino), nil
	}
	out, err := exec.CommandContext(ctx, "lsof", "-t", "--", p).Output()
	var ee *exec.ExitError
	if err != nil && !(errors.As(err, &ee) && ee.ExitCode() == 1) { // 1: nobody has it open
		return nil, err
	}
	return pidList(string(out)), nil
}

// parseProcLocks reads /proc/locks lines ("1: FLOCK ADVISORY WRITE 4242 08:02:131 0 EOF")
// for locks on the inode; waiters ("1: -> FLOCK …") are not holders.
func parseProcLocks(sc *bufio.Scanner, ino uint64) []int {
	var pids []int
	suffix := ":" + strconv.FormatUint(ino, 10)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 6 || f[1] == "->" || !strings.HasSuffix(f[5], suffix) {
			continue
		}
		if pid, err := strconv.Atoi(f[4]); err == nil {
			pids = append(pids, pid)
		}
	}
	return pids
}

func pidList(s string) []int {
	var pids []int
	for _, f := range strings.Fields(s) {
		if n, err := strconv.Atoi(f); err == nil {
			pids = append(pids, n)
		}
	}
	return pids
}

// processNames asks ps for each process's program name.
func processNames(ctx context.Context, pids []int) (map[int]string, error) {
	out := map[int]string{}
	if len(pids) == 0 {
		return out, nil
	}
	ids := make([]string, len(pids))
	for i, p := range pids {
		ids[i] = strconv.Itoa(p)
	}
	b, err := exec.CommandContext(ctx, "ps", "-o", "pid=,comm=", "-p", strings.Join(ids, ",")).Output()
	var ee *exec.ExitError
	if err != nil && !(errors.As(err, &ee) && ee.ExitCode() == 1) { // 1: none of them exist
		return nil, err
	}
	for _, line := range strings.Split(string(b), "\n") {
		pid, comm, ok := strings.Cut(strings.TrimSpace(line), " ")
		if n, err := strconv.Atoi(pid); ok && err == nil {
			out[n] = filepath.Base(strings.TrimSpace(comm))
		}
	}
	return out, nil
}
