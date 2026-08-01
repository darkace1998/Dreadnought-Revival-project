//go:build windows

package server

import (
	"os/exec"
	"sync"
	"syscall"
	"unsafe"
)

// Window suppression for the battle server.
//
// Why this is needed at all: "-nullrhi" stops the engine RENDERING, it does not
// stop it creating a game window. There is no "-RenderOffScreen" in UE 4.13, and
// a genuinely windowless mode would need a Server target binary, which this game
// never shipped -- the battle server is the ordinary client executable. So the
// process opens exactly one visible top-level window titled "DreadGame" and sits
// there, which is not what anyone wants from a headless dedicated server.
//
// Two mechanisms are used, in this order of importance:
//
//  1. STARTUPINFO's nCmdShow set to SW_HIDE (SysProcAttr.HideWindow). Measured:
//     this alone is sufficient on this build. With it set, the process ends up
//     with six top-level windows and NONE of them visible, and MainWindowHandle
//     is 0; without it, exactly one visible window titled "DreadGame" appears.
//     An earlier version of this comment guessed that UE would ignore nCmdShow.
//     That was wrong, and it was tested: the enumeration sweep below never found
//     a visible window to hide, because there was never one to find.
//  2. Enumerating the child's top-level windows after launch and hiding any that
//     are visible. This is a fallback for the case where the engine creates or
//     re-shows a window later -- during map load, or on a fullscreen change --
//     which nCmdShow cannot cover. It is polled because such a window would
//     appear well after process start, so a single sweep at launch finds
//     nothing. On the runs measured here it never fires.

var (
	user32                       = syscall.NewLazyDLL("user32.dll")
	procEnumWindows              = user32.NewProc("EnumWindows")
	procGetWindowThreadProcessID = user32.NewProc("GetWindowThreadProcessId")
	procShowWindow               = user32.NewProc("ShowWindow")
	procIsWindowVisible          = user32.NewProc("IsWindowVisible")
)

// swHide is the SW_HIDE nCmdShow value.
const swHide = 0

// hideState carries the target pid into the enum callback and counts hits.
//
// The callback is created ONCE, at package level, rather than per call:
// syscall.NewCallback allocations live for the process lifetime and the runtime
// caps how many can exist, so building one per sweep would leak until it
// panicked. The mutex makes the shared target safe, and is held for the whole
// EnumWindows call, which invokes the callback synchronously.
var hideState struct {
	sync.Mutex
	pid    uint32
	hidden int
}

var enumWindowsCallback = syscall.NewCallback(func(hwnd, _ uintptr) uintptr {
	var pid uint32
	_, _, _ = procGetWindowThreadProcessID.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	if pid == hideState.pid {
		if visible, _, _ := procIsWindowVisible.Call(hwnd); visible != 0 {
			_, _, _ = procShowWindow.Call(hwnd, swHide)
			hideState.hidden++
		}
	}
	return 1 // keep enumerating
})

// hideProcessWindows hides every visible top-level window owned by pid and
// returns how many it hid.
func hideProcessWindows(pid int) int {
	hideState.Lock()
	defer hideState.Unlock()
	hideState.pid = uint32(pid)
	hideState.hidden = 0
	_, _, _ = procEnumWindows.Call(enumWindowsCallback, 0)
	return hideState.hidden
}

// configureHidden asks Windows to start the process with its window hidden.
func configureHidden(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.HideWindow = true
}
