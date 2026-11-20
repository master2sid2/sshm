// Package termutil reads secrets and answers from the controlling terminal.
package termutil

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"golang.org/x/term"
)

// ErrCancelled is returned when the user presses ctrl+c (or ctrl+d on an
// empty line) at a prompt.
var ErrCancelled = errors.New("cancelled")

// ReadSecret prints prompt on stderr and reads a line from stdin without
// echoing it.
//
// The terminal is put in raw mode for the duration and restored afterwards,
// and ctrl+c is handled as a key, not a signal: it cancels the prompt with
// ErrCancelled. term.ReadPassword keeps signals enabled, so ctrl+c there
// either killed the process with echo still off or was swallowed by a signal
// handler, leaving the prompt impossible to leave.
//
// When stdin is not a terminal (a pipe in tests or scripts) the line is read
// as is.
func ReadSecret(prompt string) (string, error) {
	return readLine(prompt, false)
}

// ReadLine is ReadSecret with echo, for non-secret answers such as a
// keyboard-interactive question the server wants shown.
func ReadLine(prompt string) (string, error) {
	return readLine(prompt, true)
}

// piped is the buffered reader for a non-terminal stdin. It must be shared
// between prompts: a reader per prompt reads ahead and swallows the lines
// meant for the next ones.
var piped struct {
	file   *os.File
	reader *bufio.Reader
}

func pipeReader() *bufio.Reader {
	if piped.file != os.Stdin { // first use, or stdin was replaced (tests)
		piped.file, piped.reader = os.Stdin, bufio.NewReader(os.Stdin)
	}
	return piped.reader
}

func readLine(prompt string, echo bool) (string, error) {
	fd := int(os.Stdin.Fd())
	fmt.Fprint(os.Stderr, prompt)
	if !term.IsTerminal(fd) {
		line, err := pipeReader().ReadString('\n')
		fmt.Fprintln(os.Stderr)
		if err != nil && (line == "" || !errors.Is(err, io.EOF)) {
			return "", err
		}
		return strings.TrimRight(line, "\r\n"), nil
	}

	old, err := term.MakeRaw(fd)
	if err != nil {
		return "", err
	}
	defer term.Restore(fd, old) //nolint:errcheck // best effort on the way out

	out := io.Discard
	if echo {
		out = os.Stderr
	}
	line, err := editLine(os.Stdin, out)
	fmt.Fprint(os.Stderr, "\r\n")
	return line, err
}

// editLine reads keystrokes from r until enter, applying backspace, ctrl+u
// and ctrl+w, and echoing to echo (io.Discard for secrets). In raw mode the
// terminal does none of this itself.
func editLine(r io.Reader, echo io.Writer) (string, error) {
	var buf []rune
	br := bufio.NewReader(r)
	for {
		c, _, err := br.ReadRune()
		if err != nil {
			if errors.Is(err, io.EOF) && len(buf) > 0 {
				return string(buf), nil
			}
			return "", err
		}
		switch c {
		case '\r', '\n':
			return string(buf), nil
		case 3: // ctrl+c
			return "", ErrCancelled
		case 4: // ctrl+d
			if len(buf) == 0 {
				return "", ErrCancelled
			}
		case 127, 8: // backspace
			if len(buf) > 0 {
				buf = buf[:len(buf)-1]
				fmt.Fprint(echo, "\b \b")
			}
		case 21: // ctrl+u: clear the line
			fmt.Fprint(echo, strings.Repeat("\b \b", len(buf)))
			buf = buf[:0]
		case 23: // ctrl+w: delete the previous word
			n := len(buf)
			for n > 0 && buf[n-1] == ' ' {
				n--
			}
			for n > 0 && buf[n-1] != ' ' {
				n--
			}
			fmt.Fprint(echo, strings.Repeat("\b \b", len(buf)-n))
			buf = buf[:n]
		case 27: // escape sequences (arrows, ...): ignore
			if next, _, err := br.ReadRune(); err == nil && next == '[' {
				for {
					b, _, err := br.ReadRune()
					if err != nil || (b >= 0x40 && b <= 0x7e) {
						break
					}
				}
			}
		default:
			if c >= 32 {
				buf = append(buf, c)
				fmt.Fprint(echo, string(c))
			}
		}
	}
}

// Confirm asks a yes/no question on stderr. def is the answer for a bare
// enter; ctrl+c or a read error count as "no".
func Confirm(prompt string, def bool) bool {
	hint := " [y/N] "
	if def {
		hint = " [Y/n] "
	}
	ans, err := ReadLine(prompt + hint)
	if err != nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(ans)) {
	case "":
		return def
	case "y", "yes":
		return true
	default:
		return false
	}
}
