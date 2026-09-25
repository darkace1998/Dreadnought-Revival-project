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

#include <winsock2.h>
#include <ws2tcpip.h>
#include <windows.h>
#pragma comment(lib, "ws2_32.lib")

#include <share.h>
#include <stdarg.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

#include "MinHook.h"
#include "precast_paths.h"

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

// CORRECTED 2026-09-24: the second argument is the PAWN, not the PlayerState
// (the null branch logs "Trying to teleport into level a null YPawn!", and the
// caller passes an IsA-checked element of an array of pawns). This function is
// no longer hooked; see the DISPROVED note above OFF_HIGHEST_FLEET and
// HookGetNetMode. The original analysis follows, kept as a record.
//
// AYOrbitTransitionManager::TeleportPlayerIntoLevel(this, AYPlayerReplicationInfo*)
//
// The function that reads the fleet-tier gate. Verified 2026-08-15 by
// disassembly rather than inference:
//
//   0x1403d92b6  test rdx, rdx
//   0x1403d92b9  jne  0x1403d9303          ; null PRI -> a different message
//   0x1403d9303  cmp  byte ptr [rdx+0x948], 0
//   0x1403d930a  jne  0x1403d9393          ; -> teleport proceeds
//                ; fallthrough logs, at 0x1403d9360:
//                ; "Trying to teleport into level player %s that is not in orbit!"
//
// The format string has TWO identical .rdata copies (0x142edaf90 and
// 0x142edb0a0) and exactly ONE xref, to the SECOND -- the wrong-copy trap in
// CONTRIBUTING.md. Scanning .text for RIP-relative LEAs found it.
//
// rdx is AYPlayerReplicationInfo: +0x948 is m_highestFleetUnlocked in
// dread-sdk's DreadGame_Classes.h:1920, and nothing else in the SDK dump puts
// an EYFleetType there. Only rcx/rdx are read, so the two-argument passthrough
// below is complete -- [rsp+0xC0] at 0x1403d9393 is a STORE into the caller's
// home space, not a stack argument.
// UNetDriver::GetNetMode -- 0x1A5CF60-0x1A5CF8D (.pdata entry, 45 bytes):
//   if (this->IsServer())  return 1 + (GIsClient != 0);   // 1 Dedicated, 2 Listen
//   else                   return 3;                       // Client
// AActor::GetNetMode (0x1726F50) tail-jumps here with the actor's net driver.
// See HookGetNetMode.
#define RVA_NETDRIVER_GET_NET_MODE 0x1A5CF60
#define NM_DEDICATED_SERVER 1
#define NM_LISTEN_SERVER 2

// AYGameMode_Multiplayer's once-a-second timer, 0x36A080-0x36A539 (.pdata
// entry; vtable slot 0x8E0 of every multiplayer mode). Reads only rcx, so the
// one-argument passthrough is complete. See HookGameModeTimer.
#define RVA_GAMEMODE_MP_TIMER 0x36A080
#define OFF_GM_GAMESTATE 0x458        // AGameModeBase -> AGameState*
#define OFF_GM_ENABLE_SPAWN_AI 0x961  // AYGameMode_Multiplayer::m_enableSpawnAI
#define OFF_GS_GAME_MODE_TYPE 0x500   // AYGameState::m_gameModeType
#define YGMT_BOOTCAMP 18              // EYGameModeType -- the proving ground

// UYVehicleMovementComp: 0x5C4EB0-0x5C511E (.pdata entry), one argument (this).
// The only writer of +0x489. See HookVehicleViewCull.
#define RVA_VEHICLE_VIEW_CULL_SETUP 0x5C4EB0

// AYPlayerController::ClientSetPlayerRestrictions_Implementation,
// 0x5743B0-0x5744A0 (.pdata entry; referenced from 4 controller vtables, run by
// the exec thunk 0x746B40). 18 arguments: this, 16 bytes, 1 pointer (read at
// 0x5743BA-0x5744A0). See HookClientSetPlayerRestrictions.
#define RVA_CLIENT_SET_PLAYER_RESTRICTIONS_IMPL 0x5743B0
#define OFF_VMC_LOCALLY_CONTROLLED 0x488  // = owner->IsLocallyControlled()
#define OFF_VMC_VIEW_CULLED 0x489         // "cull my physics by the local camera"
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
// Any precast, on demand
//
// The four T1 mediums above are only the starter fleet. A player who fields a
// researched ship makes the host look up that ship's precast -- verified live
// 2026-09-24: "FindLoadoutByID miss for ... (Default__VH_SniperLight_T2_
// PrecastLoadout_BP_C) -> after registering: STILL MISSING", then "Active
// Loadout not found. Can't spawn" and no pawn. So a miss that the T1 set does not
// answer is resolved by NAME: "Default__<X>_C" -> <X> -> its class path from
// precast_paths.h (generated from the cooked asset list; the path cannot be
// derived, 48 of 102 precasts are outside a Precast/T<n>/ folder) ->
// StaticLoadClass -> its default object -> AddLoadout on the asking manager.
// Same mechanism as the T1 set, one class at a time, cached.
// ---------------------------------------------------------------------------

struct DynPrecast {
  const char *name; // key into kAllPrecastPaths (static storage)
  UObjectMin *cdo;  // nullptr if resolution failed (not retried)
};
static DynPrecast g_dynPrecast[128] = {};
static int g_dynPrecastCount = 0;

struct DynRegistration {
  void *mgr;
  UObjectMin *cdo;
};
static DynRegistration g_dynRegistered[256] = {};
static int g_dynRegisteredCount = 0;

static const PrecastPath *FindPrecastPath(const char *assetName) {
  for (int i = 0; i < kAllPrecastCount; ++i)
    if (strcmp(kAllPrecastPaths[i].name, assetName) == 0)
      return &kAllPrecastPaths[i];
  return nullptr;
}

