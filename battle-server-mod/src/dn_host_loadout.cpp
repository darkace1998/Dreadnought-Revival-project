// dn_host_loadout.cpp -- the host-side loadout fix, and nothing else.
//
// WHY THIS EXISTS
//
// UYLoadoutManagerComponent is populated by exactly one path:
// InitializeFromPlayerData, which reads the YMmogbrain module at +0x3898, which
// requires a login the battle server never performs. LoadInstallingLadouts
// would install exactly the four T1 mediums the client offers, and it sits
// behind the same gate; there is no third caller. So on a host the manager is
// empty, every UYLoadoutManagerComponent::FindLoadoutByID misses, and
// AYGameMode::SpawnDefaultPawn refuses to create a pawn -- the player sits on
// the orbit camera watching the planet for the whole match.
//
// This hooks FindLoadoutByID and, ON A MISS ONLY, registers the four cooked
// precast loadout assets with the manager and re-runs the engine's own lookup.
// A lookup that already succeeded is never touched. It supplies data the engine
// was designed to have and then gets out of the way, so it stays correct if a
// real backend ever fills the manager.
//
// It is deliberately NOT a general-purpose mod. In particular it does not force
// PlayerController+0x948 (EYOrbitReadyState) -- the engine computes that value
// correctly from the fleet slot count, and writing to it would be lying to a
// gate rather than filling a hole. See battle-server-mod/README.md.
//
// VERIFIED ADDRESSES (DreadGame-Win64-Shipping.exe, the 2018 retail build)
//
// Every RVA below was confirmed against the PE exception directory (.pdata) as
// a real RUNTIME_FUNCTION entry, not a chained cold chunk and not the middle of
// an instruction:
//
//   0x340340-0x3404D3  UYLoadoutManagerComponent::FindLoadoutByID(mgr, id, warn)
//                      `id` is an FName; the loop compares the 8 bytes at
//                      loadout+0xB0.
//   0x3382F0-0x338330  AddLoadout(mgr, loadout, uint8 type) -- ADD ONLY.
//                      NOT 0x337450: that is AddAndActivateLoadout, whose tail
//                      calls 0x337050 unconditionally, so registering four
//                      loadouts in a row through it leaves the LAST one active.
//                      The array ends with Support, which is why every hull a
//                      player picked used to spawn as a Cerberus.
//                      Type 2 matters: the validity gate at 0x33C680 rejects
//                      loadouts recorded as type 4.
//   0xD78110-0xD789B6  StaticLoadObject/StaticLoadClass, 7 arguments.
//
// And two data offsets, from the generated SDK for this build:
//
//   0x3F63A70          GObjects (FUObjectArray)
//   0x3E069D0          GNames   (TStaticIndirectArrayThreadSafeRead)
//
// GNames is used for log text only. If its layout were wrong the worst outcome
// is an unnamed line in the log; no decision depends on it.

#include <windows.h>

#include <share.h>
#include <stdarg.h>
#include <stdint.h>
#include <stdio.h>
#include <string.h>

#include "MinHook.h"

// ---------------------------------------------------------------------------
// Logging
//
// S14.4 asked for lines that are distinguishable from engine output on both the
// install and the fire. Everything here carries the [dn-host-loadout] tag and
// goes to stdout (which dn-dedicated captures) as well as a file beside the
// executable, so a host log tells you whether the hook was even present.
// ---------------------------------------------------------------------------

static FILE *g_logFile = nullptr;

static void LogOpen() {
  char path[MAX_PATH];
  if (!GetModuleFileNameA(NULL, path, MAX_PATH))
    return;
  char *slash = strrchr(path, '\\');
  if (!slash)
    return;
  strcpy_s(slash + 1, sizeof(path) - (slash + 1 - path), "dn_host_loadout.log");
  // _fsopen with _SH_DENYWR, not fopen: the CRT's fopen takes an exclusive lock
  // on Windows, so the file cannot even be COPIED while the host is running.
  // The whole point of this log is that an operator can read it during a match.
  g_logFile = _fsopen(path, "a", _SH_DENYWR);
}

static void Logf(const char *fmt, ...) {
  char buf[1024];
  va_list ap;
  va_start(ap, fmt);
  _vsnprintf_s(buf, sizeof(buf), _TRUNCATE, fmt, ap);
  va_end(ap);

  printf("[dn-host-loadout] %s\n", buf);
  fflush(stdout);
  if (g_logFile) {
    fprintf(g_logFile, "[dn-host-loadout] %s\n", buf);
    fflush(g_logFile);
  }
}

