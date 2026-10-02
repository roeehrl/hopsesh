package secrets

import (
	"fmt"
	"os"
	"testing"
	"time"
)

// TestKeychainRoundTrip uses the real login Keychain, so it only runs when asked
// (HOPSESH_TEST_KEYCHAIN=1) on a Mac.
func TestKeychainRoundTrip(t *testing.T) {
	if os.Getenv("HOPSESH_TEST_KEYCHAIN") == "" || !Available() {
		t.Skip("set HOPSESH_TEST_KEYCHAIN=1 on a Mac to run")
	}
	acct := fmt.Sprintf("test-%d nobody@example.invalid", time.Now().UnixNano())
	pw := `a "quoted" pass\word with spaces`
	defer Delete(acct)
	if err := Set(acct, pw); err != nil {
		t.Fatal(err)
	}
	if got, ok := Get(acct); !ok || got != pw {
		t.Fatalf("got %q %v", got, ok)
	}
	Delete(acct)
	if _, ok := Get(acct); ok {
		t.Fatal("still there after Delete")
	}
}
