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

// Wall-clock stamp on every line. C29 cost us a day: the log recorded the four
// precast resolutions with no times, so an 88-second hole sat in the middle of
// it unnoticed and we published a wrong cause built on top of it. A line that
// cannot be placed in time cannot be used as evidence.
static void LogStamp(char *out, size_t n) {
  SYSTEMTIME st;
  GetLocalTime(&st);
  _snprintf_s(out, n, _TRUNCATE, "%02u:%02u:%02u.%03u", st.wHour, st.wMinute,
              st.wSecond, st.wMilliseconds);
}

static void Logf(const char *fmt, ...) {
  char buf[1024];
  va_list ap;
  va_start(ap, fmt);
  _vsnprintf_s(buf, sizeof(buf), _TRUNCATE, fmt, ap);
  va_end(ap);

  char ts[32];
  LogStamp(ts, sizeof(ts));

  printf("[dn-host-loadout] %s %s\n", ts, buf);
  fflush(stdout);
  if (g_logFile) {
    fprintf(g_logFile, "[dn-host-loadout] %s %s\n", ts, buf);
    fflush(g_logFile);
  }
}

// Milliseconds, for attributing cost to a phase rather than guessing at it.
static double NowMs() {
  LARGE_INTEGER f, c;
  QueryPerformanceFrequency(&f);
  QueryPerformanceCounter(&c);
  return (double)c.QuadPart * 1000.0 / (double)f.QuadPart;
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
// ONE pass over GObjects for all four classes, and no VirtualQuery inside it.
//
// This function used to be called once per precast and used IsReadable -- i.e. a
// VirtualQuery syscall -- twice per object, over an array that holds millions of
// entries once a map is loaded. Four scans x ~2M objects x 2 syscalls is roughly
// 8 million syscalls, and it measured ~21.6 seconds per precast: the whole of
// C29's 90-second game-thread stall, misattributed at the time to
// StaticLoadClass, which the log could not distinguish because it had no
// timestamps.
//
// Per-object SEH replaces the per-object syscall. On x64 __try is table-driven
// and costs nothing when nothing faults, so a bad pointer still cannot take the
// host down -- it just skips that entry instead of aborting the scan.
static int FindCDOsForClasses(UObjectMin **classes, UObjectMin **cdosOut,
                              int count) {
  for (int i = 0; i < count; ++i)
    cdosOut[i] = nullptr;

  FUObjectArrayMin *arr = GObjects();
  if (!IsReadable(arr, sizeof(*arr)))
    return 0;

  int32_t num = arr->NumElements;
  FUObjectItemMin *items = arr->Objects;
  if (num <= 0 || !IsReadable(items, sizeof(FUObjectItemMin)))
    return 0;

  int found = 0;
  for (int32_t i = 0; i < num && found < count; ++i) {
    UObjectMin *o = nullptr;
    UObjectMin *ocls = nullptr;
    __try {
      o = items[i].Object;
      if (o)
        ocls = o->Class;
    } __except (EXCEPTION_EXECUTE_HANDLER) {
      continue;
    }
    if (!o || !ocls)
      continue;

    for (int c = 0; c < count; ++c) {
      if (cdosOut[c] || !classes[c] || o == classes[c] || ocls != classes[c])
        continue;
      // Prefer the class default object explicitly rather than relying on it
      // being the only instance. That held while this ran before any pawn
      // spawned; it stops holding the moment resolution moves earlier or later,
      // and a live instance would be silently registered in its place.
      const char *n = nullptr;
      __try {
        n = NameText(o->Name);
      } __except (EXCEPTION_EXECUTE_HANDLER) {
        n = nullptr;
      }
      if (n && strncmp(n, "Default__", 9) != 0)
        continue;
      cdosOut[c] = o;
      ++found;
      break;
    }
  }
  return found;
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

  // Phase 1: load the four classes. Timed individually, because C29 blamed this
  // call for the whole stall without ever measuring it.
  UObjectMin *classes[4] = {};
  double loadTotal = 0.0;
  for (int i = 0; i < kPrecastCount; ++i) {
    double t0 = NowMs();
    UObjectMin *cls = nullptr;
    __try {
      cls = (UObjectMin *)StaticLoadClass(uclassClass, nullptr,
                                          kPrecastPaths[i], nullptr, 0, nullptr,
                                          false);
    } __except (EXCEPTION_EXECUTE_HANDLER) {
      cls = nullptr;
    }
    double dt = NowMs() - t0;
    loadTotal += dt;

    if (!IsReadable(cls, sizeof(UObjectMin))) {
      Logf("precast %s: StaticLoadClass returned nothing (%.0f ms)",
           kPrecastLabels[i], dt);
      continue;
    }
    PinToRootSet(cls);
    classes[i] = cls;
    Logf("precast %s: class=%p (StaticLoadClass %.0f ms)", kPrecastLabels[i],
         cls, dt);
  }

  // Phase 2: one scan of GObjects for all four default objects.
  UObjectMin *cdos[4] = {};
  double t0 = NowMs();
  int found = FindCDOsForClasses(classes, cdos, kPrecastCount);
  double scanMs = NowMs() - t0;

  for (int i = 0; i < kPrecastCount; ++i) {
    if (!classes[i])
      continue;
    if (!cdos[i]) {
      Logf("precast %s: class loaded at %p but no default object found",
           kPrecastLabels[i], classes[i]);
      continue;
    }
    PinToRootSet(cdos[i]);
    g_precastCDO[g_precastResolved++] = cdos[i];
    const char *n = NameText(cdos[i]->Name);
    Logf("precast %s: cdo=%p (%s)", kPrecastLabels[i], cdos[i],
         n ? n : "<unnamed>");
  }

  Logf("%d/%d precast loadouts resolved "
       "(StaticLoadClass %.0f ms total, one CDO scan %.0f ms, %d/%d matched)",
       g_precastResolved, kPrecastCount, loadTotal, scanMs, found,
       kPrecastCount);
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

// S15.1: this guard was a single pointer, so a listen server -- which has two
// player controllers, the human and the local player 256 -- could alternate
// between two managers and re-register on every alternation. A small set holds
// all of them.
static void *g_registeredFor[8] = {};
static int g_registeredCount = 0;

static bool AlreadyRegistered(void *mgr) {
  for (int i = 0; i < g_registeredCount; ++i)
    if (g_registeredFor[i] == mgr)
      return true;
  return false;
}

static void RegisterPrecastLoadouts(void *mgr) {
  if (!mgr || AlreadyRegistered(mgr))
    return;
  ResolvePrecastLoadouts();
  if (g_precastResolved == 0)
    return;
  if (g_registeredCount < (int)(sizeof(g_registeredFor) / sizeof(void *)))
    g_registeredFor[g_registeredCount++] = mgr;
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
// ---------------------------------------------------------------------------
// PostLogin: put a joining player straight into the arena
//
// OFF by default. This is a much larger behavioural change than the loadout
// registration above, and it removes a step players can see.
//
// Why it exists. Registering the precast loadouts got players a PAWN -- the host
// spawns them and SetYPawn assigns it -- but they still never reach the map,
// because the orbit teleport is gated:
//
//   FUN_3D92A0:  cmp byte ptr [rdx+0x948], 0 ; jne proceed
//                -> "Trying to teleport into level player %s that is not in orbit!"
//
// and 0x948 is AYPlayerReplicationInfo::m_highestFleetUnlocked, an EYFleetType
// from YMmogbrain_Structs.h. It is EYFT_None on a host that never logged in, and
// no payload we can send changes that, because the host holds no mmogbrain data
// (AGENT-CHAT S39, S40).
//
// dread-sdk's server mod does not satisfy that gate. It skips the orbit flow
// entirely: hook PostLogin, set the controller's active loadout, and call the
// engine's own ServerRestartPlayer(), which asks the GameMode for a PlayerStart
// and spawns there. That path never enters UYPlayerOrbitComponent, never reads
// m_highestFleetUnlocked, and never needs the GameState readiness mask. It is a
// proven route -- our operator has played matches with it (S42).
//
// What it costs, stated plainly: the player no longer picks a ship in orbit.
// Everyone spawns in one configured hull. That is a real regression in
// behaviour, and it is why this is opt-in and separate from the loadout fix.
// Prefer the orbit path if it can ever be made to work.
//
// How it hooks. Every reflected call goes through UObject::ProcessEvent, vtable
// index 0x35 on this build (dread-sdk resolves it the same way). Hooking there
// costs one pointer comparison per reflected call: the UFunction objects are
// resolved ONCE at install and the hook compares pointers, never strings.
// ---------------------------------------------------------------------------

#define VF_PROCESS_EVENT 0x35

// UYLoadoutManagerComponent::m_activeLoadout, from the SDK dump
// (DreadGame_Classes.h: "class UYShipLoadout* m_activeLoadout; // 0x0208").
#define OFF_ACTIVE_LOADOUT 0x208

typedef void *(__fastcall *tProcessEvent)(void *object, void *function,
                                          void *params);
static tProcessEvent g_origProcessEvent = nullptr;

static void *g_fnK2PostLogin = nullptr;
static void *g_fnGetLoadoutManager = nullptr;
static void *g_fnServerRestartPlayer = nullptr;
static void *g_fnServerReadyForJoining = nullptr;
static void *g_fnServerSpawnNearActor = nullptr;
static void *g_fnServerPlayerReadyUp = nullptr;
static bool g_postLoginArmed = false;
static bool g_fleetTierArmed = false;
static bool g_spawnArmed = false;

// Which of the four precast loadouts everyone spawns in. Index into
// kPrecastPaths / g_precastCDO; 0 is the Assault Medium T1.
static int g_postLoginLoadoutIndex = 0;

// FindUObjectByName walks GObjects for an object whose FName text CONTAINS the
// wanted text.
//
// Substring, not equality. The first version matched "K2_PostLogin" exactly and
// found nothing on a live host; dread-sdk matches
// GetFullName().find("PostLogin"), which also catches a plain "PostLogin" and
// any Blueprint variant. Equality was a guess about which of those this build
// creates, and it was wrong.
//
// Names are not unique across classes, so the caller gets the FIRST match and
// the outer is logged. A wrong pick shows up in the log rather than silently.
static void *FindUObjectByName(const char *want, const char **outerOut) {
  FUObjectArrayMin *arr = GObjects();
  if (!IsReadable(arr, sizeof(*arr)) || !IsReadable(arr->Objects, sizeof(FUObjectItemMin)))
    return nullptr;

  int count = arr->NumElements;
  if (count < 0 || count > 20000000)
    return nullptr;

  for (int i = 0; i < count; ++i) {
    FUObjectItemMin *item = &arr->Objects[i];
    if (!IsReadable(item, sizeof(*item)))
      continue;
    UObjectMin *obj = item->Object;
    if (!IsReadable(obj, sizeof(*obj)))
      continue;
    const char *text = NameText(obj->Name);
    if (!text || !strstr(text, want))
      continue;
    if (outerOut) {
      *outerOut = nullptr;
      if (IsReadable(obj->Outer, sizeof(UObjectMin)))
        *outerOut = NameText(obj->Outer->Name);
    }
    return obj;
  }
  return nullptr;
}

// AController::PlayerState, from the SDK dump (Engine_Classes.h:797).
#define OFF_PLAYERSTATE 0x3E0
// AYPlayerReplicationInfo::m_highestFleetUnlocked (DreadGame_Classes.h:1920),
// an EYFleetType from YMmogbrain_Structs.h: None=0 Recruit=1 Veteran=2
// Legendary=3.
#define OFF_HIGHEST_FLEET 0x948
#define EYFT_NONE 0
#define EYFT_RECRUIT 1

// EnsureFleetTier gives a player the Recruit tier when the host has none.
//
// This is the byte the orbit teleport is gated on:
//
//   FUN_3D92A0: cmp byte ptr [rdx+0x948], 0 ; jne proceed
//               -> "Trying to teleport into level player %s that is not in orbit!"
//
// It is EYFT_None on a host that never logged in, so nobody is ever teleported
// and the client sits in the orbit screen. Measured, and the reason the
// post-login spawn below is not sufficient on its own: the server spawned FOUR
// pawns for the player and the CLIENT still stayed in orbit, because the only
// thing that takes a client out of orbit is the teleport.
//
// Why Recruit, and why this is not fabrication. The tier is real backend data:
// the engine computes it from the YMmogbrain module (FUN_3A5831, which logs
// "EYFleetType::EYFT_Recruit: no FleetType override - FleetTier=%d") and cannot
// here, because the host holds no mmogbrain data. Recruit is the floor -- what
// a player who owns any fleet at all has unlocked, and every player who reaches
// a battle server owns one. It is the value a logged-in host would have had.
//
// The honest limit: Veteran and Legendary players are under-reported. If a real
// tier ever reaches the host this must defer to it, which is what the guard
// below does.
//
// EVERY path here logs. The first version returned silently on three of them,
// and when the tier did not get written there was no way to tell which -- a
// default indistinguishable from a real result, which is the exact trap
// CONTRIBUTING.md warns about. Capped so a busy match cannot flood the log.
static void EnsureFleetTier(void *pc, const char *where) {
  if (!g_fleetTierArmed)
    return;

  static int s_logged = 0;
  bool verbose = (s_logged++ < 24);

  if (!IsReadable(pc, OFF_PLAYERSTATE + sizeof(void *))) {
    if (verbose)
      Logf("fleet tier [%s]: controller %p not readable to +0x%X", where, pc,
           OFF_PLAYERSTATE);
    return;
  }

  void *ps = *(void **)((uintptr_t)pc + OFF_PLAYERSTATE);
  if (!ps) {
    if (verbose)
      Logf("fleet tier [%s]: controller %p has a NULL PlayerState (+0x%X) -- "
           "too early, the engine has not created it yet",
           where, pc, OFF_PLAYERSTATE);
    return;
  }
  if (!IsReadable(ps, OFF_HIGHEST_FLEET + 1)) {
    if (verbose)
      Logf("fleet tier [%s]: PlayerState %p not readable to +0x%X", where, ps,
           OFF_HIGHEST_FLEET);
    return;
  }

  uint8_t *tier = (uint8_t *)((uintptr_t)ps + OFF_HIGHEST_FLEET);
  uint8_t before = *tier;

  // Never overwrite a value the engine already has. Same rule the
  // FindLoadoutByID hook follows -- if the engine answered, do not second-guess
  // it -- and it is what keeps this correct if a real tier ever arrives.
  if (before != EYFT_NONE) {
    if (verbose)
      Logf("fleet tier [%s]: PlayerState %p already reads %u, leaving it", where,
           ps, (unsigned)before);
    return;
  }

  *tier = EYFT_RECRUIT;

  if (verbose)
    Logf("fleet tier [%s]: PlayerState %p (controller %p) EYFT_None -> "
         "EYFT_Recruit, reads back %u",
         where, ps, pc, (unsigned)*tier);
}

// SpawnJoiningPlayer runs after the engine's own PostLogin has finished.
static void SpawnJoiningPlayer(void *params) {
  if (!IsReadable(params, sizeof(void *)))
    return;

  // AGameMode_K2_PostLogin_Params is a single APlayerController* at +0x00.
  void *pc = *(void **)params;
  if (!IsReadable(pc, 0x200)) {
    Logf("post-login: NewPlayer is not readable, skipping");
    return;
  }

  // The tier first. It is what lets the NORMAL orbit flow finish, and it is
  // useful with or without the spawn below -- which is why the two are
  // separately switchable. Tier alone is the better outcome: the player still
  // picks a ship in orbit.
  //
  // PostLogin may be too early: the engine creates the PlayerState in
  // InitPlayerState, and for a networked join that can land after this. The
  // later trigger points in HookProcessEvent are what actually catch it; this
  // one is kept because when it does work it is the earliest.
  EnsureFleetTier(pc, "PostLogin");

  if (!g_spawnArmed)
    return;

  // GetLoadoutManager() is a UFunction returning UYLoadoutManagerComponent*.
  // Calling it through ProcessEvent avoids needing another hardcoded RVA.
  struct {
    void *ReturnValue;
  } gp = {};
  g_origProcessEvent(pc, g_fnGetLoadoutManager, &gp);

  void *mgr = gp.ReturnValue;
  if (!IsReadable(mgr, OFF_ACTIVE_LOADOUT + sizeof(void *))) {
    Logf("post-login: controller %p has no readable loadout manager, skipping",
         pc);
    return;
  }

  // Same registration the FindLoadoutByID path uses, so the manager holds real
  // loadouts before one is made active. Idempotent per manager.
  RegisterPrecastLoadouts(mgr);
  if (g_precastResolved == 0) {
    Logf("post-login: no precast loadouts resolved, skipping");
    return;
  }

  int idx = g_postLoginLoadoutIndex;
  if (idx < 0 || idx >= g_precastResolved)
    idx = 0;
  void *loadout = g_precastCDO[idx];
  if (!IsReadable(loadout, sizeof(void *))) {
    Logf("post-login: precast %d is not readable, skipping", idx);
    return;
  }

  *(void **)((uintptr_t)mgr + OFF_ACTIVE_LOADOUT) = loadout;

  // The engine's own respawn. It asks the GameMode for a PlayerStart and
  // spawns there -- no orbit, no readiness mask, no fleet tier.
  g_origProcessEvent(pc, g_fnServerRestartPlayer, nullptr);

  Logf("post-login: controller %p -> active loadout %s (%p), ServerRestartPlayer called",
       pc, kPrecastLabels[idx], loadout);
}

static void *__fastcall HookProcessEvent(void *object, void *function,
                                         void *params) {
  void *ret = g_origProcessEvent ? g_origProcessEvent(object, function, params)
                                 : nullptr;

  if (!g_postLoginArmed)
    return ret;

  // Later trigger points, all server RPCs on the PlayerController, all of which
  // route through ProcessEvent and all of which happen long after the
  // PlayerState exists. `object` is the controller.
  //
  // Several rather than one because PostLogin alone did not write the byte on a
  // live host and the silent version could not say why; these bracket the whole
  // orbit sequence, from picking a ship to readying up to the last event before
  // the teleport. Writing twice is free -- the second call sees a non-zero tier
  // and leaves it.
  if (function == g_fnServerReadyForJoining) {
    EnsureFleetTier(object, "ServerReadyForJoining");
    return ret;
  }
  if (function == g_fnServerSpawnNearActor) {
    EnsureFleetTier(object, "ServerSpawnNearActor");
    return ret;
  }
  if (function == g_fnServerPlayerReadyUp) {
    EnsureFleetTier(object, "ServerPlayerReadyUpForMatch");
    return ret;
  }

  // One pointer compare on the hot path. Everything else is behind it.
  if (function != g_fnK2PostLogin)
    return ret;

  // SpawnJoiningPlayer calls ProcessEvent twice, which re-enters this hook.
  // Neither call is K2_PostLogin, so the compare above already stops it; the
  // guard makes that explicit rather than incidental.
  static thread_local bool s_inSpawn = false;
  if (s_inSpawn)
    return ret;
  s_inSpawn = true;
  SpawnJoiningPlayer(params);
  s_inSpawn = false;

  return ret;
}

// Both of the switches below are opt-in separately from the loadout fix,
// because both change what players see.
static DWORD WINAPI PostLoginInstallThread(LPVOID);

// SwitchOn reports whether an env var is "1" or a marker file sits beside the
// executable. Two ways because the spawner inherits its environment but a file
// survives however the operator starts the service.
static bool SwitchOn(const char *envName, const char *markerFile) {
  char buf[8];
  DWORD n = GetEnvironmentVariableA(envName, buf, sizeof(buf));
  if (n == 1 && buf[0] == '1')
    return true;

  char path[MAX_PATH];
  if (!GetModuleFileNameA(NULL, path, MAX_PATH))
    return false;
  char *slash = strrchr(path, '\\');
  if (!slash)
    return false;
  strcpy_s(slash + 1, sizeof(path) - (slash + 1 - path), markerFile);
  return GetFileAttributesA(path) != INVALID_FILE_ATTRIBUTES;
}

// The ServerRestartPlayer bypass: spawns joining players straight into the
// arena, at the cost of removing ship selection.
static bool PostLoginSpawnEnabled() {
  return SwitchOn("DN_HOST_POSTLOGIN_SPAWN", "dn_host_postlogin.txt");
}

// The fleet tier: lets the NORMAL orbit flow complete, keeping ship selection.
// Preferred over the bypass, and useful on its own.
static bool FleetTierEnabled() {
  return SwitchOn("DN_HOST_FLEET_TIER", "dn_host_fleet_tier.txt");
}

// The install runs on its own thread and WAITS, because GObjects is not
// populated when this DLL is attached.
//
// Measured: the first version resolved at DLL_PROCESS_ATTACH and logged
// "K2_PostLogin not found in GObjects" on a live host, while the loadout half of
// this file worked -- because that half resolves lazily, on the first
// FindLoadoutByID miss, by which time the engine is up. dread-sdk sleeps 20
// seconds before its server callbacks for the same reason.
//
// Polling rather than a fixed sleep, so a fast host is not held back and a slow
// one is not cut off. The window is generous: the hook only has to be in place
// before the first player joins, which is many seconds after map load.
#define POSTLOGIN_WAIT_MS 90000
#define POSTLOGIN_POLL_MS 500

static void *WaitForUObjectByName(const char *want, const char **outerOut,
                                  int *waitedMsOut) {
  int waited = 0;
  for (;;) {
    void *found = FindUObjectByName(want, outerOut);
    if (found) {
      if (waitedMsOut)
        *waitedMsOut = waited;
      return found;
    }
    if (waited >= POSTLOGIN_WAIT_MS)
      break;
    Sleep(POSTLOGIN_POLL_MS);
    waited += POSTLOGIN_POLL_MS;
  }
  if (waitedMsOut)
    *waitedMsOut = waited;
  return nullptr;
}

static void InstallPostLoginHook() {
  g_spawnArmed = PostLoginSpawnEnabled();
  g_fleetTierArmed = FleetTierEnabled();

  // The ProcessEvent hook carries both features, so it installs if either is
  // wanted.
  if (!g_spawnArmed && !g_fleetTierArmed) {
    Logf("post-login hook is OFF. Enable the fleet tier "
         "(dn_host_fleet_tier.txt or DN_HOST_FLEET_TIER=1) to let the normal "
         "orbit flow finish, and/or the spawn bypass (dn_host_postlogin.txt or "
         "DN_HOST_POSTLOGIN_SPAWN=1) to skip orbit entirely.");
    return;
  }
  Logf("post-login hook: fleet tier %s, spawn bypass %s",
       g_fleetTierArmed ? "ON" : "off", g_spawnArmed ? "ON" : "off");

  const char *outer = nullptr;
  int waited = 0;
  g_fnK2PostLogin = FindUObjectByName("PostLogin", &outer);
  if (!g_fnK2PostLogin)
    g_fnK2PostLogin = WaitForUObjectByName("PostLogin", &outer, &waited);
  if (!g_fnK2PostLogin) {
    FUObjectArrayMin *arr = GObjects();
    Logf("post-login: no UFunction containing \"PostLogin\" after %d ms "
         "(GObjects reports %d objects). Not hooking.",
         waited,
         IsReadable(arr, sizeof(*arr)) ? arr->NumElements : -1);
    return;
  }
  if (waited)
    Logf("post-login: waited %d ms for GObjects to carry PostLogin", waited);
  Logf("post-login: K2_PostLogin found at %p (outer %s)", g_fnK2PostLogin,
       outer ? outer : "<unknown>");

  // Only the spawn bypass needs these two. The fleet tier writes a byte on the
  // PlayerState and calls nothing, so it must not be blocked by their absence.
  if (g_spawnArmed) {
    g_fnGetLoadoutManager = WaitForUObjectByName("GetLoadoutManager", &outer, nullptr);
    g_fnServerRestartPlayer = WaitForUObjectByName("ServerRestartPlayer", &outer, nullptr);
    if (!g_fnGetLoadoutManager || !g_fnServerRestartPlayer) {
      Logf("post-login: GetLoadoutManager=%p ServerRestartPlayer=%p -- spawn "
           "bypass disabled, continuing with fleet tier only.",
           g_fnGetLoadoutManager, g_fnServerRestartPlayer);
      g_spawnArmed = false;
      if (!g_fleetTierArmed)
        return;
    }
  }

  // The later tier trigger points. All optional: a missing one costs a trigger,
  // not the feature.
  g_fnServerReadyForJoining = FindUObjectByName("ServerReadyForJoining", &outer);
  g_fnServerSpawnNearActor = FindUObjectByName("ServerSpawnNearActor", &outer);
  g_fnServerPlayerReadyUp =
      FindUObjectByName("ServerPlayerReadyUpForMatch", &outer);
  Logf("post-login: tier triggers -- ServerReadyForJoining=%p "
       "ServerSpawnNearActor=%p ServerPlayerReadyUpForMatch=%p",
       g_fnServerReadyForJoining, g_fnServerSpawnNearActor,
       g_fnServerPlayerReadyUp);

  // ProcessEvent is virtual on UObject, so any UObject's vtable has it. The
  // UFunction just resolved is one.
  if (!IsReadable(g_fnK2PostLogin, sizeof(void *))) {
    Logf("post-login: K2_PostLogin object is not readable. Not hooking.");
    return;
  }
  void **vtable = *(void ***)g_fnK2PostLogin;
  if (!IsReadable(vtable, (VF_PROCESS_EVENT + 1) * sizeof(void *))) {
    Logf("post-login: vtable is not readable to index 0x%X. Not hooking.",
         VF_PROCESS_EVENT);
    return;
  }
  void *processEvent = vtable[VF_PROCESS_EVENT];

  if (MH_CreateHook(processEvent, &HookProcessEvent,
                    (LPVOID *)&g_origProcessEvent) != MH_OK ||
      MH_EnableHook(processEvent) != MH_OK) {
    Logf("post-login: failed to hook ProcessEvent at %p. Not hooking.",
         processEvent);
    return;
  }

  g_postLoginArmed = true;
  if (g_spawnArmed)
    Logf("post-login: ProcessEvent hooked at %p; joining players will spawn "
         "directly as %s, bypassing the orbit flow.",
         processEvent, kPrecastLabels[g_postLoginLoadoutIndex]);
  else
    Logf("post-login: ProcessEvent hooked at %p; fleet tier only -- players "
         "keep ship selection and the normal orbit flow.",
         processEvent);
}

static DWORD WINAPI PostLoginInstallThread(LPVOID) {
  InstallPostLoginHook();
  return 0;
}

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

  // On its own thread: InstallPostLoginHook waits for GObjects, and Startup
  // must return so the FindLoadoutByID hook above is live immediately.
  CreateThread(NULL, 0, PostLoginInstallThread, NULL, 0, NULL);
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
