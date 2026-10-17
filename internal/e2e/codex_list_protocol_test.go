package e2e

import (
	"bufio"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"
)

func TestCodexListProtocol(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"delayed", "initialize-error", "list-error", "missing-result"} {
		t.Run(mode, func(t *testing.T) {
			got, err := codexListExchange(t.Context(), []string{executable, "-test.run=^TestCodexListProtocolChild$", "--", "codex-list-child:" + mode}, t.TempDir())
			if mode == "delayed" {
				if err != nil || got != `{"data":[{"id":"fixture-thread"}]}` {
					t.Fatalf("delayed handshake/list: %q %v", got, err)
				}
			} else if err == nil || got != "" {
				t.Fatalf("%s accepted missing/failed response: %q %v", mode, got, err)
			} else if mode != "missing-result" && !strings.Contains(err.Error(), "fixture rejection") {
				t.Fatalf("lost protocol error: %v", err)
			}
		})
	}
}

func TestCodexListProtocolChild(t *testing.T) {
	mode, ok := strings.CutPrefix(os.Args[len(os.Args)-1], "codex-list-child:")
	if !ok {
		t.Skip("subprocess protocol fixture")
	}
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() || !strings.Contains(scanner.Text(), `"method":"initialize"`) {
		t.Fatal("first request must initialize")
	}
	lines := make(chan string)
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer close(lines)
		for scanner.Scan() {
			lines <- scanner.Text()
		}
	}()
	if mode == "delayed" {
		// Longer than the old client's fixed stdin lifetime, while rejecting
		// application messages sent before the initialize response.
		select {
		case line := <-lines:
			t.Fatalf("input closed or sent before initialize completed: %q", line)
		case <-time.After(3200 * time.Millisecond):
		}
	}
	if mode == "initialize-error" {
		fmt.Println(`{"id":1,"error":{"code":-1,"message":"fixture rejection"}}`)
	} else {
		fmt.Println(`{"method":"unrelated/status","params":{}}`)
		fmt.Println(`{"id":1,"result":{}}`)
		if line := <-lines; !strings.Contains(line, `"method":"initialized"`) {
			t.Fatalf("missing initialized: %q", line)
		}
		if line := <-lines; !strings.Contains(line, `"method":"thread/list"`) || !strings.Contains(line, `"useStateDbOnly":true`) {
			t.Fatalf("missing index-only list request: %q", line)
		}
		switch mode {
		case "list-error":
			fmt.Println(`{"id":2,"error":{"code":-1,"message":"fixture rejection"}}`)
		case "missing-result":
			// Exit cleanly without a reply: the client must not accept exit 0
			// as evidence that the index was empty.
			return
		default:
			fmt.Println(`{"id":2,"result":{"data":[{"id":"fixture-thread"}]}}`)
		}
	}
	for line := range lines {
		t.Errorf("unexpected further request: %q", line)
	}
	<-done
}