// ---------------------------------------------------------------------------
// Memory safety
//
// This runs inside a live game process with no symbols. Every pointer that came
// from the game is checked before it is followed.
// ---------------------------------------------------------------------------

static bool IsReadable(const void *p, size_t n) {
  if (!p)
    return false;
  MEMORY_BASIC_INFORMATION mbi;
  if (!VirtualQuery(p, &mbi, sizeof(mbi)))
    return false;
  if (mbi.State != MEM_COMMIT)
    return false;
  const DWORD bad = PAGE_NOACCESS | PAGE_GUARD;
  if (mbi.Protect & bad)
    return false;
  const uintptr_t regionEnd = (uintptr_t)mbi.BaseAddress + mbi.RegionSize;
  return (uintptr_t)p + n <= regionEnd;
}

// ---------------------------------------------------------------------------
// Just enough UE4 to find four objects
// ---------------------------------------------------------------------------

static uintptr_t g_base = 0;

#define RVA_FIND_LOADOUT_BY_ID 0x340340
#define RVA_ADD_LOADOUT 0x3382F0
#define RVA_STATIC_LOAD_CLASS 0xD78110
#define OFF_GOBJECTS 0x3F63A70
#define OFF_GNAMES 0x3E069D0

struct FNameMin {
  int32_t ComparisonIndex;
  int32_t Number;
};

struct UObjectMin {
  void *VfTable;        // 0x00
  int32_t Flags;        // 0x08
  int32_t InternalIndex;// 0x0C
  UObjectMin *Class;    // 0x10
  FNameMin Name;        // 0x18
  UObjectMin *Outer;    // 0x20
};

struct FUObjectItemMin {
  UObjectMin *Object;
  int32_t Flags;
  int32_t ClusterIndex;
  int32_t SerialNumber;
  int32_t Pad;
};

struct FUObjectArrayMin {
  FUObjectItemMin *Objects;
  int32_t MaxElements;
  int32_t NumElements;
};

static FUObjectArrayMin *GObjects() {
  return (FUObjectArrayMin *)(g_base + OFF_GOBJECTS);
}

// Name text, for the log only. Mirrors the layout the generated SDK uses for
// this build: a pointer to a chunk table, chunks of 16384 FNameEntry pointers,
// and an FNameEntry whose ANSI text starts at +0x10 (int32 Index, 4 bytes of
// padding, FNameEntry* HashNext). Bit 0 of Index means the entry is wide.
static const char *NameText(const FNameMin &name) {
  const int32_t kElementsPerChunk = 16384;
  int32_t idx = name.ComparisonIndex;
  if (idx < 0)
    return nullptr;

  void ***chunkTablePtr = (void ***)(g_base + OFF_GNAMES);
  if (!IsReadable(chunkTablePtr, sizeof(void *)))
    return nullptr;
  void **chunkTable = (void **)*chunkTablePtr;
  if (!IsReadable(chunkTable + (idx / kElementsPerChunk), sizeof(void *)))
    return nullptr;
  void **chunk = (void **)chunkTable[idx / kElementsPerChunk];
  if (!IsReadable(chunk + (idx % kElementsPerChunk), sizeof(void *)))
    return nullptr;
  uint8_t *entry = (uint8_t *)chunk[idx % kElementsPerChunk];
  if (!IsReadable(entry, 0x20))
    return nullptr;
  if (*(int32_t *)entry & 0x1) // wide -- we only ever compare ASCII asset names
    return nullptr;
  return (const char *)(entry + 0x10);
}

// The class of a UClass is UClass, and the UClass class object is its own
// Class. That fixed point identifies it without needing GNames to be right,
// which is why it is used instead of a name lookup: StaticLoadClass takes this
// pointer, so a wrong answer here would be a wrong call rather than a wrong log
// line.
static UObjectMin *g_uclassClass = nullptr;

static UObjectMin *ResolveUClassClass() {
  if (g_uclassClass)
    return g_uclassClass;

  FUObjectArrayMin *arr = GObjects();
  if (!IsReadable(arr, sizeof(*arr)))
    return nullptr;
  int32_t count = arr->NumElements;
  if (count <= 0 || !IsReadable(arr->Objects, sizeof(FUObjectItemMin)))
    return nullptr;

  for (int32_t i = 0; i < count && i < 4096; ++i) {
    FUObjectItemMin *item = &arr->Objects[i];
    if (!IsReadable(item, sizeof(*item)))
      continue;
    UObjectMin *o = item->Object;
    if (!IsReadable(o, sizeof(*o)))
      continue;
    UObjectMin *c = o->Class;
    if (!IsReadable(c, sizeof(*c)))
      continue;
    UObjectMin *cc = c->Class;
    if (!IsReadable(cc, sizeof(*cc)))
      continue;
    if (cc->Class == cc) { // fixed point: this is UClass
      g_uclassClass = cc;
      const char *n = NameText(cc->Name);
      Logf("resolved UClass class object at %p (%s)", cc, n ? n : "<unnamed>");
      return g_uclassClass;
    }
  }
  Logf("could not resolve the UClass class object; giving up on this pass");
  return nullptr;
}

