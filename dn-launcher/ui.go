//go:build windows

package main

import (
	"fmt"
	"strings"

	"golang.org/x/sys/windows"
)

// openBrowser opens address in the default browser through ShellExecute.
// CHANGED 2026-09-27: this ran "rundll32 url.dll,FileProtocolHandler", a
// well-known living-off-the-land pattern that behaviour-based security software
// reports as malicious tool execution (a tester's PC flagged the launcher as
// "post-exploit via malicious tool execution").
func openBrowser(address string) {
	verb, _ := windows.UTF16PtrFromString("open")
	target, _ := windows.UTF16PtrFromString(address)
	if err := windows.ShellExecute(0, verb, target, nil, nil, windows.SW_SHOWNORMAL); err != nil {
		fmt.Printf("[!] Could not open a browser automatically. Open this address yourself:\n    %s\n", address)
	}
}

func capitalise(text string) string {
	if text == "" {
		return text
	}
	return strings.ToUpper(text[:1]) + text[1:]
}