// "Default__VH_SniperLight_T2_PrecastLoadout_BP_C" -> table entry, or nullptr.
static const PrecastPath *PrecastForLoadoutId(const char *idText) {
  if (!idText || strncmp(idText, "Default__", 9) != 0)
    return nullptr;
  char asset[160];
  size_t n = strlen(idText + 9);
  if (n < 3 || n >= sizeof(asset) || strcmp(idText + 9 + n - 2, "_C") != 0)
    return nullptr;
  memcpy(asset, idText + 9, n - 2);
  asset[n - 2] = 0;
  return FindPrecastPath(asset);
}

static UObjectMin *ResolvePrecastByName(const PrecastPath *pp) {
  for (int i = 0; i < g_dynPrecastCount; ++i)
    if (g_dynPrecast[i].name == pp->name)
      return g_dynPrecast[i].cdo;

  UObjectMin *cdo = nullptr;
  UObjectMin *uclassClass = ResolveUClassClass();
  if (uclassClass) {
    tStaticLoadClass StaticLoadClass =
        (tStaticLoadClass)(g_base + RVA_STATIC_LOAD_CLASS);
    double t0 = NowMs();
    UObjectMin *cls = nullptr;
    __try {
      cls = (UObjectMin *)StaticLoadClass(uclassClass, nullptr, pp->path,
                                          nullptr, 0, nullptr, false);
    } __except (EXCEPTION_EXECUTE_HANDLER) {
      cls = nullptr;
    }
    if (IsReadable(cls, sizeof(UObjectMin))) {
      PinToRootSet(cls);
      UObjectMin *classes[1] = {cls};
      UObjectMin *cdos[1] = {};
      FindCDOsForClasses(classes, cdos, 1);
      cdo = cdos[0];
      if (cdo)
        PinToRootSet(cdo);
    }
    Logf("precast %s (on demand): class=%p cdo=%p (%.0f ms)", pp->name, cls,
         cdo, NowMs() - t0);
  }
  if (g_dynPrecastCount < (int)(sizeof(g_dynPrecast) / sizeof(g_dynPrecast[0])))
    g_dynPrecast[g_dynPrecastCount++] = {pp->name, cdo};
  return cdo;
}

// Registers the named precast with this manager if the name is a known precast.
// Returns true if something new was registered.
static bool RegisterPrecastOnDemand(void *mgr, const char *idText) {
  const PrecastPath *pp = PrecastForLoadoutId(idText);
  if (!pp)
    return false;
  UObjectMin *cdo = ResolvePrecastByName(pp);
  if (!cdo)
    return false;
  for (int i = 0; i < g_dynRegisteredCount; ++i)
    if (g_dynRegistered[i].mgr == mgr && g_dynRegistered[i].cdo == cdo)
      return false;
  if (g_dynRegisteredCount <
      (int)(sizeof(g_dynRegistered) / sizeof(g_dynRegistered[0])))
    g_dynRegistered[g_dynRegisteredCount++] = {mgr, cdo};
  bool ok = AddLoadoutGuarded(mgr, cdo);
  Logf("register %s with manager %p -> %s", pp->name, mgr,
       ok ? "ok" : "EXCEPTION");
  return ok;
}

// ---------------------------------------------------------------------------
// The hook
// ---------------------------------------------------------------------------

typedef void *(__fastcall *tFindLoadoutByID)(void *mgr, void **id, uint8_t warn);
static tFindLoadoutByID g_origFindLoadoutByID = nullptr;


// ---------------------------------------------------------------------------
// The player's own fit (dn_host_player_loadouts.txt)
//
// Verified 2026-09-25 from the exe: a client tells the host only WHICH loadout
// it picked (ServerPlayerClickedShipLoadout(FName id)), and the host cannot
// look the fit up itself -- its mmog AutoLogin (0x2AABCB0) is a stub and its
// fleet manager (0x35FDF0) only reads the host's own account. The original
// servers were a separate build. So:
//
//   1. mmogbrain adds ?DNPID=<pid> to the YA_Connect travel address; the host
//      keeps the login URL at UNetConnection+0x198 (verified live), reached
//      via loadoutManager+0xA8 (owner controller) -> +0x5A8 (NetConnection).
//   2. The mod asks mmogbrain (GET /battle/loadout?pid=&id=, loopback) for the
//      record the client built its own loadout from.
//   3. It builds the loadout exactly as the client does in
//      HandleMmogbrainLoadoutAdded (0x348830, verified):
//        obj = StaticConstructObject(UYShipLoadout::StaticClass() 0x614E30,
//                                    outer = manager)             0xD759E0
//        FYShipImportLoadoutInfo from the record                  (0x34E550)
//        0x34D690(obj, *(owner+0x970), &info)
//        obj->m_cachedInitializationData (+0x38..) = info
//        obj+0x1B1 = obj+0x1B2 = 1
//        if !FindLoadoutByID(mgr, &obj->m_id) -> AddLoadout(mgr, obj, 2)
//
// The client picks by the precast's name, so registering a default precast
// first would shadow the player's fit: this runs BEFORE any precast fallback.
// ---------------------------------------------------------------------------

#define RVA_YSHIPLOADOUT_STATICCLASS 0x614E30
#define RVA_STATIC_CONSTRUCT_OBJECT 0xD759E0
#define RVA_LOADOUT_INIT_FROM_INFO 0x34D690
#define RVA_FSTRING_ASSIGN 0x21F5E0
#define RVA_TARRAY_INT_ASSIGN 0x1CF3240
#define RVA_FNAME_CTOR_WIDE 0xC9CF20
#define OFF_COMPONENT_OWNER 0xA8
#define OFF_PC_NETCONNECTION 0x5A8
#define OFF_NETCONN_REQUEST_URL 0x198
#define OFF_OWNER_INIT_ARG 0x970