// Pin an object and its Outer chain into the GC root set, so an asset we loaded
// outside the engine's own reference graph is not collected between the load
// and the spawn. RootSet is bit 30; PendingKill (29) and Unreachable (28) are
// cleared.
static void PinToRootSet(UObjectMin *obj) {
  FUObjectArrayMin *arr = GObjects();
  if (!IsReadable(arr, sizeof(*arr)))
    return;
  for (UObjectMin *cur = obj; cur && IsReadable(cur, sizeof(*cur));
       cur = cur->Outer) {
    int32_t idx = cur->InternalIndex;
    if (idx < 0 || idx >= arr->NumElements)
      break;
    FUObjectItemMin *item = &arr->Objects[idx];
    if (!IsReadable(item, sizeof(*item)) || item->Object != cur)
      break;
    InterlockedOr((volatile LONG *)&item->Flags, (LONG)(1 << 30));
    InterlockedAnd((volatile LONG *)&item->Flags,
                   (LONG)~((1 << 28) | (1 << 29)));
  }
}

typedef void *(*tStaticLoadClass)(void *ObjectClass, void *InOuter,
                                  const wchar_t *InName,
                                  const wchar_t *Filename, int LoadFlags,
                                  void *Sandbox, bool bDoNotReconcile);

// ---------------------------------------------------------------------------
// The four precast loadouts
//
// These are the same four T1 mediums LoadInstallingLadouts would have installed,
// which is the set the client's ship-select screen offers. All four are
// registered, not just the first: an earlier version stopped at the first
// success and every player got the Assault Medium whatever they picked, because
// the other three ids kept missing.
// ---------------------------------------------------------------------------

static const wchar_t *kPrecastPaths[] = {
    L"/Game/Generic/Loadouts/Precast/T1/VH_AssaultMedium_T1_PrecastLoadout_BP."
    L"VH_AssaultMedium_T1_PrecastLoadout_BP_C",
    L"/Game/Generic/Loadouts/Precast/T1/"
    L"VH_DreadnoughtMedium_T1_PrecastLoadout_BP."
    L"VH_DreadnoughtMedium_T1_PrecastLoadout_BP_C",
    L"/Game/Generic/Loadouts/Precast/T1/VH_SniperMedium_T1_PrecastLoadout_BP."
    L"VH_SniperMedium_T1_PrecastLoadout_BP_C",
    L"/Game/Generic/Loadouts/Precast/T1/VH_SupportMedium_T1_PrecastLoadout_BP."
    L"VH_SupportMedium_T1_PrecastLoadout_BP_C",
};

static const char *kPrecastLabels[] = {
    "VH_AssaultMedium_T1",
    "VH_DreadnoughtMedium_T1",
    "VH_SniperMedium_T1",
    "VH_SupportMedium_T1",
};

static const int kPrecastCount =
    (int)(sizeof(kPrecastPaths) / sizeof(kPrecastPaths[0]));

static UObjectMin *g_precastCDO[4] = {};
static int g_precastResolved = 0;
static bool g_precastAttempted = false;

// StaticLoadClass returns the loaded UClass. Its class default object is the
// object whose Class is that UClass and which is not the UClass itself -- at
// resolve time, before any pawn spawns, the CDO is the only such object.
//
// Matching on the pointer rather than on the CDO's name is deliberate: the
// short name "Default__..._C" cannot be matched against UObject::GetFullName,
// which is what an earlier version tried, and it made all four report "could not
// resolve" before we knew whether the load itself had worked.
static UObjectMin *FindCDOForClass(UObjectMin *cls) {
  FUObjectArrayMin *arr = GObjects();
  if (!IsReadable(arr, sizeof(*arr)))
    return nullptr;
  int32_t count = arr->NumElements;
  for (int32_t i = 0; i < count; ++i) {
    FUObjectItemMin *item = &arr->Objects[i];
    if (!IsReadable(item, sizeof(*item)))
      continue;
    UObjectMin *o = item->Object;
    if (!o || o == cls || !IsReadable(o, sizeof(*o)))
      continue;
    if (o->Class == cls)
      return o;
  }
  return nullptr;
}

