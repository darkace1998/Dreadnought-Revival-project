//go:build windows

package main

import (
	"os"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The browser is built as a GUI program (-ldflags "-H windowsgui") so the
// desktop window opens without a console flashing up first. The console flow
// (--console, or no WebView2) still needs one.
var kernel32 = windows.NewLazySystemDLL("kernel32.dll")

var consoleReady bool

func ensureConsole() {
	if consoleReady {
		return
	}
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

func openBrowser(address string) {
	verb, _ := windows.UTF16PtrFromString("open")
	target, _ := windows.UTF16PtrFromString(address)
	if err := windows.ShellExecute(0, verb, target, nil, nil, windows.SW_SHOWNORMAL); err != nil {
		println("[!] Could not open a browser automatically. Open this address yourself:")
		println("   ", address)
	}
}

func capitalise(text string) string {
	if text == "" {
		return text
	}
	return strings.ToUpper(text[:1]) + text[1:]
}
