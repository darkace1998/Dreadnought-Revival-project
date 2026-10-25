//go:build windows

package main

// The desktop launcher: one native window hosting the launcher's own HTML in
// Microsoft Edge WebView2 (built into Windows 11, installed with Edge on most
// Windows 10 PCs). Sign-in, account creation, news and PLAY all happen in it --
// no separate browser. Without the WebView2 runtime, runDesktopLauncher
// returns false and the launcher falls back to the console + browser flow.
//
// Threading: WebView2 calls bound functions ON THE UI THREAD, so every one of
// them that touches the network starts a goroutine and reports back through
// w.Dispatch(w.Eval(...)); a blocking call would freeze the window.

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"time"
	"unsafe"

	webview2 "github.com/jchv/go-webview2"
	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// newsURL is the original launcher's tiles feed, still served by legacy-api
// (public route /v2/dreadnought/launcher/dn/tiles/{lang}/).
const newsURL = "https://legacyapi.prod.greybox.sixfoot.live/v2/dreadnought/launcher/dn/tiles/en/"

// runDesktopLauncher shows the launcher window and returns once it is closed.
// false means WebView2 is not available and nothing was shown.
func runDesktopLauncher(exeDir string, cfg Config) bool {
	if v := webView2RuntimeVersion(); v == "" {
		return false
	} else {
		fmt.Printf("[*] WebView2 runtime %s\n", v)
	}
	dataDir := filepath.Join(os.Getenv("LOCALAPPDATA"), "DreadnoughtPrivateServer", "WebView2")
	w := webview2.NewWithOptions(webview2.WebViewOptions{
		AutoFocus: true,
		DataPath:  dataDir,
		WindowOptions: webview2.WindowOptions{
			Title: "Dreadnought — Private Server",
			// Group icon #1 from winres/winres.json (rsrc_windows_amd64.syso);
			// 0 would give the window the generic application icon.
			IconId: 1,
			Width:  launcherWidth,
			Height: launcherHeight,
			Center: true,
		},
	})
	if w == nil {
		return false
	}
	defer w.Destroy()
	hideConsole()
	makeBorderless(uintptr(w.Window()))

	api := newLauncherAPI(exeDir, cfg, func() uintptr { return uintptr(w.Window()) })

	// reply runs js on the UI thread with a JSON argument.
	reply := func(fn string, v any) {
		b, _ := json.Marshal(v)
		w.Dispatch(func() { w.Eval(fn + "(" + string(b) + ")") })
	}

	// Synchronous calls return to the page's promise. Those on the UI thread
	// on purpose: the folder picker and Windows' certificate confirmation then
	// belong to this window instead of opening behind it.
	_ = w.Bind("dnInit", api.Init)
	_ = w.Bind("dnPickGame", api.PickGame)
	_ = w.Bind("dnSetGameDir", api.SetGameDir)
	_ = w.Bind("dnAutoGame", api.AutoGame)
	_ = w.Bind("dnSetOption", api.SetOption)
	_ = w.Bind("dnInstallCert", api.InstallCert)
	_ = w.Bind("dnSignOut", api.SignOut)
	_ = w.Bind("dnOpenLogs", api.OpenLogs)
	_ = w.Bind("dnAssets", api.Assets)
	_ = w.Bind("dnOpenURL", api.OpenURL)
	// The window has no frame (as the original's "windowFrame": "Borderless"),
	// so the page moves, minimizes and closes it.
	_ = w.Bind("dnWindow", func(action string) {
		hwnd := uintptr(w.Window())
		switch action {
		case "move":
			// Hand the drag to Windows as if the press were on a title bar.
			// Posted, not sent: the binding returns first, and the move loop
			// then runs on this thread's message pump.
			_, _, _ = procReleaseCapture.Call()
			_, _, _ = procPostMessageW.Call(hwnd, wmNCLButtonDown, htCaption, 0)
		case "fit":
			// The display scale changed under the window (launcher.html).
			fitWindow(hwnd, false)
		case "minimize":
			_, _, _ = procShowWindow.Call(hwnd, swMinimize)
		case "close":
			w.Terminate()
		}
	})
	// Network calls run off the UI thread and report back through a callback.
	_ = w.Bind("dnSubmit", func(mode, username, identifier, password string) {
		go func() { reply("dnAuthResult", api.Submit(mode, username, identifier, password)) }()
	})
	_ = w.Bind("dnNews", func() {
		go func() { reply("dnNewsResult", api.News()) }()
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

	w.SetHtml(desktopPageHTML)
	w.Run()
	return true
}

// newsTile is one tile of the feed. url, background_image_url and
// html_content are the original launcher's optional fields (its tiles.js and
// main.html): a picture tile with a link, or a half tile's HTML.
type newsTile struct {
	ID                 string `json:"id"`
	Title              string `json:"title"`
	Body               string `json:"body"`
	Active             bool   `json:"active"`
	Size               string `json:"section_size"`
	URL                string `json:"url,omitempty"`
	BackgroundImageURL string `json:"background_image_url,omitempty"`
	HTMLContent        string `json:"html_content,omitempty"`
}

// gameInfo is the install folder the launcher will use and how it was found,
// for the window ("" when the game was not found).
func gameInfo(exeDir string, cfg Config) map[string]any {
	p, source := gameLocation(exeDir, cfg)
	if p == "" {
		return map[string]any{"path": "", "source": ""}
	}
	return map[string]any{"path": gameInstallRoot(p), "source": source}
}

func fetchNews() ([]newsTile, error) {
	resp, err := launcherHTTPClient().Get(newsURL)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("news: HTTP %d", resp.StatusCode)
	}
	var doc struct {
		Result struct {
			Tiles []newsTile `json:"tiles"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return nil, fmt.Errorf("news: %w", err)
	}
	var out []newsTile
	for _, t := range doc.Result.Tiles {
		if t.Active {
			out = append(out, t)
		}
	}
	return out, nil
}

// hideConsole detaches the console the launcher was started with, so the
// desktop window stands alone. A launcher started from a terminal just stops
// printing there.
func hideConsole() {
	proc := windows.NewLazySystemDLL("kernel32.dll").NewProc("FreeConsole")
	_, _, _ = proc.Call()
}

// The original launcher's window: main.json "minWidth": 1105, "minHeight":
// 620, "windowFrame": "Borderless". launcher.html is laid out for exactly
// this size in CSS pixels and scales itself to the window it is given.
const (
	launcherWidth  = 1105
	launcherHeight = 620
)

var (
	user32                = windows.NewLazySystemDLL("user32.dll")
	procGetWindowLongPtrW = user32.NewProc("GetWindowLongPtrW")
	procSetWindowLongPtrW = user32.NewProc("SetWindowLongPtrW")
	procSetWindowPos      = user32.NewProc("SetWindowPos")
	procGetWindowRect     = user32.NewProc("GetWindowRect")
	procMonitorFromWindow = user32.NewProc("MonitorFromWindow")
	procGetMonitorInfoW   = user32.NewProc("GetMonitorInfoW")
	procGetDpiForWindow   = user32.NewProc("GetDpiForWindow")
	procReleaseCapture    = user32.NewProc("ReleaseCapture")
	procPostMessageW      = user32.NewProc("PostMessageW")
	procShowWindow        = user32.NewProc("ShowWindow")
	procGetDpiForMonitor  = windows.NewLazySystemDLL("shcore.dll").NewProc("GetDpiForMonitor")
)

const (
	gwlStyle                = ^uintptr(15) // GWL_STYLE (-16)
	wsCaption               = 0x00C00000
	wsThickFrame            = 0x00040000
	wsMaximizeBox           = 0x00010000
	swpNoZOrder             = 0x0004
	swpNoActivate           = 0x0010
	swpFrameChanged         = 0x0020
	wmNCLButtonDown         = 0x00A1
	htCaption               = 2
	swMinimize              = 6
	monitorDefaultToNearest = 2
)

type winRect struct{ Left, Top, Right, Bottom int32 }

func (r winRect) width() int32  { return r.Right - r.Left }
func (r winRect) height() int32 { return r.Bottom - r.Top }

type monitorInfo struct {
	Size    uint32
	Monitor winRect
	Work    winRect
	Flags   uint32
}

// makeBorderless drops the title bar and the sizing border, keeping the
// window's taskbar button and minimize, then sizes it for its monitor.
func makeBorderless(hwnd uintptr) {
	style, _, _ := procGetWindowLongPtrW.Call(hwnd, gwlStyle)
	style &^= wsCaption | wsThickFrame | wsMaximizeBox
	_, _, _ = procSetWindowLongPtrW.Call(hwnd, gwlStyle, style)
	fitWindow(hwnd, true)
}

// fitWindow sizes the window for the display it is on. The exe is per-monitor
// DPI aware (winres.json), so sizes here are physical pixels: 1105x620 times
// the display's scale, as Windows would size it -- but never larger than the
// work area. At 225% on a 3440x1440 display the scaled height, 1395, does not
// fit above a 108-pixel taskbar (1332 left), which a DPI-unaware window
// (Windows stretching the 1105x620 bitmap) could not avoid; it overlapped the
// taskbar and was blurred. The page scales its 1105x620 layout to the window
// (launcher.html, fit), so a window smaller than the full scale still shows
// all of it.
//
// center places it in the middle of the work area (the first fit); otherwise
// it keeps its top-left corner, pulled back inside the work area (the page
// asks for that when the display scale changes -- the window was moved to
// another monitor).
//
// The size is set twice when it is unchanged: go-webview2 fits the page to the
// client area on WM_SIZE, and a SetWindowPos to the size the window already
// has may not send one -- needed after the frame was removed.
func fitWindow(hwnd uintptr, center bool) {
	var cur winRect
	_, _, _ = procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&cur)))
	mon, _, _ := procMonitorFromWindow.Call(hwnd, monitorDefaultToNearest)
	mi := monitorInfo{Size: uint32(unsafe.Sizeof(monitorInfo{}))}
	if ok, _, _ := procGetMonitorInfoW.Call(mon, uintptr(unsafe.Pointer(&mi))); ok == 0 {
		mi.Work = winRect{0, 0, launcherWidth, launcherHeight}
	}
	r := fittedRect(windowDPI(hwnd, mon), mi.Work, cur, center)
	if r.width() == cur.width() && r.height() == cur.height() {
		_, _, _ = procSetWindowPos.Call(hwnd, 0, uintptr(r.Left), uintptr(r.Top), uintptr(r.width()), uintptr(r.height()+1), swpNoZOrder|swpNoActivate|swpFrameChanged)
	}
	_, _, _ = procSetWindowPos.Call(hwnd, 0, uintptr(r.Left), uintptr(r.Top), uintptr(r.width()), uintptr(r.height()), swpNoZOrder|swpNoActivate|swpFrameChanged)
}

// windowDPI is the window's display DPI (96 = 100%): GetDpiForWindow from
// Windows 10 1607, the monitor's from Windows 8.1, else 96.
func windowDPI(hwnd, mon uintptr) uint32 {
	if procGetDpiForWindow.Find() == nil {
		if dpi, _, _ := procGetDpiForWindow.Call(hwnd); dpi != 0 {
			return uint32(dpi)
		}
	}
	if procGetDpiForMonitor.Find() == nil {
		var x, y uint32
		if hr, _, _ := procGetDpiForMonitor.Call(mon, 0, uintptr(unsafe.Pointer(&x)), uintptr(unsafe.Pointer(&y))); hr == 0 && x != 0 {
			return x
		}
	}
	return 96
}

// fittedRect is the window rectangle for a display of the given DPI and work
// area: the layout at the display's scale, shrunk (aspect kept) until it fits
// in the work area.
func fittedRect(dpi uint32, work, cur winRect, center bool) winRect {
	scale := float64(dpi) / 96
	scale = min(scale, float64(work.width())/launcherWidth, float64(work.height())/launcherHeight)
	w := int32(math.Round(launcherWidth * scale))
	h := int32(math.Round(launcherHeight * scale))
	x, y := cur.Left, cur.Top
	if center {
		x = work.Left + (work.width()-w)/2
		y = work.Top + (work.height()-h)/2
	}
	x = max(work.Left, min(x, work.Right-w))
	y = max(work.Top, min(y, work.Bottom-h))
	return winRect{x, y, x + w, y + h}
}

// desktopPageHTML is the launcher page (launcher.html), shared with the
// browser version.
//
//go:embed launcher.html
var desktopPageHTML string

// webView2RuntimeVersion is Microsoft's documented detection for the Evergreen
// WebView2 runtime: a non-empty "pv" under the EdgeUpdate client key, machine-
// or per-user-wide. Checked BEFORE creating anything, because the binding's
// Embed waits for the environment's completion callback with no timeout -- a
// runtime that is absent or broken must not get that far (under Wine, which
// has none, it hung there).
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