static void ResolvePrecastLoadouts() {
  if (g_precastAttempted)
    return;
  g_precastAttempted = true;

  UObjectMin *uclassClass = ResolveUClassClass();
  if (!uclassClass) {
    g_precastAttempted = false; // try again on the next miss
    return;
  }

  tStaticLoadClass StaticLoadClass =
      (tStaticLoadClass)(g_base + RVA_STATIC_LOAD_CLASS);

  for (int i = 0; i < kPrecastCount; ++i) {
    UObjectMin *cls = nullptr;
    __try {
      cls = (UObjectMin *)StaticLoadClass(uclassClass, nullptr,
                                          kPrecastPaths[i], nullptr, 0, nullptr,
                                          false);
    } __except (EXCEPTION_EXECUTE_HANDLER) {
      cls = nullptr;
    }

    if (!IsReadable(cls, sizeof(UObjectMin))) {
      Logf("precast %s: StaticLoadClass returned nothing", kPrecastLabels[i]);
      continue;
    }
    PinToRootSet(cls);

    UObjectMin *cdo = FindCDOForClass(cls);
    if (!cdo) {
      Logf("precast %s: class loaded at %p but no default object found",
           kPrecastLabels[i], cls);
      continue;
    }
    PinToRootSet(cdo);

    g_precastCDO[g_precastResolved++] = cdo;
    const char *n = NameText(cdo->Name);
    Logf("precast %s: class=%p cdo=%p (%s)", kPrecastLabels[i], cls, cdo,
         n ? n : "<unnamed>");
  }

  Logf("%d/%d precast loadouts resolved", g_precastResolved, kPrecastCount);
}

// ---------------------------------------------------------------------------
// Registration
// ---------------------------------------------------------------------------

typedef void(__fastcall *tAddLoadout)(void *mgr, void *loadout, uint8_t type);

static bool AddLoadoutGuarded(void *mgr, void *loadout) {
  __try {
    ((tAddLoadout)(g_base + RVA_ADD_LOADOUT))(mgr, loadout, 2);
    return true;
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    return false;
  }
}

static void RegisterPrecastLoadouts(void *mgr) {
  static void *s_registeredFor = nullptr;
  if (!mgr || s_registeredFor == mgr)
    return;
  ResolvePrecastLoadouts();
  if (g_precastResolved == 0)
    return;
  s_registeredFor = mgr;
  for (int i = 0; i < g_precastResolved; ++i) {
    bool ok = AddLoadoutGuarded(mgr, g_precastCDO[i]);
    Logf("register %s with manager %p -> %s", kPrecastLabels[i], mgr,
         ok ? "ok" : "EXCEPTION");
  }
}

// ---------------------------------------------------------------------------
// The hook
// ---------------------------------------------------------------------------

typedef void *(__fastcall *tFindLoadoutByID)(void *mgr, void **id, uint8_t warn);
static tFindLoadoutByID g_origFindLoadoutByID = nullptr;

static void *__fastcall HookFindLoadoutByID(void *mgr, void **id,
                                            uint8_t warn) {
  if (!g_origFindLoadoutByID)
    return nullptr;

  void *found = g_origFindLoadoutByID(mgr, id, warn);

  // The engine answered. Never second-guess it -- this is what keeps the hook
  // correct if a real backend ever populates the manager.
  if (found || !mgr || !id)
    return found;

  // Registering calls back into the manager, which re-enters this hook. The
  // per-manager guard in RegisterPrecastLoadouts already terminates that; this
  // makes the re-entrancy explicit rather than incidental.
  static thread_local bool s_inRetry = false;
  if (s_inRetry)
    return found;
  s_inRetry = true;

  static int s_logged = 0;
  bool verbose = (s_logged++ < 8);

  // Print the id as text as well as hex. The hex alone is not enough to tell a
  // successful lookup from a lookup of the wrong thing: two runs in which the
  // player picked visibly different hulls both logged 0x21F0F, and "FOUND"
  // looks identical either way. The name says which hull was actually asked
  // for, so a wrong hull shows up here rather than on the player's screen.
  uint64_t want = IsReadable(id, 8) ? *(uint64_t *)id : 0ull;
  const char *wantText = nullptr;
  if (IsReadable(id, 8))
    wantText = NameText(*(FNameMin *)id);

  RegisterPrecastLoadouts(mgr);
  void *retry = g_origFindLoadoutByID(mgr, id, 0);

  s_inRetry = false;

  if (verbose)
    Logf("FindLoadoutByID miss for FName 0x%llX (%s) -> after registering: %s",
         (unsigned long long)want, wantText ? wantText : "<unresolved>",
         retry ? "FOUND" : "STILL MISSING");

  return retry;
}

