package pty

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"sync"
)

// Attach shows a tab that runs elsewhere (hopsesh serve on another machine, over conn) in
// the terminal in and out belong to: the tab's scrollback and output go to out, the keys
// read from in go to the program, and each size from sizes resizes it. It returns the
// program's exit code once it ends, or an error when the connection ends first (the tab
// keeps running there; attaching again shows it). problems is told what the far end
// refused.
func Attach(conn Conn, id string, in io.Reader, out io.Writer, sizes <-chan [2]int, problems func(string)) (int, error) {
	defer conn.Close()
	if err := conn.Send(append([]byte{opAttach}, id...)); err != nil {
		return -1, err
	}
	var sendMu sync.Mutex
	send := func(f []byte) error {
		sendMu.Lock()
		defer sendMu.Unlock()
		return conn.Send(f)
	}
	go func() {
		for sz := range sizes {
			f := make([]byte, 5)
			f[0] = opResize
			binary.BigEndian.PutUint16(f[1:], uint16(sz[0]))
			binary.BigEndian.PutUint16(f[3:], uint16(sz[1]))
			if send(f) != nil {
				return
			}
		}
	}()
	go func() {
		buf := make([]byte, 32<<10)
		for {
			n, err := in.Read(buf)
			if n > 0 && send(append([]byte{opInput}, buf[:n]...)) != nil {
				return
			}
			if err != nil {
				return
			}
		}
	}()
	for {
		f, err := conn.Receive()
		if err != nil {
			return -1, errors.New("the connection to the terminal ended; it keeps running there")
		}
		if len(f) == 0 {
			continue
		}
		switch body := f[1:]; f[0] {
		case frameOutput:
			if _, err := out.Write(body); err != nil {
				return -1, err
			}
			ack := make([]byte, 5)
			ack[0] = opAck
			binary.BigEndian.PutUint32(ack[1:], uint32(len(body)))
			_ = send(ack)
		case frameState:
			var i Info
			if json.Unmarshal(body, &i) == nil && i.State == Exited {
				return i.Code, nil
			}
		case frameError:
			if problems != nil {
				problems(string(body))
			}
		}
	}
}
