package termapp

// ExecShell is not needed on Windows: its terminals keep the window open (-NoExit).
func ExecShell(string) error { return nil }
