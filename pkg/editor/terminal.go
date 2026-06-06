package editor

import (
	"bufio"
	"fmt"
	"os"
	"syscall"

	"golang.org/x/term"
)

type KeyType int

const (
	KeyRune KeyType = iota
	KeyArrowUp
	KeyArrowDown
	KeyArrowLeft
	KeyArrowRight
	KeyEnter
	KeyEscape
	KeyBackspace
	KeyTab
	KeyCtrlC
	KeyCtrlS
	KeyUnknown
)

type Key struct {
	Type KeyType
	Ch   rune
}

var oldState *term.State

func initTerminal() error {
	var err error
	oldState, err = term.MakeRaw(int(syscall.Stdin))
	if err != nil {
		return err
	}
	fmt.Print("\033[?1049h")
	fmt.Print("\033[?25l")
	return nil
}

func restoreTerminal() {
	if oldState != nil {
		term.Restore(int(syscall.Stdin), oldState)
	}
	fmt.Print("\033[?1049l")
	fmt.Print("\033[?25h")
}

func getTerminalSize() (height, width int) {
	w, h, err := term.GetSize(int(syscall.Stdout))
	if err != nil {
		return 24, 80
	}
	return h, w
}

func readKey() (Key, error) {
	reader := bufio.NewReader(os.Stdin)
	r, _, err := reader.ReadRune()
	if err != nil {
		return Key{}, err
	}

	if r == '\x1b' {
		if reader.Buffered() > 0 {
			r2, _, _ := reader.ReadRune()
			if r2 == '[' {
				r3, _, _ := reader.ReadRune()
				switch r3 {
				case 'A':
					return Key{Type: KeyArrowUp}, nil
				case 'B':
					return Key{Type: KeyArrowDown}, nil
				case 'C':
					return Key{Type: KeyArrowRight}, nil
				case 'D':
					return Key{Type: KeyArrowLeft}, nil
				}
			}
			return Key{Type: KeyEscape}, nil
		}
		return Key{Type: KeyEscape}, nil
	}

	switch r {
	case '\r', '\n':
		return Key{Type: KeyEnter}, nil
	case '\x7f', '\b':
		return Key{Type: KeyBackspace}, nil
	case '\t':
		return Key{Type: KeyTab}, nil
	case '\x03':
		return Key{Type: KeyCtrlC}, nil
	case '\x13':
		return Key{Type: KeyCtrlS}, nil
	default:
		return Key{Type: KeyRune, Ch: r}, nil
	}
}
