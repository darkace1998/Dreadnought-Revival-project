//go:build windows

package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// dn-dedicated-browser: pick a cluster, join it. The browser lists clusters
// from a master-master directory (or hand-added servers), handles the CA
// trust per cluster automatically, signs in, and starts the game pointed at
// the chosen cluster. No certificate to install by hand, no JSON editing, no
// hosts file — ever.

// defaultDirectory is the directory a distributed browser points at when no
// --directory flag and no saved value exist. Set at build time:
//
//	go build -ldflags "-X main.defaultDirectory=https://directory.example.org:8091" ./dn-server-browser
var defaultDirectory = ""

func main() {
	fmt.Println("=================================================")
	fmt.Println("  Dreadnought Server Browser v1.0")
	fmt.Println("=================================================")
	fmt.Println()

	directoryFlag := flag.String("directory", "", "master-master directory base URL (saved for next time)")
	consoleFlag := flag.Bool("console", false, "serve the browser page in the default web browser instead of a desktop window")
	signOutFlag := flag.Bool("sign-out", false, "forget saved sign-ins and exit")
	flag.Parse()
	_ = signOutFlag

	exePath, _ := os.Executable()
	exeDir := filepath.Dir(exePath)

	api := newBrowserAPI(exeDir, firstNonEmpty(strings.TrimSpace(*directoryFlag), defaultDirectory))

	if *signOutFlag {
		ensureConsole()
		api.SignOutAll()
		fmt.Println("[+] Saved sign-ins cleared.")
		return
	}

	if !*consoleFlag && runBrowserWindow(exeDir, api) {
		return
	}
	ensureConsole()
	if !*consoleFlag {
		fmt.Println("[*] The desktop window needs Microsoft Edge WebView2, which is not installed; opening the browser in your web browser instead.")
	}
	if err := runConsoleBrowser(exeDir, api); err != nil {
		fatalf("[!] %v", err)
	}
}

func fatalf(format string, args ...interface{}) {
	msg := fmt.Sprintf(format, args...)
	if !consoleReady {
		messageBox("Dreadnought Server Browser", msg)
		return
	}
	fmt.Fprintln(os.Stderr, msg)
	os.Exit(1)
}
