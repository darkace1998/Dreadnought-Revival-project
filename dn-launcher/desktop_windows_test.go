//go:build windows

package main

import (
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

// The window is the layout at the display's scale, shrunk to the work area
// when that does not fit. The first case is a player's: 225% on 3440x1440,
// where the scaled 2486x1395 is taller than the 1332 pixels above a 108-pixel
// taskbar.
func TestFittedRect(t *testing.T) {
	for _, c := range []struct {
		name   string
		dpi    uint32
		work   winRect
		cur    winRect
		center bool
		want   winRect
	}{
		{"225% ultrawide", 216, winRect{0, 0, 3440, 1332}, winRect{}, true, winRect{533, 0, 2907, 1332}},
		{"100% 1080p", 96, winRect{0, 0, 1920, 1040}, winRect{}, true, winRect{407, 210, 1512, 830}},
		{"150% 4K", 144, winRect{0, 0, 3840, 2088}, winRect{}, true, winRect{1091, 579, 2749, 1509}},
		{"125% 720p", 120, winRect{0, 0, 1280, 680}, winRect{}, true, winRect{34, 0, 1246, 680}},
		// A second monitor to the right: the corner is kept, the window pulled
		// back inside its work area.
		{"refit keeps corner", 96, winRect{3440, 0, 5360, 1040}, winRect{4800, 100, 7174, 1432}, false, winRect{4255, 100, 5360, 720}},
	} {
		if got := fittedRect(c.dpi, c.work, c.cur, c.center); got != c.want {
			t.Errorf("%s: got %+v, want %+v", c.name, got, c.want)
		}
	}
}

// The calls themselves, on a real window: it comes out the size fittedRect
// gives for its monitor, and borderless.
func TestFitWindow(t *testing.T) {
	class, _ := windows.UTF16PtrFromString("STATIC")
	hwnd, _, _ := user32.NewProc("CreateWindowExW").Call(0, uintptr(unsafe.Pointer(class)), 0,
		0x00CF0000, 0, 0, 640, 480, 0, 0, 0, 0)
	if hwnd == 0 {
		t.Skip("no window station")
	}
	defer func() { _, _, _ = user32.NewProc("DestroyWindow").Call(hwnd) }()
	makeBorderless(hwnd)

	mon, _, _ := procMonitorFromWindow.Call(hwnd, monitorDefaultToNearest)
	mi := monitorInfo{Size: uint32(unsafe.Sizeof(monitorInfo{}))}
	_, _, _ = procGetMonitorInfoW.Call(mon, uintptr(unsafe.Pointer(&mi)))
	want := fittedRect(windowDPI(hwnd, mon), mi.Work, winRect{}, true)
	var got winRect
	_, _, _ = procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&got)))
	t.Logf("dpi %d, work area %+v: window %+v", windowDPI(hwnd, mon), mi.Work, got)
	if got != want {
		t.Errorf("window %+v, want %+v (work area %+v, dpi %d)", got, want, mi.Work, windowDPI(hwnd, mon))
	}
	style, _, _ := procGetWindowLongPtrW.Call(hwnd, gwlStyle)
	if style&(wsCaption|wsThickFrame) != 0 {
		t.Errorf("style %#x still has a frame", style)
	}
}
