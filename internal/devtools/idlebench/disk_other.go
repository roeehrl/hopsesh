//go:build !linux && !darwin && !windows

package main

func diskWritten() int64 { return -1 }

func wakeups() int64 { return -1 }
