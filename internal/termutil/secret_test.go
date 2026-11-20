package termutil

import (
	"errors"
	"io"
	"strings"
	"testing"
)

func TestEditLine(t *testing.T) {
	cases := []struct {
		name, in, want string
		err            error
	}{
		{"plain", "secret\r", "secret", nil},
		{"newline", "secret\n", "secret", nil},
		{"backspace", "secrx\x7fet\r", "secret", nil},
		{"ctrl+u clears", "wrong\x15right\r", "right", nil},
		{"ctrl+w deletes word", "one two\x17three\r", "one three", nil},
		{"arrow keys ignored", "se\x1b[Dcret\r", "secret", nil},
		{"unicode", "пароль\r", "пароль", nil},
		{"ctrl+c cancels", "sec\x03ret\r", "", ErrCancelled},
		{"ctrl+d on empty cancels", "\x04", "", ErrCancelled},
		{"eof after text", "secret", "secret", nil},
		{"eof on empty", "", "", io.EOF},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := editLine(strings.NewReader(tc.in), io.Discard)
			if !errors.Is(err, tc.err) || got != tc.want {
				t.Errorf("editLine(%q) = %q, %v; want %q, %v", tc.in, got, err, tc.want, tc.err)
			}
		})
	}
}

func TestEditLineEchoesOnlyWhatIsKept(t *testing.T) {
	var echo strings.Builder
	if _, err := editLine(strings.NewReader("ab\x7fc\r"), &echo); err != nil {
		t.Fatal(err)
	}
	if got, want := echo.String(), "ab\b \bc"; got != want {
		t.Errorf("echo = %q, want %q", got, want)
	}
}