struct FStringMin {
  wchar_t *data;
  int32_t num; // characters including the terminator
  int32_t max;
};
struct TArrayIntMin {
  int32_t *data;
  int32_t num;
  int32_t max;
};
// FYShipImportLoadoutInfo, 0x70 bytes (dread-sdk DreadGame_Structs.h:4634).
struct ShipImportInfoMin {
  FNameMin loadoutID;  // 0x00
  FNameMin pid;        // 0x08
  int32_t precastID;   // 0x10
  int32_t pad14;       // 0x14
  FStringMin name;     // 0x18
  int32_t shipClass;   // 0x28
  int32_t pad2c;       // 0x2C
  FStringMin display;  // 0x30
  TArrayIntMin weapons;   // 0x40
  TArrayIntMin abilities; // 0x50
  TArrayIntMin perks;     // 0x60
};
static_assert(sizeof(ShipImportInfoMin) == 0x70, "FYShipImportLoadoutInfo size");

typedef void *(__fastcall *tStaticClass)();
typedef void *(__fastcall *tStaticConstructObject)(void *cls, void *outer,
                                                   uint64_t name, uint32_t flags,
                                                   uint32_t internalFlags,
                                                   void *tmpl, bool copyTransients,
                                                   void *instanceGraph);
typedef void(__fastcall *tLoadoutInitFromInfo)(void *obj, void *ownerArg,
                                               ShipImportInfoMin *info);
typedef void(__fastcall *tAssign)(void *dst, const void *src);
typedef FNameMin *(__fastcall *tFNameCtor)(FNameMin *out, const wchar_t *text,
                                           int findType);

static bool PlayerLoadoutsEnabled();

// DNPID from the owning connection's login URL, or "" (the host's local
// player 256 has no connection).
static bool PlayerPIDForManager(void *mgr, char *out, size_t outLen) {
  out[0] = 0;
  __try {
    uint8_t *owner = *(uint8_t **)((uint8_t *)mgr + OFF_COMPONENT_OWNER);
    if (!IsReadable(owner, OFF_PC_NETCONNECTION + 8))
      return false;
    uint8_t *conn = *(uint8_t **)(owner + OFF_PC_NETCONNECTION);
    if (!IsReadable(conn, OFF_NETCONN_REQUEST_URL + sizeof(FStringMin)))
      return false;
    FStringMin *url = (FStringMin *)(conn + OFF_NETCONN_REQUEST_URL);
    if (!url->data || url->num <= 0 || url->num > 4096 ||
        !IsReadable(url->data, (size_t)url->num * 2))
      return false;
    const wchar_t *p = wcsstr(url->data, L"DNPID=");
    if (!p)
      return false;
    p += 6;
    size_t n = 0;
    while (p[n] && p[n] != L'?' && p[n] != L'&' && n + 1 < outLen) {
      out[n] = (char)p[n];
      ++n;
    }
    out[n] = 0;
    return n > 0;
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    return false;
  }
}

// Minimal HTTP/1.0 GET on loopback. Returns the body, or "" on any failure.
static bool HttpGetLoopback(const char *pathAndQuery, char *body, size_t bodyLen) {
  body[0] = 0;
  static bool s_wsa = false;
  if (!s_wsa) {
    WSADATA wd;
    if (WSAStartup(MAKEWORD(2, 2), &wd) != 0)
      return false;
    s_wsa = true;
  }
  char host[64] = "127.0.0.1";
  int port = 8083;
  char env[80];
  DWORD n = GetEnvironmentVariableA("DN_MMOG_HTTP", env, sizeof(env));
  if (n > 0 && n < sizeof(env)) { // "host:port"
    char *colon = strrchr(env, ':');
    if (colon) {
      *colon = 0;
      strncpy_s(host, sizeof(host), env, _TRUNCATE);
      port = atoi(colon + 1);
    }
  }
  SOCKET s = socket(AF_INET, SOCK_STREAM, IPPROTO_TCP);
  if (s == INVALID_SOCKET)
    return false;
  DWORD timeoutMs = 2000;
  setsockopt(s, SOL_SOCKET, SO_RCVTIMEO, (const char *)&timeoutMs, sizeof(timeoutMs));
  setsockopt(s, SOL_SOCKET, SO_SNDTIMEO, (const char *)&timeoutMs, sizeof(timeoutMs));
  sockaddr_in a = {};
  a.sin_family = AF_INET;
  a.sin_port = htons((u_short)port);
  inet_pton(AF_INET, host, &a.sin_addr);
  if (connect(s, (sockaddr *)&a, sizeof(a)) != 0) {
    closesocket(s);
    return false;
  }
  char req[768];
  int rl = _snprintf_s(req, sizeof(req), _TRUNCATE,
                       "GET %s HTTP/1.0\r\nHost: %s\r\nConnection: close\r\n\r\n",
                       pathAndQuery, host);
  if (rl <= 0 || send(s, req, rl, 0) != rl) {
    closesocket(s);
    return false;
  }
  static char buf[8192];
  int total = 0, got;
  while (total < (int)sizeof(buf) - 1 &&
         (got = recv(s, buf + total, (int)sizeof(buf) - 1 - total, 0)) > 0)
    total += got;
  closesocket(s);
  buf[total] = 0;
  if (strncmp(buf, "HTTP/1.", 7) != 0 || strncmp(buf + 9, "200", 3) != 0)
    return false;
  const char *b = strstr(buf, "\r\n\r\n");
  if (!b)
    return false;
  strncpy_s(body, bodyLen, b + 4, _TRUNCATE);
  return true;
}

