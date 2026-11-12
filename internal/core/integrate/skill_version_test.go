package integrate

import (
	"os"
	"path/filepath"
	"testing"
)

// An older hopsesh never "updates" a skill a newer hopsesh installed; a newer one replaces
// an older copy, and a development build keeps the hash-only behavior.
func TestCheckSkillVersions(t *testing.T) {
	v1 := map[string][]byte{"SKILL.md": []byte("skill 0.4.6")}
	v0 := map[string][]byte{"SKILL.md": []byte("skill 0.4.5")}
	dir := filepath.Join(t.TempDir(), SkillName)
	if _, err := InstallSkill(dir, "0.4.6", v1, false); err != nil {
		t.Fatal(err)
	}
	if st := CheckSkill(dir, v0, "0.4.5"); st.State != Current || !st.Newer || st.Version != "0.4.6" {
		t.Fatalf("older build: %+v", st)
	}
	if st, err := InstallSkill(dir, "0.4.5", v0, false); err != nil || !st.Newer {
		t.Fatalf("older build installed over a newer copy: %+v %v", st, err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "SKILL.md")); string(b) != "skill 0.4.6" {
		t.Fatalf("downgraded: %s", b)
	}
	if st := CheckSkill(dir, v0, "0.4.7-rc.1"); st.State != Stale || st.Newer {
		t.Fatalf("newer pre-release: %+v", st)
	}
	if st := CheckSkill(dir, v0, "dev"); st.State != Stale {
		t.Fatalf("dev build: %+v", st)
	}
	if st := CheckSkill(dir, v1, "0.4.5"); st.State != Current || st.Newer {
		t.Fatalf("same files: %+v", st)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("edited"), 0o644); err != nil {
		t.Fatal(err)
	}
	if st := CheckSkill(dir, v0, "0.4.5"); st.State != Modified {
		t.Fatalf("an edit still wins: %+v", st)
	}
}
