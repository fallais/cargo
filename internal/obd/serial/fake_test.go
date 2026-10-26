package serial

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"strings"
	"testing"
)

// scriptedLine is an adapter that answers from a script: each command written
// to it queues its reply and a prompt. A command missing from the script is a
// test failure, so a test also pins down exactly what was asked.
type scriptedLine struct {
	t       *testing.T
	replies map[string][]string
	// lenient answers what the script leaves out as an adapter on an empty
	// bus would: OK to an AT command, NO DATA to anything else.
	lenient bool
	// onCommand, when set, answers first; it reports whether it did.
	onCommand func(cmd string) (string, bool)
	out       bytes.Buffer
	asked     []string
}

func (l *scriptedLine) Write(p []byte) (int, error) {
	cmd := strings.TrimSpace(string(p))
	l.asked = append(l.asked, cmd)
	if l.onCommand != nil {
		if reply, ok := l.onCommand(cmd); ok {
			l.out.WriteString(reply + "\r\r>")
			return len(p), nil
		}
	}
	queue := l.replies[cmd]
	if len(queue) == 0 && l.lenient {
		if strings.HasPrefix(cmd, "AT") {
			queue = []string{"OK"}
		} else {
			queue = []string{"NO DATA"}
		}
	}
	if len(queue) == 0 {
		l.t.Errorf("unscripted command %q", cmd)
		l.out.WriteString("?\r>")
		return len(p), nil
	}
	reply := queue[0]
	if len(queue) > 1 {
		l.replies[cmd] = queue[1:]
	}
	l.out.WriteString(reply + "\r\r>")
	return len(p), nil
}

func (l *scriptedLine) Read(p []byte) (int, error) {
	if l.out.Len() == 0 {
		return 0, io.EOF
	}
	return l.out.Read(p)
}

func (l *scriptedLine) Close() error { return nil }
func (l *scriptedLine) Flush() error { return nil }

// newScriptedELM returns an adapter answering each command with its replies in
// turn, the last one repeating.
func newScriptedELM(t *testing.T, replies map[string][]string) (*ELM327, *scriptedLine) {
	line := &scriptedLine{t: t, replies: replies}
	return &ELM327{port: line, reader: bufio.NewReader(line)}, line
}

// newScriptedProvider returns a connected provider on a CAN bus whose adapter
// answers from the script, leniently.
func newScriptedProvider(t *testing.T, replies map[string][]string) (*SerialOBD, *scriptedLine) {
	elm, line := newScriptedELM(t, replies)
	line.lenient = true
	elm.protocol = "6"
	s := New(Options{})
	s.elm, s.connected = elm, true
	return s, line
}

var fmtSscanf = fmt.Sscanf
