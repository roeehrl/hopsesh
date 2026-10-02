package all

import "testing"

func TestRegistry(t *testing.T) {
	r := Registry()
	if len(r.All()) != 2 || len(r.LoginEnv()) == 0 || len(r.Worktrees()) == 0 {
		t.Fatalf("registry: %v %v %v", r.IDs(), r.LoginEnv(), r.Worktrees())
	}
}
