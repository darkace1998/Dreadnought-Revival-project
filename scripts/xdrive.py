#!/usr/bin/env python3
"""xdrive -- drive an X display (headless Xwayland :97 or Xvfb) with XTest, no xdotool needed.

Used by scripts/headless-client.sh to get the unmodified client past its
"press any key" title screen and through any scripted clicks, then to take
the screenshots that make a headless run worth keeping. ctypes over libX11 +
libXtst only, so nothing needs installing.

    xdrive.py :97 wait-window '^DreadGame$' 120     # exit 1 on timeout
    xdrive.py :97 key space
    xdrive.py :97 hold w 3                          # press, hold 3 s, release
    xdrive.py :97 click 640 360 [button]
    xdrive.py :97 move 640 360
    xdrive.py :97 windows                           # id, geometry, name
    xdrive.py :97 run steps.txt                     # one command per line

A steps file adds `sleep SECONDS` and `shot NAME` (shot is executed by the
caller: xdrive prints `SHOT NAME` and the shell grabs the frame, so the
screenshot path and tool stay in one place).

No window manager runs on the headless display, so every key/click first
gives the game window input focus; clicks then move to the point in two
steps (UE4 needs a hover before the press).
"""
import ctypes
import ctypes.util
import re
import sys
import time

x11 = ctypes.CDLL(ctypes.util.find_library("X11"))
xtst = ctypes.CDLL(ctypes.util.find_library("Xtst"))

x11.XOpenDisplay.restype = ctypes.c_void_p
x11.XOpenDisplay.argtypes = [ctypes.c_char_p]
x11.XDefaultRootWindow.restype = ctypes.c_ulong
x11.XDefaultRootWindow.argtypes = [ctypes.c_void_p]
x11.XStringToKeysym.restype = ctypes.c_ulong
x11.XStringToKeysym.argtypes = [ctypes.c_char_p]
x11.XKeysymToKeycode.restype = ctypes.c_ubyte
x11.XKeysymToKeycode.argtypes = [ctypes.c_void_p, ctypes.c_ulong]
x11.XFlush.argtypes = [ctypes.c_void_p]
x11.XSync.argtypes = [ctypes.c_void_p, ctypes.c_int]
x11.XQueryTree.argtypes = [ctypes.c_void_p, ctypes.c_ulong,
                           ctypes.POINTER(ctypes.c_ulong), ctypes.POINTER(ctypes.c_ulong),
                           ctypes.POINTER(ctypes.POINTER(ctypes.c_ulong)),
                           ctypes.POINTER(ctypes.c_uint)]
x11.XFetchName.argtypes = [ctypes.c_void_p, ctypes.c_ulong, ctypes.POINTER(ctypes.c_char_p)]
x11.XFree.argtypes = [ctypes.c_void_p]
x11.XCloseDisplay.argtypes = [ctypes.c_void_p]
x11.XSetInputFocus.argtypes = [ctypes.c_void_p, ctypes.c_ulong, ctypes.c_int, ctypes.c_ulong]
xtst.XTestFakeKeyEvent.argtypes = [ctypes.c_void_p, ctypes.c_uint, ctypes.c_int, ctypes.c_ulong]
xtst.XTestFakeButtonEvent.argtypes = [ctypes.c_void_p, ctypes.c_uint, ctypes.c_int, ctypes.c_ulong]
xtst.XTestFakeMotionEvent.argtypes = [ctypes.c_void_p, ctypes.c_int, ctypes.c_int, ctypes.c_int, ctypes.c_ulong]


class XWindowAttributes(ctypes.Structure):
    _fields_ = [("x", ctypes.c_int), ("y", ctypes.c_int), ("width", ctypes.c_int),
                ("height", ctypes.c_int), ("border_width", ctypes.c_int), ("depth", ctypes.c_int),
                ("visual", ctypes.c_void_p), ("root", ctypes.c_ulong), ("c_class", ctypes.c_int),
                ("bit_gravity", ctypes.c_int), ("win_gravity", ctypes.c_int),
                ("backing_store", ctypes.c_int), ("backing_planes", ctypes.c_ulong),
                ("backing_pixel", ctypes.c_ulong), ("save_under", ctypes.c_int),
                ("colormap", ctypes.c_ulong), ("map_installed", ctypes.c_int),
                ("map_state", ctypes.c_int), ("all_event_masks", ctypes.c_long),
                ("your_event_mask", ctypes.c_long), ("do_not_propagate_mask", ctypes.c_long),
                ("override_redirect", ctypes.c_int), ("screen", ctypes.c_void_p)]


x11.XGetWindowAttributes.argtypes = [ctypes.c_void_p, ctypes.c_ulong, ctypes.POINTER(XWindowAttributes)]
IS_VIEWABLE = 2


def die(msg, code=2):
    print(f"xdrive: {msg}", file=sys.stderr)
    sys.exit(code)


