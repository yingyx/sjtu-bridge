package auth

import (
	"fmt"
	"io"
	"os"

	"github.com/yingyx/sjtu-bridge/contracts"
	"golang.org/x/term"
	"rsc.io/qr"
)

// RequireQRDisplay checks both ends before any login network activity. The
// signed QR URL is a short-lived secret and must not land in redirected logs.
func RequireQRDisplay(input io.Reader, output io.Writer) error {
	if e := RequireTerminal(input); e != nil {
		return e
	}
	f, ok := output.(*os.File)
	if !ok || !term.IsTerminal(int(f.Fd())) {
		return contracts.Fail("AUTH_REQUIRED", "QR login requires a real terminal for input and display; output cannot be redirected")
	}
	return nil
}

func DisplayQR(input io.Reader, output io.Writer, value string) error {
	if e := RequireQRDisplay(input, output); e != nil {
		return e
	}
	code, e := qr.Encode(value, qr.L)
	if e != nil {
		return contracts.Fail("UPSTREAM_CHANGED", "Could not encode login QR challenge")
	}
	// Half blocks preserve square modules while fitting the full quiet zone in
	// a normal terminal. Never print the raw signed URL as a fallback.
	for y := -4; y < code.Size+4; y += 2 {
		if _, e = fmt.Fprint(output, "\x1b[30;47m"); e != nil {
			return e
		}
		for x := -4; x < code.Size+4; x++ {
			top, bottom := code.Black(x, y), code.Black(x, y+1)
			cell := " "
			switch {
			case top && bottom:
				cell = "█"
			case top:
				cell = "▀"
			case bottom:
				cell = "▄"
			}
			if _, e = fmt.Fprint(output, cell); e != nil {
				return e
			}
		}
		if _, e = fmt.Fprintln(output, "\x1b[0m"); e != nil {
			return e
		}
	}
	return nil
}
