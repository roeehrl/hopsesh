package e2e

import (
	"context"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/x/vt"

	"github.com/roeehrl/hopsesh/internal/app"
	"github.com/roeehrl/hopsesh/internal/core/move"
)

// atTerminal is the user at the terminal where a hand-off's terminal step runs (claude
// --cloud through hopsesh's relay): a screen (a terminal emulator, which answers the
// program's queries as the user's terminal does) and a keyboard. When the stand-in claude
// asks whether the folder is trusted, the user types answer ("\r" or "1": yes, "2": no,
// "": nothing, so the program waits until the keyboard closes). The test types here, as
// the user would; hopsesh never does. When hopsesh saw no link and asks for it, the user
// pastes paste ("" for none).
type atTerminal struct {
	answer string
	paste  string

	mu    sync.Mutex
	asked int // times the trust question was on the screen
	steps int
	last  string // the screen when the last step ended
}

func (u *atTerminal) runner(a *app.App) move.StepRunner {
	return func(_ context.Context, s move.TermStep) (move.StepResult, error) {
		u.mu.Lock()
		u.steps++
		answer, paste := u.answer, u.paste
		u.mu.Unlock()
		scr := &userScreen{emu: vt.NewEmulator(100, 30)}
		keys, keyboard := io.Pipe()
		var kmu sync.Mutex
		press := func(b []byte) {
			kmu.Lock()
			defer kmu.Unlock()
			_, _ = keyboard.Write(b)
		}
		go func() { // the terminal's answers to the program's queries
			buf := make([]byte, 1024)
			for {
				n, err := scr.emu.Read(buf)
				if n > 0 {
					press(buf[:n])
				}
				if err != nil {
					return
				}
			}
		}()
		stop := make(chan struct{})
		watched := make(chan struct{})
		go func() { // the user, reading the screen
			defer close(watched)
			for {
				select {
				case <-stop:
					return
				case <-time.After(30 * time.Millisecond):
				}
				if strings.Contains(scr.text(), "Quick safety check") {
					u.mu.Lock()
					u.asked++
					u.mu.Unlock()
					if answer != "" {
						press([]byte(answer))
					} else {
						_ = keyboard.Close()
					}
					return
				}
			}
		}()
		res, err := a.RunStepHere(s, keys, scr, func() string { return paste })
		close(stop)
		<-watched
		_ = keyboard.Close()
		u.mu.Lock()
		u.last = scr.text()
		u.mu.Unlock()
		return res, err
	}
}

// userScreen is the emulator, safe for the relay's writes and the user's reads.
type userScreen struct {
	mu  sync.Mutex
	emu *vt.Emulator
}

func (s *userScreen) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.emu.Write(p)
}

func (s *userScreen) text() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.emu.String()
}