class Display:
    def __init__(self, name):
        self.d = x11.XOpenDisplay(name.encode())
        if not self.d:
            die(f"cannot open display {name}")
        self.root = x11.XDefaultRootWindow(self.d)

    def close(self):
        x11.XCloseDisplay(self.d)

    def windows(self):
        """Every viewable named window, depth-first: (id, x, y, w, h, name)."""
        out = []

        def walk(w):
            root, parent = ctypes.c_ulong(), ctypes.c_ulong()
            kids, n = ctypes.POINTER(ctypes.c_ulong)(), ctypes.c_uint()
            if not x11.XQueryTree(self.d, w, ctypes.byref(root), ctypes.byref(parent),
                                  ctypes.byref(kids), ctypes.byref(n)):
                return
            ids = [kids[i] for i in range(n.value)]
            if kids:
                x11.XFree(kids)
            for c in ids:
                name = ctypes.c_char_p()
                attrs = XWindowAttributes()
                x11.XGetWindowAttributes(self.d, c, ctypes.byref(attrs))
                if x11.XFetchName(self.d, c, ctypes.byref(name)) and name.value is not None:
                    if attrs.map_state == IS_VIEWABLE:
                        out.append((c, attrs.x, attrs.y, attrs.width, attrs.height,
                                    name.value.decode(errors="replace")))
                    x11.XFree(name)
                walk(c)

        walk(self.root)
        return out

    def find(self, pattern):
        rx = re.compile(pattern)
        # The client opens two windows: "DreadGame" (the one to drive) and one
        # named with the full exe path -- skip anything with a backslash.
        hits = [w for w in self.windows() if rx.search(w[5]) and "\\" not in w[5]]
        return max(hits, key=lambda w: w[3] * w[4]) if hits else None

    def move(self, x, y):
        xtst.XTestFakeMotionEvent(self.d, -1, int(x), int(y), 0)
        x11.XFlush(self.d)

    def focus_game(self, centre=False):
        # Never re-centre before a click: the warp lands after UE has read the
        # target position and the press hits the screen centre instead
        #. Keys only need input focus.
        w = self.find("^DreadGame")
        if w:
            if centre:
                self.move(w[1] + w[3] // 2, w[2] + w[4] // 2)
            x11.XSetInputFocus(self.d, w[0], 1, 0)  # RevertToPointerRoot
            x11.XSync(self.d, 0)
        return w

    def key(self, keysym, hold=0.08):
        ks = x11.XStringToKeysym(keysym.encode())
        if not ks:
            die(f"unknown keysym {keysym!r}")
        kc = x11.XKeysymToKeycode(self.d, ks)
        self.focus_game()
        xtst.XTestFakeKeyEvent(self.d, kc, 1, 0)
        x11.XFlush(self.d)
        time.sleep(hold)
        xtst.XTestFakeKeyEvent(self.d, kc, 0, 0)
        x11.XFlush(self.d)

    def origin(self):
        w = self.find("^DreadGame")
        return (w[1], w[2]) if w else (0, 0)

    def click(self, x, y, button=1):
        # x, y are game-window pixels (what the shots show); the X screen is
        # larger than the window, see cycle.SCREEN
        w = self.focus_game()
        x, y = int(x) + (w[1] if w else 0), int(y) + (w[2] if w else 0)
        # approach from a few px away: UE4 widgets take hover from a real
        # motion event, and the press needs that hover a few frames old
        self.move(int(x) + 6, int(y) + 6)
        time.sleep(0.3)
        self.move(x, y)
        time.sleep(0.4)
        xtst.XTestFakeButtonEvent(self.d, int(button), 1, 0)
        x11.XFlush(self.d)
        time.sleep(0.08)
        xtst.XTestFakeButtonEvent(self.d, int(button), 0, 0)
        x11.XFlush(self.d)


def run_cmd(disp, argv):
    cmd, args = argv[0], argv[1:]
    if cmd == "wait-window":
        pattern, timeout = args[0], float(args[1]) if len(args) > 1 else 120
        end = time.time() + timeout
        while time.time() < end:
            w = disp.find(pattern)
            if w:
                print(f"WINDOW {w[0]:#x} {w[3]}x{w[4]}+{w[1]}+{w[2]} {w[5]}", flush=True)
                return 0
            time.sleep(1)
        print(f"TIMEOUT waiting for window {pattern!r}", flush=True)
        return 1
    if cmd == "key":
        for k in args:
            disp.key(k)
            time.sleep(0.3)
        return 0
    if cmd == "hold":  # hold KEY SECONDS -- flight controls need a held key
        disp.key(args[0], float(args[1]) if len(args) > 1 else 1)
        return 0
    if cmd == "click":
        disp.click(args[0], args[1], args[2] if len(args) > 2 else 1)
        return 0
    if cmd == "move":
        ox, oy = disp.origin()
        disp.move(int(args[0]) + ox, int(args[1]) + oy)
        return 0
    if cmd == "windows":
        for w in disp.windows():
            print(f"{w[0]:#x} {w[3]}x{w[4]}+{w[1]}+{w[2]} {w[5]}")
        return 0
    if cmd == "sleep":
        time.sleep(float(args[0]))
        return 0
    if cmd == "shot":
        print(f"SHOT {args[0]}", flush=True)
        return 0
    if cmd == "run":
        rc = 0
        with open(args[0]) as f:
            for line in f:
                line = line.split("#", 1)[0].strip()
                if not line:
                    continue
                print(f"STEP {line}", flush=True)
                rc = run_cmd(disp, line.split()) or rc
        return rc
    die(f"unknown command {cmd!r}")


if __name__ == "__main__":
    if len(sys.argv) < 3:
        die(__doc__.strip().splitlines()[0] + "\nusage: xdrive.py DISPLAY COMMAND [ARGS...]")
    sys.exit(run_cmd(Display(sys.argv[1]), sys.argv[2:]))
