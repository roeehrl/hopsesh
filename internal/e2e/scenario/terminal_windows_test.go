//go:build windows

package scenario

import "os/exec"

func setCtty(*exec.Cmd) {}
