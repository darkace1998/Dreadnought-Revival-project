//go:build windows

package main

// The browser window: Microsoft Edge WebView2 hosting the browser page.
// Mirrors dn-launcher's desktop window (same threading contract: WebView2
// calls bound functions ON THE UI THREAD, so network calls run in a
// goroutine and report back through w.Dispatch(w.Eval(...))).

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	webview2 "github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// runBrowserWindow shows the server browser and returns once it is closed.
// false means WebView2 is not available and nothing was shown.
func runBrowserWindow(exeDir string, api *browserAPI) bool {
	if v := webView2RuntimeVersion(); v == "" {
		return false
	} else {
		fmt.Printf("[*] WebView2 runtime %s\n", v)
	}
	dataDir := filepath.Join(os.Getenv("LOCALAPPDATA"), "DreadnoughtPrivateServer", "WebView2Browser")
	w := webview2.NewWithOptions(webview2.WebViewOptions{
		AutoFocus: true,
		DataPath:  dataDir,
		WindowOptions: webview2.WindowOptions{
			Title:  "Dreadnought — Server Browser",
			IconId: 1,
			Width:  980,
			Height: 680,
			Center: true,
		},
	})
	if w == nil {
		return false
	}
	defer w.Destroy()
	hideConsole()

	reply := func(fn string, v any) {
		b, _ := json.Marshal(v)
		w.Dispatch(func() { w.Eval(fn + "(" + string(b) + ")") })
	}

	_ = w.Bind("dnDirectory", api.DirectoryUI)
	_ = w.Bind("dnSetDirectory", api.SetDirectoryUI)
	_ = w.Bind("dnInit", api.Init)
	_ = w.Bind("dnRefreshDir", func() {
		go func() { reply("dnDirectoryResult", api.DirectoryUI()) }()
	})
	_ = w.Bind("dnSetDirectory", func(url string) {
		go func() { reply("dnSetDirectoryResult", api.SetDirectoryUI(url)) }()
	})
	_ = w.Bind("dnSelectCluster", func(id string) {
		go func() { reply("dnEnterCluster", api.SelectCluster(id)) }()
	})
	_ = w.Bind("dnSelectManual", func(url string) {
		go func() { reply("dnEnterCluster", api.SelectManual(url)) }()
	})
	_ = w.Bind("dnAddManual", func(name, url, ca string) {
		go func() { reply("dnAddManualResult", api.AddManualUI(name, url, ca)) }()
	})
	_ = w.Bind("dnRemoveManual", func(url string) {
		go func() { api.RemoveManualUI(url) }()
	})
	_ = w.Bind("dnConfirmCert", func() {
		go func() { reply("dnConfirmCertResult", api.ConfirmCert()) }()
	})
	_ = w.Bind("dnPickGame", api.PickGame)
	_ = w.Bind("dnSetOption", api.SetOption)
	_ = w.Bind("dnSignOut", api.SignOut)
	_ = w.Bind("dnOpenLogs", api.OpenLogs)
	_ = w.Bind("dnSubmit", func(mode, username, identifier, password string) {
		go func() { reply("dnAuthResult", api.Submit(mode, username, identifier, password)) }()
	})
	_ = w.Bind("dnNews", func() {
		go func() { reply("dnNewsResult", api.ClusterNews()) }()
	})
	_ = w.Bind("dnPlay", func() {
		go func() {
			r := api.Play()
			reply("dnPlayResult", r)
			if r["ok"] == true {
				time.Sleep(2500 * time.Millisecond)
				w.Dispatch(w.Terminate)
			}
		}()
	})

	w.SetHtml(browserPageHTML)
	w.Run()
	return true
}

// webView2RuntimeVersion is Microsoft's documented detection for the Evergreen
// WebView2 runtime (same as dn-launcher's).
func webView2RuntimeVersion() string {
	const client = `Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}`
	for _, loc := range []struct {
		root registry.Key
		path string
	}{
		{registry.LOCAL_MACHINE, `SOFTWARE\WOW6432Node\` + client},
		{registry.LOCAL_MACHINE, `SOFTWARE\` + client},
		{registry.CURRENT_USER, `Software\` + client},
	} {
		k, err := registry.OpenKey(loc.root, loc.path, registry.QUERY_VALUE)
		if err != nil {
			continue
		}
		v, _, err := k.GetStringValue("pv")
		_ = k.Close()
		if err == nil && v != "" && v != "0.0.0.0" {
			return v
		}
	}
	return ""
}

// hideConsole detaches the console the browser was started with, so the
// desktop window stands alone.
func hideConsole() {
	proc := windows.NewLazySystemDLL("kernel32.dll").NewProc("FreeConsole")
	_, _, _ = proc.Call()
}