static const char *FieldValue(const char *body, const char *key, char *out, size_t outLen) {
  size_t kl = strlen(key);
  for (const char *line = body; line && *line;) {
    const char *end = strchr(line, '\n');
    size_t len = end ? (size_t)(end - line) : strlen(line);
    if (len > kl && strncmp(line, key, kl) == 0 && line[kl] == '=') {
      size_t vl = len - kl - 1;
      if (vl >= outLen)
        vl = outLen - 1;
      memcpy(out, line + kl + 1, vl);
      out[vl] = 0;
      return out;
    }
    line = end ? end + 1 : nullptr;
  }
  out[0] = 0;
  return nullptr;
}

static int ParseInts(const char *csv, int32_t *out, int maxN) {
  int n = 0;
  while (csv && *csv && n < maxN) {
    out[n++] = (int32_t)strtol(csv, nullptr, 10);
    const char *c = strchr(csv, ',');
    csv = c ? c + 1 : nullptr;
  }
  return n;
}

static bool RegisterPlayerLoadout(void *mgr, void **id, const char *idText) {
  if (!PlayerLoadoutsEnabled() || !idText)
    return false;
  char pid[80];
  if (!PlayerPIDForManager(mgr, pid, sizeof(pid)))
    return false; // not a connected player (e.g. the host's local player)

  char path[512], body[4096];
  _snprintf_s(path, sizeof(path), _TRUNCATE, "/battle/loadout?pid=%s&id=%s", pid, idText);
  if (!HttpGetLoopback(path, body, sizeof(body))) {
    Logf("player loadout: %s for %s -- mmogbrain has no record (or unreachable); falling back to the default precast",
         idText, pid);
    return false;
  }

  char v[512];
  static wchar_t nameW[128], displayW[512];
  ShipImportInfoMin info = {};
  info.loadoutID = *(FNameMin *)id;
  static wchar_t pidW[80];
  MultiByteToWideChar(CP_UTF8, 0, pid, -1, pidW, 80);
  ((tFNameCtor)(g_base + RVA_FNAME_CTOR_WIDE))(&info.pid, pidW, 1 /* FNAME_Add */);
  info.precastID = (int32_t)strtol(FieldValue(body, "precast", v, sizeof(v)) ? v : "0", nullptr, 10);
  info.shipClass = (int32_t)strtol(FieldValue(body, "class", v, sizeof(v)) ? v : "0", nullptr, 10);
  FieldValue(body, "name", v, sizeof(v));
  int nl = MultiByteToWideChar(CP_UTF8, 0, v, -1, nameW, 128);
  info.name = {nameW, nl > 0 ? nl : 1, 128};
  FieldValue(body, "display", v, sizeof(v));
  int dl = MultiByteToWideChar(CP_UTF8, 0, v, -1, displayW, 512);
  info.display = {displayW, dl > 0 ? dl : 1, 512};
  // Same shapes 0x34E550 builds: 3 weapon entries (2 slots + a trailing 0),
  // 4 abilities, 4 perks, positional.
  static int32_t weapons[3], abilities[4], perks[4];
  memset(weapons, 0, sizeof(weapons));
  memset(abilities, 0, sizeof(abilities));
  memset(perks, 0, sizeof(perks));
  ParseInts(FieldValue(body, "weapons", v, sizeof(v)), weapons, 2);
  ParseInts(FieldValue(body, "abilities", v, sizeof(v)), abilities, 4);
  ParseInts(FieldValue(body, "perks", v, sizeof(v)), perks, 4);
  info.weapons = {weapons, 3, 3};
  info.abilities = {abilities, 4, 4};
  info.perks = {perks, 4, 4};

  uint8_t *obj = nullptr;
  __try {
    void *cls = ((tStaticClass)(g_base + RVA_YSHIPLOADOUT_STATICCLASS))();
    obj = (uint8_t *)((tStaticConstructObject)(g_base + RVA_STATIC_CONSTRUCT_OBJECT))(
        cls, mgr, 0, 0, 0, nullptr, false, nullptr);
    if (!obj)
      return false;
    uint8_t *owner = *(uint8_t **)((uint8_t *)mgr + OFF_COMPONENT_OWNER);
    void *ownerArg = IsReadable(owner, OFF_OWNER_INIT_ARG + 8)
                         ? *(void **)(owner + OFF_OWNER_INIT_ARG) : nullptr;
    ((tLoadoutInitFromInfo)(g_base + RVA_LOADOUT_INIT_FROM_INFO))(obj, ownerArg, &info);
    // m_cachedInitializationData, copied field by field as the client does
    // (engine allocations for the strings and arrays; ours stay ours).
    tAssign fstr = (tAssign)(g_base + RVA_FSTRING_ASSIGN);
    tAssign tarr = (tAssign)(g_base + RVA_TARRAY_INT_ASSIGN);
    *(FNameMin *)(obj + 0x38) = info.loadoutID;
    *(FNameMin *)(obj + 0x40) = info.pid;
    *(int32_t *)(obj + 0x48) = info.precastID;
    fstr(obj + 0x50, &info.name);
    *(int32_t *)(obj + 0x60) = info.shipClass;
    fstr(obj + 0x68, &info.display);
    tarr(obj + 0x78, &info.weapons);
    tarr(obj + 0x88, &info.abilities);
    tarr(obj + 0x98, &info.perks);
    *(uint16_t *)(obj + 0x1B1) = 0x0101;
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    Logf("player loadout: EXCEPTION building %s for %s", idText, pid);
    return false;
  }

  if (g_origFindLoadoutByID(mgr, (void **)(obj + 0xB0), 0))
    return true; // already there (the id matched an existing entry)
  bool ok = AddLoadoutGuarded(mgr, obj);
  Logf("player loadout: %s for %s -> precast %d, weapons %d/%d, abilities %d/%d/%d/%d -> %s",
       idText, pid, info.precastID, weapons[0], weapons[1], abilities[0],
       abilities[1], abilities[2], abilities[3], ok ? "registered" : "EXCEPTION");
  return ok;
}

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

  // Order matters: the client picks by the precast's name, so the player's
  // own fit must be registered before any default precast could shadow it.
  void *retry = nullptr;
  if (RegisterPlayerLoadout(mgr, id, wantText))
    retry = g_origFindLoadoutByID(mgr, id, 0);
  // Otherwise that ship's default precast, on demand...
  if (!retry && RegisterPrecastOnDemand(mgr, wantText))
    retry = g_origFindLoadoutByID(mgr, id, 0);
  // ...and the original four-T1 set as the last resort.
  if (!retry) {
    RegisterPrecastLoadouts(mgr);
    retry = g_origFindLoadoutByID(mgr, id, 0);
  }

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
// DISPROVED 2026-09-24 -- read before using anything in this block.
//
// The orbit gate does NOT read the PlayerState. TeleportPlayerIntoLevel
// (0x3D92A0) is called from TeleportPlayersFromOrbit (call at 0x3838D1) with an
// element of a weak-pointer array that passed an IsA check, and its null branch
// logs "Trying to teleport into level a null YPawn!" -- the object is the PAWN,
// and +0x948 on AYPawn is a native, unreflected field (the SDK has no member
// there), i.e. the pawn's own in-orbit state. The SDK's
// AYPlayerReplicationInfo::m_highestFleetUnlocked shares the offset by
// coincidence; the real PlayerState constructor (0x5A8820, reached from the
// class thunk at 0x5CE5D0) already sets it to Recruit.
//
// So the TeleportPlayerIntoLevel write this block fed forced the pawn's orbit
// flag -- the "fake the gate" the repo rule warns about, and the operator
// rejected it. The real cause is the net mode: see HookGetNetMode. The fleet
// tier feature is therefore disabled (g_fleetTierArmed is never set) and kept
// only as a record.
//
// Original text: AYPlayerReplicationInfo::m_highestFleetUnlocked
// (DreadGame_Classes.h:1920), an EYFleetType from YMmogbrain_Structs.h:
// None=0 Recruit=1 Veteran=2 Legendary=3.
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
// EnsureFleetTierOnPlayerState is the half that does the work, split out
// because the teleport hook already HAS the PlayerState -- it is the argument
// the gate is read from -- and must not go looking for one via a controller.
static void EnsureFleetTierOnPlayerState(void *ps, const char *where,
                                         bool verbose) {
  if (!ps) {
    if (verbose)
      Logf("fleet tier [%s]: NULL PlayerState", where);
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
    Logf("fleet tier [%s]: PlayerState %p EYFT_None -> EYFT_Recruit, reads "
         "back %u",
         where, ps, (unsigned)*tier);
}

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
  EnsureFleetTierOnPlayerState(ps, where, verbose);
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

// ---------------------------------------------------------------------------
// Dedicated net mode: let the game's own dedicated-server path run the orbit
//
// Why players never leave orbit (verified 2026-09-24 by disassembly and the
// full battle-server log run/battle-logs/battle-20260923-204528-port7777.log):
//
//   - The teleport into the arena needs the pawn's in-orbit state, which the
//     orbit sequence sets only once the GameState readiness mask is complete
//     (AYGameState_MP+0x1D60 == 0xF, AGENT-CHAT S39).
//   - On a DEDICATED server the game completes that mask itself.
//     0x3ACA40, called from ServerReadyForJoining (0x592350) and
//     ClientLoadingCompleted (0x390970), does:
//         if Role == Authority:
//             if GetNetMode() == NM_DedicatedServer: set bits 0x1|0x2|0x4|0x8
//         set bit 0x10
//         if Role == Authority: StartOrbitTransition
//   - Our host is the shipping GAME exe, which is never dedicated: engine
//     PreInit sets GIsClient = 1 on every non-commandlet launch (0x228F71),
//     whatever the command line says -- which is why "-server" and dropping it
//     changed nothing (S43). So GetNetMode() answers 2 (listen), the mask stops
//     at 0x2 (only OrbitLevelReady), and TeleportPlayersFromOrbit logs "not in
//     orbit" for every player. The log shows ServerReadyForJoining and
//     StartOrbitTransition running, and none of the other three bits.
//
// The fix: a server net driver reports DEDICATED instead of LISTEN. Nothing is
// forced; the engine then takes the path the shipping servers took. Clients
// are untouched (their net driver is not a server, the original answers 3),
// and GIsClient itself is left alone, so client-only systems in this process
// keep the state they initialised with.
//
// Calls the original first and only changes a LISTEN answer, per the hooking
// checklist. Logs the first rewrites so the effect is visible.
typedef int(__fastcall *tGetNetMode)(void *netDriver);
static tGetNetMode g_origGetNetMode = nullptr;

static int __fastcall HookGetNetMode(void *netDriver) {
  int mode = g_origGetNetMode(netDriver);
  if (mode != NM_LISTEN_SERVER)
    return mode;
  static volatile LONG s_logged = 0;
  if (InterlockedIncrement(&s_logged) <= 3)
    Logf("net mode: net driver %p answered LISTEN (2); reporting DEDICATED (1) "
         "so the game's dedicated-server orbit path runs",
         netDriver);
  return NM_DEDICATED_SERVER;
}

// ---------------------------------------------------------------------------
// AI in the proving ground (dn_host_bc_ai.txt)
//
// Verified 2026-09-24 (disassembly + live memory of a BC host):
//   - Every NPC spawn path ends in game-mode virtual 0x9D0 (0x3678F0), which
//     fills the teams (0x381FA0 -> per-NPC 0x3671C0 -> StartCombat) only if
//     GameMode+0x961 m_enableSpawnAI or +0x962 is set. +0x962 is written only by
//     the cheat-manager commands SpawnAI / SpawnAITeams (0x32AC10, 0x32AD90).
//   - For the proving ground the natural caller is this timer, at 0x36A806:
//     m_enableSpawnAI && !GameState.m_isMatchStarted && m_remainingTime <= 50.
//   - Native constructors: Multiplayer 0x361DF0 writes 0x961 = 0, TrainingMatch
//     0x362840 writes 1. GameInfo_BC_BP does not override it, so on our host it
//     read 0 with 45 NPC entries loaded and nothing ever spawned.
//   - Writing the byte live (scratchpad bcpoke.py) made the game fill both teams
//     at the 50 s mark (T1 7 + the player, T2 8) and log "StartCombat - starting
//     combat"; the operator fought the bots.
//
// So this sets the designer switch the mode's own AI data is waiting for; the
// game does the rest. Limited to Bootcamp: PvP modes have no bots by design.
typedef void(__fastcall *tGameModeTimer)(void *gameMode);
static tGameModeTimer g_origGameModeTimer = nullptr;

static void __fastcall HookGameModeTimer(void *gameMode) {
  uint8_t *gm = (uint8_t *)gameMode;
  if (IsReadable(gm + OFF_GM_ENABLE_SPAWN_AI, 1) &&
      gm[OFF_GM_ENABLE_SPAWN_AI] == 0 &&
      IsReadable(gm + OFF_GM_GAMESTATE, sizeof(void *))) {
    uint8_t *gs = *(uint8_t **)(gm + OFF_GM_GAMESTATE);
    if (gs && IsReadable(gs + OFF_GS_GAME_MODE_TYPE, 1) &&
        gs[OFF_GS_GAME_MODE_TYPE] == YGMT_BOOTCAMP) {
      gm[OFF_GM_ENABLE_SPAWN_AI] = 1;
      Logf("bc ai: game mode %p is Bootcamp; m_enableSpawnAI 0 -> 1. The game "
           "fills both teams when the pre-match countdown reaches 50 s.",
           gameMode);
    }
  }
  g_origGameModeTimer(gameMode);
}

// ---------------------------------------------------------------------------
// Player ship physics (dn_host_ship_physics.txt)
//
// Verified 2026-09-24 (disassembly + live memory while the operator flew):
//   - Ship inputs reach the host (ServerUpdateThrottle/Steering/VerticalState
//     -> m_replicatedState +0x210, copied to +0x228.. for non-local ships at
//     0x5C92E5): the host read throttle 1.0 / steering -1.0 as pressed.
//   - The force builder 0x5C8C00 skips ALL forces for a ship that is not
//     locally controlled (+0x488 = owner->IsLocallyControlled(), set at
//     0x5C4910 -- false for every human player on any server) when +0x489 is
//     set, unless the ship is near and in front of the LOCAL player's camera
//     (+0x498 distance squared, +0x49C view dot, cvar via 0x5B9B40). A client
//     optimisation: don't simulate remote ships nobody is looking at.
//   - +0x489 is written only here, and only when the world has a first local
//     player controller. A real dedicated server has none, so every ship is
//     simulated. Our host is the game exe and has one (player 256, parked at the
//     orbit camera), so player ships ~73,000 units away were never simulated:
//     the host copy only drifted, and every server correction (e.g. on firing)
//     snapped the client back -- "position resets and I can't move".
//   - Clearing +0x489 live (scratchpad fixnow.py) made the host copy follow the
//     inputs at once: accelerate, turn, climb; the operator confirmed no resets.
//
// So after the original runs, the flag is cleared again: the host behaves like
// the dedicated server it stands in for. Locally controlled ships (AI) return
// early in the original and are untouched.
typedef void(__fastcall *tVehicleViewCull)(void *movementComp);
static tVehicleViewCull g_origVehicleViewCull = nullptr;

static void __fastcall HookVehicleViewCull(void *movementComp) {
  g_origVehicleViewCull(movementComp);
  uint8_t *mc = (uint8_t *)movementComp;
  if (mc[OFF_VMC_LOCALLY_CONTROLLED] == 0 && mc[OFF_VMC_VIEW_CULLED] != 0) {
    mc[OFF_VMC_VIEW_CULLED] = 0;
    static volatile LONG s_logged = 0;
    if (InterlockedIncrement(&s_logged) <= 8)
      Logf("ship physics: movement component %p is a remote player's ship; "
           "cleared the local-camera physics cull so the host simulates it",
           movementComp);
  }
}

// ---------------------------------------------------------------------------
// Stack overflow at match end (always on)
//
// Verified 2026-09-24 by scraping the overflowed game-thread stack of a host
// that died as a proving-ground match was won (Wine: EXCEPTION_STACK_OVERFLOW
// c00000fd): one 12-frame loop repeated ~3,884 times --
//   exec ClientSetPlayerRestrictions (0x746B40) -> _Implementation (0x5743B0)
//   -> SetPlayerRestrictions (0x5954F0) -> ClientSetPlayerRestrictions RPC
//   (0x5E5B80) -> ProcessEvent ... -> exec 0x746B40 -> ...
// 0x5954F0 applies the restrictions locally only if the world's net mode is
// Standalone/Client (0x1CDB7C0 -> UNetDriver::GetNetMode) or world+0x840 == 3;
// on any server it sends the client RPC instead. For a remote player that RPC
// goes over the wire and the loop never forms. Our host also has a LOCAL
// player (256, the game exe cannot run without one), and a client RPC on a
// local controller executes in-process -- straight back into 0x5954F0, which
// sends it again. The shipping servers had no local player, so this never
// fired there. The same applies whether the host reports LISTEN or DEDICATED.
//
// The guard lets the first call through and drops a nested one on the same
// thread: nesting here can only be that local loop. Remote players are
// unaffected (their RPC is sent, not executed here).
typedef void(__fastcall *tClientSetPlayerRestrictions)(
    void *, uint64_t, uint64_t, uint64_t, uint64_t, uint64_t, uint64_t,
    uint64_t, uint64_t, uint64_t, uint64_t, uint64_t, uint64_t, uint64_t,
    uint64_t, uint64_t, uint64_t, uint64_t);
static tClientSetPlayerRestrictions g_origClientSetPlayerRestrictions = nullptr;

static void __fastcall HookClientSetPlayerRestrictions(
    void *pc, uint64_t a2, uint64_t a3, uint64_t a4, uint64_t a5, uint64_t a6,
    uint64_t a7, uint64_t a8, uint64_t a9, uint64_t a10, uint64_t a11,
    uint64_t a12, uint64_t a13, uint64_t a14, uint64_t a15, uint64_t a16,
    uint64_t a17, uint64_t a18) {
  static thread_local int s_depth = 0;
  if (s_depth > 0) {
    static volatile LONG s_logged = 0;
    if (InterlockedIncrement(&s_logged) <= 3)
      Logf("restrictions: dropped a re-entrant ClientSetPlayerRestrictions on "
           "controller %p (the local player's RPC loop that overflowed the "
           "stack at match end)",
           pc);
    return;
  }
  ++s_depth;
  g_origClientSetPlayerRestrictions(pc, a2, a3, a4, a5, a6, a7, a8, a9, a10,
                                    a11, a12, a13, a14, a15, a16, a17, a18);
  --s_depth;
}

// ---------------------------------------------------------------------------
// End-of-match screen (on unless dn_host_no_eom_stats.txt / DN_HOST_NO_EOM_STATS=1)
//
// Verified 2026-09-25 from a client log + the exe: the client's end-of-match
// stage graph runs Init -> FadeToBlack -> UnloadSublevels -> LoadLevel ->
// LoadContent -> SetupUIWidgets and stops there, on a black screen. That stage
// (0x34B7E0) reads PC+0x3E0 (the AYPlayerReplicationInfo) and waits for
// PRI+0x7E0: if clear it binds to the PRI+0x5E0 delegate and waits; once set it
// moves on to stage 7. The only writer of +0x7E0 is 0x5B0240, the
// ClientSetTopPlayerMatchStats(TArray<FYPlayerMatchStat>) body (exec thunk
// 0x747180, vtable slot 0x6A8), which copies the array to PRI+0x7D0, sets the
// flag and broadcasts.
//
// Nothing in this exe SENDS that RPC: the FName global it would be called by
// (0x3E102F8) is referenced only by its own startup initializer (full .text
// scan). The server code that ranked players at match end lived in the
// separate server build, like the loadout lookup. So the mod sends it, at the
// moment the host starts a player's end-of-match flow: right after the
// ClientStartEndOfMatchTransition RPC stub (0x5E5D70, UYPlayerOrbitComponent,
// the only sender; FindFunctionChecked 0xD57C90 + ProcessEvent vtable 0x1A8,
// read from that stub), on the same controller's PRI.
//
// The array is sent EMPTY: the handler only copy-assigns it (0x410B60), and
// ranking stats is data the host does not have. The MVP page shows no
// entries rather than invented ones.
#define RVA_CLIENT_START_EOM_TRANSITION 0x5E5D70
#define RVA_FIND_FUNCTION_CHECKED 0xD57C90
#define RVA_FNAME_CLIENT_SET_TOP_PLAYER_MATCH_STATS 0x3E102F8
#define OFF_PC_ORBIT_COMPONENT 0xBE8 // AYPlayerController::m_orbitComponent
#define OFF_PC_PLAYER_STATE 0x3E0
#define VT_PROCESS_EVENT 0x1A8

typedef void(__fastcall *tClientStartEomTransition)(void *orbitComp);
static tClientStartEomTransition g_origClientStartEomTransition = nullptr;
typedef void *(__fastcall *tFindFunctionChecked)(void *obj, uint64_t name);
typedef void(__fastcall *tProcessEventVirt)(void *obj, void *fn, void *parms);

static void __fastcall HookClientStartEomTransition(void *orbitComp) {
  g_origClientStartEomTransition(orbitComp);
  __try {
    uint8_t *pc = *(uint8_t **)((uint8_t *)orbitComp + OFF_COMPONENT_OWNER);
    if (!IsReadable(pc, OFF_PC_ORBIT_COMPONENT + 8) ||
        *(void **)(pc + OFF_PC_ORBIT_COMPONENT) != orbitComp) {
      Logf("eom stats: orbit component %p has no matching controller; not sent",
           orbitComp);
      return;
    }
    if (!*(void **)(pc + OFF_PC_NETCONNECTION))
      return; // the host's own local player; its flow is not what anyone sees
    uint8_t *pri = *(uint8_t **)(pc + OFF_PC_PLAYER_STATE);
    if (!IsReadable(pri, 0x800)) {
      Logf("eom stats: controller %p has no player state; not sent", pc);
      return;
    }
    uint64_t name = *(uint64_t *)(g_base + RVA_FNAME_CLIENT_SET_TOP_PLAYER_MATCH_STATS);
    void *fn = ((tFindFunctionChecked)(g_base + RVA_FIND_FUNCTION_CHECKED))(pri, name);
    if (!fn) {
      Logf("eom stats: ClientSetTopPlayerMatchStats not found on %p; not sent", pri);
      return;
    }
    TArrayIntMin parms = {nullptr, 0, 0}; // TArray<FYPlayerMatchStat>, empty
    void **vt = *(void ***)pri;
    ((tProcessEventVirt)vt[VT_PROCESS_EVENT / 8])(pri, fn, &parms);
    Logf("eom stats: sent ClientSetTopPlayerMatchStats (empty) to controller %p "
         "PRI %p -- the client's SetupUIWidgets stage waits for it",
         pc, pri);
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    Logf("eom stats: EXCEPTION sending ClientSetTopPlayerMatchStats for %p",
         orbitComp);
  }
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

// The fleet tier: DISABLED, see the DISPROVED note above OFF_HIGHEST_FLEET.
// Still read, only so a leftover marker file is reported instead of ignored.
static bool FleetTierEnabled() {
  return SwitchOn("DN_HOST_FLEET_TIER", "dn_host_fleet_tier.txt");
}

// Dedicated net mode (HookGetNetMode). Opt-in like every host mod, so a match
// can still be diagnosed with it off.
static bool DedicatedNetModeEnabled() {
  return SwitchOn("DN_HOST_DEDICATED", "dn_host_dedicated.txt");
}

// The player's own fit via mmogbrain (RegisterPlayerLoadout).
static bool PlayerLoadoutsEnabled() {
  return SwitchOn("DN_HOST_PLAYER_LOADOUTS", "dn_host_player_loadouts.txt");
}

// AI in the proving ground (HookGameModeTimer).
static bool BootcampAIEnabled() {
  return SwitchOn("DN_HOST_BC_AI", "dn_host_bc_ai.txt");
}

// Player ship physics without the local-camera cull (HookVehicleViewCull).
static bool ShipPhysicsEnabled() {
  return SwitchOn("DN_HOST_SHIP_PHYSICS", "dn_host_ship_physics.txt");
}

// Installs one single-argument hook and reports it either way.
static void InstallSwitchedHook(const char *what, uintptr_t rva, void *detour,
                                void **orig) {
  void *target = (void *)(g_base + rva);
  if (MH_CreateHook(target, detour, (LPVOID *)orig) != MH_OK ||
      MH_EnableHook(target) != MH_OK)
    Logf("%s: FAILED to hook RVA 0x%X (%p)", what, (unsigned)rva, target);
  else
    Logf("installed: %s hooked at RVA 0x%X (%p)", what, (unsigned)rva, target);
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
  // g_fleetTierArmed is already set in Startup, which must know it before this
  // thread exists so the teleport hook can install without racing it.

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

  // The fleet tier write is disabled for good (see OFF_HIGHEST_FLEET). Say so
  // if an old marker file is still asking for it.
  g_fleetTierArmed = false;
  if (FleetTierEnabled())
    Logf("fleet tier: dn_host_fleet_tier.txt / DN_HOST_FLEET_TIER is set but "
         "IGNORED -- it forced the pawn's orbit state. Use dn_host_dedicated.txt.");

  // Dedicated net mode. Installed here, before the map loads, so every
  // GetNetMode the orbit code asks is answered the same way.
  if (DedicatedNetModeEnabled()) {
    void *nm = (void *)(g_base + RVA_NETDRIVER_GET_NET_MODE);
    if (MH_CreateHook(nm, &HookGetNetMode, (LPVOID *)&g_origGetNetMode) != MH_OK ||
        MH_EnableHook(nm) != MH_OK) {
      Logf("net mode: FAILED to hook UNetDriver::GetNetMode at RVA 0x%X (%p). "
           "The host stays a listen server and players will stay in orbit.",
           RVA_NETDRIVER_GET_NET_MODE, nm);
    } else {
      Logf("installed: UNetDriver::GetNetMode hooked at RVA 0x%X (%p). A server "
           "net driver now reports DEDICATED.",
           RVA_NETDRIVER_GET_NET_MODE, nm);
    }
  } else {
    Logf("net mode: dedicated OFF (create dn_host_dedicated.txt beside the "
         "executable, or set DN_HOST_DEDICATED=1). The host stays a listen "
         "server; players will not leave orbit.");
  }

  // Not switchable: without it a host dies whenever restrictions change.
  InstallSwitchedHook("restrictions (ClientSetPlayerRestrictions_Implementation)",
                      RVA_CLIENT_SET_PLAYER_RESTRICTIONS_IMPL,
                      (void *)&HookClientSetPlayerRestrictions,
                      (void **)&g_origClientSetPlayerRestrictions);

  if (!SwitchOn("DN_HOST_NO_EOM_STATS", "dn_host_no_eom_stats.txt"))
    InstallSwitchedHook("eom stats (ClientStartEndOfMatchTransition)",
                        RVA_CLIENT_START_EOM_TRANSITION,
                        (void *)&HookClientStartEomTransition,
                        (void **)&g_origClientStartEomTransition);
  else
    Logf("eom stats: OFF (dn_host_no_eom_stats.txt / DN_HOST_NO_EOM_STATS=1). "
         "Clients will stop on a black screen at SetupUIWidgets.");

  if (BootcampAIEnabled())
    InstallSwitchedHook("bc ai (AYGameMode_Multiplayer timer)",
                        RVA_GAMEMODE_MP_TIMER, (void *)&HookGameModeTimer,
                        (void **)&g_origGameModeTimer);
  else
    Logf("bc ai: OFF (create dn_host_bc_ai.txt beside the executable, or set "
         "DN_HOST_BC_AI=1). The proving ground has no bots.");

  if (ShipPhysicsEnabled())
    InstallSwitchedHook("ship physics (UYVehicleMovementComp view cull)",
                        RVA_VEHICLE_VIEW_CULL_SETUP, (void *)&HookVehicleViewCull,
                        (void **)&g_origVehicleViewCull);
  else
    Logf("ship physics: OFF (create dn_host_ship_physics.txt beside the "
         "executable, or set DN_HOST_SHIP_PHYSICS=1). Player ships far from the "
         "host's orbit camera are not simulated; players snap back.");

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