// ---------------------------------------------------------------------------
// Gating and entry point
// ---------------------------------------------------------------------------

// A battle server, not a client. game-manager's spawner is the only thing that
// passes -MatchID=, so its presence identifies a headless host. A player's
// client never has it, which is what makes it safe for this DLL to sit in a
// directory both processes load from.
static bool IsBattleServer() {
  const wchar_t *cmd = GetCommandLineW();
  if (!cmd)
    return false;
  for (const wchar_t *p = cmd; *p; ++p) {
    if ((p[0] == L'-' || p[0] == L'/') && _wcsnicmp(p + 1, L"MatchID", 7) == 0)
      return true;
  }
  return false;
}

// Opt-in on the host. A marker file beside the executable survives however the
// operator starts the service; the environment variable is honoured too because
// dn-dedicated's spawner does inherit its environment (buildEnv, AGENT-CHAT
// S10.5).
static bool IsEnabled() {
  char buf[8];
  DWORD n = GetEnvironmentVariableA("DN_SERVER_LOADOUT", buf, sizeof(buf));
  if (n == 1 && buf[0] == '1')
    return true;

  char path[MAX_PATH];
  if (!GetModuleFileNameA(NULL, path, MAX_PATH))
    return false;
  char *slash = strrchr(path, '\\');
  if (!slash)
    return false;
  strcpy_s(slash + 1, sizeof(path) - (slash + 1 - path),
           "dn_server_loadout.txt");
  return GetFileAttributesA(path) != INVALID_FILE_ATTRIBUTES;
}

static DWORD WINAPI Startup(LPVOID) {
  if (!IsBattleServer())
    return 0; // a client. Do nothing at all, and say nothing.

  LogOpen();

  if (!IsEnabled()) {
    Logf("battle server detected but the fix is off "
         "(create dn_server_loadout.txt beside the executable, or set "
         "DN_SERVER_LOADOUT=1, to enable). Standing down.");
    return 0;
  }

  g_base = (uintptr_t)GetModuleHandleW(L"DreadGame-Win64-Shipping.exe");
  if (!g_base) {
    Logf("could not find DreadGame-Win64-Shipping.exe in this process. "
         "Standing down.");
    return 0;
  }
  Logf("battle server detected and enabled. module base 0x%llX",
       (unsigned long long)g_base);

  if (MH_Initialize() != MH_OK) {
    Logf("MH_Initialize failed. Standing down.");
    return 0;
  }

  void *target = (void *)(g_base + RVA_FIND_LOADOUT_BY_ID);
  if (MH_CreateHook(target, &HookFindLoadoutByID,
                    (LPVOID *)&g_origFindLoadoutByID) != MH_OK ||
      MH_EnableHook(target) != MH_OK) {
    Logf("failed to hook FindLoadoutByID at RVA 0x%X. Standing down.",
         RVA_FIND_LOADOUT_BY_ID);
    return 0;
  }

  Logf("installed: FindLoadoutByID hooked at RVA 0x%X (%p). Waiting for a "
       "loadout lookup.",
       RVA_FIND_LOADOUT_BY_ID, target);
  return 0;
}

BOOL APIENTRY DllMain(HMODULE hModule, DWORD reason, LPVOID) {
  if (reason == DLL_PROCESS_ATTACH) {
    DisableThreadLibraryCalls(hModule);
    CreateThread(NULL, 0, Startup, NULL, 0, NULL);
  }
  return TRUE;
}

// ---------------------------------------------------------------------------
// wer.dll stand-in exports
//
// The game imports these four from wer.dll (Windows Error Reporting) and
// resolves them from its own directory first, which is how this DLL gets loaded
// without an injector. They are no-ops: the engine only calls them while
// writing a crash report, and a host that is writing a crash report has already
// lost the match. See README.md -- this is the deployment mechanism, not part
// of the fix.
// ---------------------------------------------------------------------------

extern "C" __declspec(dllexport) void WerReportAddFile() {}
extern "C" __declspec(dllexport) void WerReportSubmit() {}
extern "C" __declspec(dllexport) void WerReportSetParameter() {}
extern "C" __declspec(dllexport) void WerReportCreate() {}
