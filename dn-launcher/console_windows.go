//go:build windows

package main

import (
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The launcher is built as a GUI program (-ldflags "-H windowsgui") so the
// desktop window opens without a console flashing up first. The console flow
// (--console, a pinned player_id, or no WebView2) still needs one, so it makes
// its own: ensureConsole reuses a console the launcher already has (a console
// build, or started from a terminal of a console build) and otherwise opens a
// new window and points stdin/stdout/stderr at it.

var kernel32 = windows.NewLazySystemDLL("kernel32.dll")

// consoleReady is true once output has somewhere to go; fatalf shows a message
// box until then.
var consoleReady bool

func ensureConsole() {
	if consoleReady {
		return
	}
	// Output that already goes somewhere stays there: a pipe or file, or --
	// under Wine, where dn-launcher-linux.sh runs this launcher -- the Unix
	// terminal. Allocating a console there opened a separate Wine console
	// window and the terminal lost every line after "Server: ...", including
	// the sign-in address. A GUI program double-clicked on Windows has no
	// standard handles at all, so it still gets its own console below.
	if h, err := windows.GetStdHandle(windows.STD_OUTPUT_HANDLE); err == nil && h != 0 && h != windows.InvalidHandle {
		if t, err := windows.GetFileType(h); err == nil && t != windows.FILE_TYPE_UNKNOWN {
			consoleReady = true
			return
		}
	}
	if hwnd, _, _ := kernel32.NewProc("GetConsoleWindow").Call(); hwnd == 0 {
		if r, _, _ := kernel32.NewProc("AllocConsole").Call(); r == 0 {
			return
		}
		if out, err := os.OpenFile("CONOUT$", os.O_RDWR, 0); err == nil {
			os.Stdout, os.Stderr = out, out
		}
		if in, err := os.OpenFile("CONIN$", os.O_RDWR, 0); err == nil {
			os.Stdin = in
		}
	}
	consoleReady = true
}

func messageBox(title, text string) {
	t, _ := windows.UTF16PtrFromString(title)
	m, _ := windows.UTF16PtrFromString(text)
	const mbIconError = 0x10
	_, _, _ = windows.NewLazySystemDLL("user32.dll").NewProc("MessageBoxW").Call(0,
		uintptr(unsafe.Pointer(m)), uintptr(unsafe.Pointer(t)), mbIconError)
}
