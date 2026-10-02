package lnp

import (
	"strconv"
	"strings"
	"syscall"
)

func gated() bool {
	v, err := syscall.Sysctl("kern.osproductversion")
	if err != nil {
		return true // assume a current macOS
	}
	major, _ := strconv.Atoi(strings.SplitN(v, ".", 2)[0])
	return major >= 15
}
