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
#include <math.h>
#pragma comment(lib, "ws2_32.lib")

#include <share.h>
#include <stdarg.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <ctype.h>

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
// Team Elimination. Observed on live hosts (2026-09-29): a ?game=TE host logs
// "type 5", a TDM host "type 3". TE starts with m_enableSpawnAI 0; forcing it
// on for the whole match refilled both teams every time a bot died, so TE
// "just played like a deathmatch" (operator). TE's ROUND flow is not in this
// exe at all -- ClientPreRoundStart / ClientNewRoundStarts / ClientPostRoundEnd
// / ClientRespawnTeamElimination have FName globals (0x3E10260, 0x3E10218,
// 0x3E10268, 0x3E10298) referenced ONLY by their initializers, like
// ClientSetTopPlayerMatchStats: the server build ran rounds. What the mod does
// for TE (operator's call: "bots can be enabled but they should not respawn"):
// spawner ON until both teams are filled, then OFF, so a destroyed bot stays
// destroyed. See the timer below.
#define YGMT_TEAM_ELIMINATION 5
#ifndef OFF_GM_AI_TARGET_T1
#define OFF_GM_AI_TARGET_T1 0x980
#endif

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
// FName(const WIDECHAR*, EFindName). FIXED 2026-09-28: this was 0xC9CF20,
// which is the ANSI (char*) constructor -- the game calls 0xC9CF20 with
// "YMmogbrain" and 0xC9CFA0 with L"m_team". Given a wide string, 0xC9CF20
// built a garbage name: team sync logged "SetTeam not found", and the
// loadout's pid FName was garbage too (nothing read it).
#define RVA_FNAME_CTOR_WIDE 0xC9CFA0
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

// One ?KEY=value option from the controller's connection login URL (e.g.
// "DNPID=", "TEAM="), or false (the host's local player 256 has no connection).
static bool UrlOptionForController(uint8_t *owner, const wchar_t *key, char *out, size_t outLen) {
  out[0] = 0;
  __try {
    if (!IsReadable(owner, OFF_PC_NETCONNECTION + 8))
      return false;
    uint8_t *conn = *(uint8_t **)(owner + OFF_PC_NETCONNECTION);
    if (!IsReadable(conn, OFF_NETCONN_REQUEST_URL + sizeof(FStringMin)))
      return false;
    FStringMin *url = (FStringMin *)(conn + OFF_NETCONN_REQUEST_URL);
    if (!url->data || url->num <= 0 || url->num > 4096 ||
        !IsReadable(url->data, (size_t)url->num * 2))
      return false;
    const wchar_t *p = wcsstr(url->data, key);
    if (!p)
      return false;
    p += wcslen(key);
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

// The same, as wide text (player names are not ASCII-only).
static bool UrlOptionWideForController(uint8_t *owner, const wchar_t *key, wchar_t *out, size_t outLen) {
  out[0] = 0;
  __try {
    if (!IsReadable(owner, OFF_PC_NETCONNECTION + 8))
      return false;
    uint8_t *conn = *(uint8_t **)(owner + OFF_PC_NETCONNECTION);
    if (!IsReadable(conn, OFF_NETCONN_REQUEST_URL + sizeof(FStringMin)))
      return false;
    FStringMin *url = (FStringMin *)(conn + OFF_NETCONN_REQUEST_URL);
    if (!url->data || url->num <= 0 || url->num > 4096 ||
        !IsReadable(url->data, (size_t)url->num * 2))
      return false;
    const wchar_t *p = wcsstr(url->data, key);
    if (!p)
      return false;
    p += wcslen(key);
    size_t n = 0;
    while (p[n] && p[n] != L'?' && p[n] != L'&' && n + 1 < outLen) {
      out[n] = p[n];
      ++n;
    }
    out[n] = 0;
    return n > 0;
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    return false;
  }
}

// DNPID from the controller's connection login URL.
static bool PlayerPIDForController(uint8_t *owner, char *out, size_t outLen) {
  return UrlOptionForController(owner, L"DNPID=", out, outLen);
}

static bool PlayerPIDForManager(void *mgr, char *out, size_t outLen) {
  out[0] = 0;
  __try {
    return PlayerPIDForController(*(uint8_t **)((uint8_t *)mgr + OFF_COMPONENT_OWNER),
                                  out, outLen);
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    return false;
  }
}

// The loadouts each player picked this match, for the match result's ship XP
// (ReportMatchResult). Recorded when mmogbrain served the fit; only touched on
// the game thread (FindLoadoutByID and the end-of-match RPC both run there).
struct FlownShips {
  char pid[80];
  char ids[512]; // comma-separated loadout ids
};
static FlownShips g_flown[64];

// The pid each controller had when its loadout was served. FIXED 2026-09-27:
// the first live match (20:53 host) sent the eom stats and then reported
// nothing -- ReportMatchResult's only silent exit was reading DNPID from the
// connection URL, which had worked for the same player's loadouts minutes
// earlier. Why it failed at match end is not known; this cache is the
// fallback, and every exit now logs.
struct ControllerPID {
  void *pc;
  char pid[80];
};
static ControllerPID g_controllerPIDs[64];

static void RememberControllerPID(void *pc, const char *pid) {
  ControllerPID *slot = nullptr;
  for (auto &c : g_controllerPIDs) {
    if (c.pc == pc) { slot = &c; break; }
    if (!slot && !c.pc) slot = &c;
  }
  if (!slot)
    return;
  slot->pc = pc;
  strncpy_s(slot->pid, sizeof(slot->pid), pid, _TRUNCATE);
}

static bool RememberedControllerPID(void *pc, char *out, size_t outLen) {
  for (auto &c : g_controllerPIDs)
    if (c.pc && c.pc == pc) {
      strncpy_s(out, outLen, c.pid, _TRUNCATE);
      return out[0] != 0;
    }
  return false;
}

static void RecordFlownShip(const char *pid, const char *id) {
  FlownShips *slot = nullptr;
  for (auto &f : g_flown) {
    if (strcmp(f.pid, pid) == 0) { slot = &f; break; }
    if (!slot && !f.pid[0]) slot = &f;
  }
  if (!slot)
    return;
  if (!slot->pid[0])
    strncpy_s(slot->pid, sizeof(slot->pid), pid, _TRUNCATE);
  for (const char *p = strstr(slot->ids, id); p; p = strstr(p + 1, id)) {
    size_t n = strlen(id);
    if ((p == slot->ids || p[-1] == ',') && (p[n] == 0 || p[n] == ','))
      return; // already recorded
  }
  if (slot->ids[0])
    strncat_s(slot->ids, sizeof(slot->ids), ",", _TRUNCATE);
  strncat_s(slot->ids, sizeof(slot->ids), id, _TRUNCATE);
}

static void ClearFlownShips(const char *pid) {
  for (auto &f : g_flown)
    if (f.pid[0] && strcmp(f.pid, pid) == 0)
      f.ids[0] = 0;
}

static const char *FlownShipsFor(const char *pid) {
  for (auto &f : g_flown)
    if (f.pid[0] && strcmp(f.pid, pid) == 0)
      return f.ids;
  return "";
}

// Minimal HTTP/1.0 GET on loopback. Returns the body, or "" on any failure.
// HttpGetLoopbackRaw fetches pathAndQuery into buf and returns where the body
// starts and how long it is. Binary-safe: the body may contain NULs (the
// scoring table is a binary mmog document).
static bool HttpGetLoopbackRaw(const char *pathAndQuery, char *buf, int bufLen,
                               const char **bodyOut, int *bodyLenOut) {
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
  char req[4096];
  int rl = _snprintf_s(req, sizeof(req), _TRUNCATE,
                       "GET %s HTTP/1.0\r\nHost: %s\r\nConnection: close\r\n\r\n",
                       pathAndQuery, host);
  if (rl <= 0 || send(s, req, rl, 0) != rl) {
    closesocket(s);
    return false;
  }
  int total = 0, got;
  while (total < bufLen - 1 && (got = recv(s, buf + total, bufLen - 1 - total, 0)) > 0)
    total += got;
  closesocket(s);
  buf[total] = 0;
  if (total < 12 || strncmp(buf, "HTTP/1.", 7) != 0 || strncmp(buf + 9, "200", 3) != 0)
    return false;
  const char *b = strstr(buf, "\r\n\r\n");
  if (!b)
    return false;
  *bodyOut = b + 4;
  *bodyLenOut = total - (int)(b + 4 - buf);
  return true;
}

static bool HttpGetLoopback(const char *pathAndQuery, char *body, size_t bodyLen) {
  body[0] = 0;
  static char buf[8192];
  const char *b;
  int n;
  if (!HttpGetLoopbackRaw(pathAndQuery, buf, (int)sizeof(buf), &b, &n))
    return false;
  strncpy_s(body, bodyLen, b, _TRUNCATE);
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
  RecordFlownShip(pid, idText);
  __try {
    RememberControllerPID(*(void **)((uint8_t *)mgr + OFF_COMPONENT_OWNER), pid);
  } __except (EXCEPTION_EXECUTE_HANDLER) {
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

// Team sync (on unless dn_host_no_team_sync.txt / DN_HOST_NO_TEAM_SYNC=1),
// see PlayersTick.
static bool g_teamSyncArmed = false;

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
  // AGameMode_K2_PostLogin_Params: APlayerController* NewPlayer at +0x00.
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

static bool g_bcAIArmed = false;
static void PlayersTick(uint8_t *gm);
static void ClassTimeTick(uint8_t *gm);

// Bots in every multiplayer mode (on; dn_host_bots_bc_only.txt /
// DN_HOST_BOTS_BC_ONLY=1 limits them to the proving ground again). Added
// 2026-09-28 at the operator's request. The spawner only fills a team from the
// mode's own bot list (gm+0x968, count gm+0x970) up to its team size
// (gm+0x97C); whether a PvP map loads such a list is NOT known yet -- the
// once-per-game-mode log line below answers it for each mode.
#define OFF_GM_NPC_COUNT 0x970
#define OFF_GM_TEAM_SIZE 0x97C
static bool g_botsBCOnly = false;

static void FlushPendingEomStats();

static void OnslaughtCreepTick(uint8_t *gm);
static void HavocDirectorTick(uint8_t *gm);
static void ConquestTick(uint8_t *gm);
static void ReportFarAway(uint8_t *pawn, const char *what);
static void NpcAiStartTick(uint8_t *gm);

static bool ClassChainContains(UObjectMin *obj, const char *needle);

// Team Elimination rounds, defined with the Havoc code (they use its helpers).
static void TeRoundsTick(uint8_t *gm);
static void TeOnShipKilled(void *gameMode, void *victimCtrl, int victimClass);
static void TeFixLeader(uint8_t *gm);

static void __fastcall HookGameModeTimer(void *gameMode) {
  uint8_t *gm = (uint8_t *)gameMode;
  if (g_bcAIArmed && IsReadable(gm + OFF_GM_ENABLE_SPAWN_AI, 1) &&
      IsReadable(gm + OFF_GM_GAMESTATE, sizeof(void *))) {
    uint8_t *gs = *(uint8_t **)(gm + OFF_GM_GAMESTATE);
    if (gs && IsReadable(gs + OFF_GS_GAME_MODE_TYPE, 1)) {
      int type = gs[OFF_GS_GAME_MODE_TYPE];
      static void *s_reported = nullptr;
      if (s_reported != gameMode && IsReadable(gm + OFF_GM_TEAM_SIZE, 4)) {
        s_reported = gameMode;
        Logf("bots: game mode %p type %d: bot list %d entries, team size %d%s",
             gameMode, type, *(int32_t *)(gm + OFF_GM_NPC_COUNT),
             *(int32_t *)(gm + OFF_GM_TEAM_SIZE),
             (type == YGMT_BOOTCAMP || !g_botsBCOnly) ? "" : " (bots limited to the proving ground)");
      }
      // TE: fill once, then no respawns. The fill has happened once the AI
      // targets are set (gm+0x980/0x984, written by 0x3678F0 when the teams are
      // filled -- the same signal bot balance uses); 15 s later the spawner is
      // switched off for the rest of the match.
      static void *s_teFilled = nullptr;
      static DWORD s_teFilledAt = 0;
      static void *s_teStopped = nullptr;
      if (type == YGMT_TEAM_ELIMINATION && s_teStopped != gameMode &&
          IsReadable(gm + OFF_GM_AI_TARGET_T1, 8)) {
        bool filled = *(int32_t *)(gm + OFF_GM_AI_TARGET_T1) > 0 ||
                      *(int32_t *)(gm + OFF_GM_AI_TARGET_T1 + 4) > 0;
        if (filled && s_teFilled != gameMode) {
          s_teFilled = gameMode;
          s_teFilledAt = GetTickCount();
        }
        if (s_teFilled == gameMode && GetTickCount() - s_teFilledAt > 15000 &&
            gm[OFF_GM_ENABLE_SPAWN_AI]) {
          gm[OFF_GM_ENABLE_SPAWN_AI] = 0;
          s_teStopped = gameMode;
          Logf("bots: Team Elimination %p -- teams filled, m_enableSpawnAI 1 -> 0: "
               "destroyed bots no longer respawn", gameMode);
        }
      }
      // Not on a PvE season episode (YGameMode_PVE: Horde, Escort): its waves
      // come from the map's own PVE managers, and the multiplayer bot fill
      // has nothing to do there (2026-10-09, probe host on PVE_S1E1).
      static int s_pve = -1;
      static void *s_pveFor = nullptr;
      if (s_pveFor != gameMode) {
        s_pveFor = gameMode;
        s_pve = ClassChainContains((UObjectMin *)gameMode, "YGameMode_PVE") ? 1 : 0;
        if (s_pve)
          Logf("pve: game mode %p (type %d) is a PvE episode -- no multiplayer bot fill", gameMode, type);
      }
      if (gm[OFF_GM_ENABLE_SPAWN_AI] == 0 && s_teStopped != gameMode && !s_pve &&
          (type == YGMT_BOOTCAMP || !g_botsBCOnly)) {
        gm[OFF_GM_ENABLE_SPAWN_AI] = 1;
        Logf("bots: game mode %p (type %d) m_enableSpawnAI 0 -> 1. The game fills "
             "both teams when the pre-match countdown reaches 50 s%s.",
             gameMode, type,
             type == YGMT_TEAM_ELIMINATION ? " (Team Elimination: switched off again once filled)" : "");
      }
    }
  }
  g_origGameModeTimer(gameMode);
  OnslaughtCreepTick(gm);
  HavocDirectorTick(gm);
  TeRoundsTick(gm);
  ConquestTick(gm);
  NpcAiStartTick(gm);
  PlayersTick(gm);
  ClassTimeTick(gm);
  FlushPendingEomStats();
}

// ---------------------------------------------------------------------------
// Onslaught match points (on by default; dn_host_no_onslaught_points.txt /
// DN_HOST_NO_ONSLAUGHT_POINTS=1 turns it off)
//
// Operator report 2026-10-07: "the game should finish if 1 of the teams reaches
// 300 points but now it finishes at around 248 points".
//
// Verified in the exe and the cooked assets (2026-10-07):
//   - Onslaught is YGameMode_Invasion / YGameState_Invasion (host mode code
//     "IVN", m_gameModeType 16). GameState_Onslaught_BP sets
//     m_matchPointsForWinning 300 (+0x1E28) and the points per kill:
//     m_playerkillPoints 7 (+0x1E2C), m_smallCreepkillPoints 1 (+0x1E30),
//     m_midCreepkillPoints 2 (+0x1E34), m_bigCreepkillPoints 30 (+0x1E38); the
//     replicated creep-kill counters sit at +0x1E3C.. (small/mid/big T1, then
//     T2). GameInfo_Onslaught_BP sets m_killsForWinning 30.
//   - NOTHING in this exe reads +0x1E28 or writes the creep counters (full
//     scan of .text for those displacements). The points rule was server-build
//     code, like Team Elimination's rounds.
//   - What does end an Onslaught match here is the deathmatch rule: the kill
//     handler 0x375F30 adds 1 to the killer's team (GameState +0x1DF0/+0x1DF4,
//     only for victims whose vtable +0x7E0 is false -- not creeps) and calls
//     vtable +0x8D0 = 0x3685C0, which ends the match when a team's kills reach
//     the game mode's m_killsForWinning (+0x9DC = 30). 30 kills x 7 points plus
//     creeps is the ~248 the operator saw.
//   - The winner is the team with more KILLS (0x384C90 writes GameState
//     +0x4E8: 1 team 1, 2 team 2, 3 draw).
//
// So for Onslaught the mod: lifts the kill limit (game mode +0x9DC only; the
// GameState's copy the clients see stays), counts creep kills into the
// GameState's own counters, ends the match through EndMatch (vtable +0x668,
// as 0x3685C0 does) when a team reaches m_matchPointsForWinning, and makes the
// winner the team with more points.
//
// Which creep is which: the AI ships are VH_ONS_AITarget{L,M,H}_Pawn_BP (H has
// _V/_L tier variants). Their scoring events name them: AITargetL "Fighter",
// AITargetM "Assault Ship", AITargetH "Command Ship" (ribbon names), and the
// datamine's NPC Onslaught weapons list the same three. GUESS: L = small,
// M = mid, H = big, which also matches the points (fighter 1, command ship 30).
#define RVA_TDM_KILLED 0x375F30          // AYGameMode_TDM::Killed (vtable)
#define RVA_TDM_DETERMINE_LEADER 0x384C90 // writes GameState +0x4E8 from kills
#define YGMT_INVASION 16
#define OFF_GM_KILLS_FOR_WINNING 0x9DC
#define OFF_GS_KILLS_T1 0x1DF0
#define OFF_GS_KILLS_T2 0x1DF4
#define OFF_GS_WINNING_TEAM 0x4E8
#define OFF_GS_ONS_POINTS_TO_WIN 0x1E28
#define OFF_GS_ONS_PLAYER_POINTS 0x1E2C   // then small, mid, big (+4 each)
#define OFF_GS_ONS_CREEP_KILLS 0x1E3C     // small, mid, big T1, then T2 (+4 each)
#define OFF_CONTROLLER_PAWN 0x3C8         // as 0x375F30 reads the killer's ship
#define OFF_SHIP_TEAM 0x8C0               // 0x57C100: a ship's EYTeam
#define VT_GM_END_MATCH 0x668
#define ONS_KILL_LIMIT_OFF 1000000
#define OFF_GS_WINNING_TEAM_FINAL 0x56B // GameState final match result (0 while playing)
#define OFF_PAWN_IS_AI_TARGET 0x950 // YPawn: what vtable +0x7E0 returns
#ifndef OFF_USTRUCT_SUPER
#define OFF_USTRUCT_SUPER 0x30
#endif

typedef void(__fastcall *tTdmKilled)(void *gm, void *killer, void *victimCtrl,
                                     void *victimPawn, void *damageType);
static tTdmKilled g_origTdmKilled = nullptr;
typedef void(__fastcall *tDetermineLeader)(void *gm);
static tDetermineLeader g_origDetermineLeader = nullptr;
// The Onslaught points rule's switch: its kill hook is also installed for Team
// Elimination rounds, so it checks this before acting.
static bool g_onsPointsOn = true;

struct OnslaughtMatch {
  void *gm;
  int creeps[2][3];    // [team-1][small, mid, big]
  bool ended;
};
static OnslaughtMatch g_ons = {};

static uint8_t *InvasionGameState(uint8_t *gm) {
  if (!gm || !IsReadable(gm + OFF_GM_GAMESTATE, sizeof(void *)))
    return nullptr;
  uint8_t *gs = *(uint8_t **)(gm + OFF_GM_GAMESTATE);
  if (!gs || !IsReadable(gs + OFF_GS_GAME_MODE_TYPE, 1) ||
      gs[OFF_GS_GAME_MODE_TYPE] != YGMT_INVASION ||
      !IsReadable(gs + OFF_GS_KILLS_T1, OFF_GS_ONS_CREEP_KILLS + 24 - OFF_GS_KILLS_T1))
    return nullptr;
  return gs;
}

// The GameState's points per kill and target, or the cooked defaults when a
// value is missing.
static void OnslaughtRules(uint8_t *gs, int *perPlayer, int perCreep[3], int *target) {
  const int def[4] = {7, 1, 2, 30};
  int32_t *v = (int32_t *)(gs + OFF_GS_ONS_PLAYER_POINTS);
  *perPlayer = v[0] > 0 ? v[0] : def[0];
  for (int i = 0; i < 3; ++i)
    perCreep[i] = v[1 + i] > 0 ? v[1 + i] : def[1 + i];
  int32_t t = *(int32_t *)(gs + OFF_GS_ONS_POINTS_TO_WIN);
  *target = t > 0 ? t : 300;
}

static int OnslaughtPoints(uint8_t *gs, int team /*1 or 2*/) {
  int perPlayer, perCreep[3], target;
  OnslaughtRules(gs, &perPlayer, perCreep, &target);
  int kills = *(int32_t *)(gs + (team == 1 ? OFF_GS_KILLS_T1 : OFF_GS_KILLS_T2));
  int pts = kills * perPlayer;
  for (int i = 0; i < 3; ++i)
    pts += g_ons.creeps[team - 1][i] * perCreep[i];
  return pts;
}

// 0 small, 1 mid, 2 big; -1 not a creep. Walks the class chain so a tier
// variant (VH_ONS_AITargetH_Pawn_V_BP) is found by its own name.
static int CreepSize(void *pawn) {
  UObjectMin *cls = IsReadable(pawn, sizeof(UObjectMin)) ? ((UObjectMin *)pawn)->Class : nullptr;
  for (int depth = 0; cls && depth < 6 && IsReadable(cls, OFF_USTRUCT_SUPER + 8); ++depth) {
    const char *n = NameText(cls->Name);
    if (n) {
      if (strstr(n, "AITargetH")) return 2;
      if (strstr(n, "AITargetM")) return 1;
      if (strstr(n, "AITargetL")) return 0;
    }
    cls = *(UObjectMin **)((uint8_t *)cls + OFF_USTRUCT_SUPER);
  }
  return -1;
}

static const char *ClassNameOf(void *obj) {
  if (!IsReadable(obj, sizeof(UObjectMin)) || !IsReadable(((UObjectMin *)obj)->Class, sizeof(UObjectMin)))
    return "?";
  const char *n = NameText(((UObjectMin *)obj)->Class->Name);
  return n ? n : "?";
}

static int ShipTeam(void *pawn) {
  if (!pawn || !IsReadable((uint8_t *)pawn + OFF_SHIP_TEAM, 1))
    return 0;
  int t = ((uint8_t *)pawn)[OFF_SHIP_TEAM];
  return (t == 1 || t == 2) ? t : 0;
}

// GameState +0x4E8 to the team with more points; false when a team has no
// players (the original's own +0x4F8/+0x4FC branches decide those).
static bool SetLeaderByPoints(uint8_t *gs, int p1, int p2) {
  if (!IsReadable(gs + 0x4F8, 8) || *(int32_t *)(gs + 0x4F8) == 0 || *(int32_t *)(gs + 0x4FC) == 0)
    return false;
  gs[OFF_GS_WINNING_TEAM] = p1 > p2 ? 1 : p2 > p1 ? 2 : 3;
  return true;
}

// ---------------------------------------------------------------------------
// Kill detail for daily contracts (on; dn_host_no_kill_detail.txt turns it off)
//
// mmogbrain counts the daily contracts (mmogbrain/daily_contracts.go), and
// many count only some kills: "Destroy Enemy Corvettes" (the destroyed ship's
// class), "Destroy Enemies with a Corvette" (the class the player flies). The
// match result carried only a kill count. Each kill is now recorded here --
// the destroyed ship's EYShipClass (pawn +0x940, read by 0x57BAE0) and the
// killer's ship's, by the killer's PlayerReplicationInfo -- and reported with
// the result as kl=victim.killer,... (ReportMatchResult).
#define OFF_PAWN_SHIP_CLASS 0x940
#define OFF_PC_PLAYER_STATE 0x3E0 // also defined with the match result below
#define KILL_LOG_MAX 64
struct KillLog {
  void *pri;
  int n;
  uint8_t victim[KILL_LOG_MAX], killer[KILL_LOG_MAX];
};
static KillLog g_killLogs[64] = {};
static SRWLOCK g_killLogLock = SRWLOCK_INIT;

static bool SwitchOn(const char *envName, const char *markerFile);

static bool KillDetailEnabled() {
  static int s_on = -1;
  if (s_on < 0)
    s_on = SwitchOn("DN_HOST_NO_KILL_DETAIL", "dn_host_no_kill_detail.txt") ? 0 : 1;
  return s_on == 1;
}

static void RecordKill(void *killerCtrl, uint8_t victimClass) {
  uint8_t *kc = (uint8_t *)killerCtrl;
  if (!kc || !IsReadable(kc + OFF_PC_PLAYER_STATE, 8) || !IsReadable(kc + OFF_CONTROLLER_PAWN, 8))
    return;
  void *pri = *(void **)(kc + OFF_PC_PLAYER_STATE);
  uint8_t *kp = *(uint8_t **)(kc + OFF_CONTROLLER_PAWN);
  uint8_t killerClass = (kp && IsReadable(kp + OFF_PAWN_SHIP_CLASS, 1)) ? kp[OFF_PAWN_SHIP_CLASS] : 0;
  if (!pri)
    return;
  AcquireSRWLockExclusive(&g_killLogLock);
  KillLog *slot = nullptr;
  for (auto &k : g_killLogs) {
    if (k.pri == pri) { slot = &k; break; }
    if (!slot && !k.pri) slot = &k;
  }
  if (slot) {
    slot->pri = pri;
    if (slot->n < KILL_LOG_MAX) {
      slot->victim[slot->n] = victimClass;
      slot->killer[slot->n] = killerClass;
      slot->n++;
    }
  }
  ReleaseSRWLockExclusive(&g_killLogLock);
}

// ---------------------------------------------------------------------------
// Seconds per ship class, for the Statistics screen (on; added 2026-10-15)
//
// YA_GetPlayerStatistics has DestroyerTimeSec .. CorvetteTimeSec, and nothing
// in this exe counts them (the TimeUseShipClass stats counter has no sender:
// the client's only YA_IncrementPlayerStatsCounter calls are Customize and
// FinishTutorial, 0x30FFA0/0x426740/0x54B690). So the players tick samples,
// once a second, the class of every human's current ship (controller +0x3C8
// -> pawn +0x940, as RecordKill reads it) and adds the elapsed time to it.
// No pawn (dead, in orbit) adds nothing. Reported with the result as
// ct=class.seconds,... (mmogbrain/match_statistics.go).
#define CLASS_TIME_MAX 21 // EYShipClass values 0..20
struct ClassTime {
  void *pri;
  DWORD ms[CLASS_TIME_MAX];
};
static ClassTime g_classTimes[64] = {};
static SRWLOCK g_classTimeLock = SRWLOCK_INIT;

static void AddClassTime(void *pri, uint8_t cls, DWORD ms) {
  if (!pri || cls == 0 || cls >= CLASS_TIME_MAX || ms == 0 || ms > 10000)
    return;
  AcquireSRWLockExclusive(&g_classTimeLock);
  ClassTime *slot = nullptr;
  for (auto &c : g_classTimes) {
    if (c.pri == pri) { slot = &c; break; }
    if (!slot && !c.pri) slot = &c;
  }
  if (slot) {
    slot->pri = pri;
    slot->ms[cls] += ms;
  }
  ReleaseSRWLockExclusive(&g_classTimeLock);
}

// The player's seconds per class as "class.seconds,..." into out.
static void TakeClassTime(void *pri, char *out, size_t outLen) {
  out[0] = 0;
  size_t o = 0;
  AcquireSRWLockExclusive(&g_classTimeLock);
  for (auto &c : g_classTimes) {
    if (c.pri != pri)
      continue;
    for (int i = 1; i < CLASS_TIME_MAX && o + 16 < outLen; ++i)
      if (c.ms[i] >= 1000)
        o += _snprintf_s(out + o, outLen - o, _TRUNCATE, "%s%d.%lu", o ? "," : "", i, (unsigned long)(c.ms[i] / 1000));
    c = ClassTime{};
    break;
  }
  ReleaseSRWLockExclusive(&g_classTimeLock);
}

// The player's kills as "victim.killer,..." into out; false when the kill
// detail is off (mmogbrain then counts only the class-free contracts).
static bool TakeKillLog(void *pri, char *out, size_t outLen) {
  out[0] = 0;
  if (!KillDetailEnabled())
    return false;
  size_t o = 0;
  AcquireSRWLockExclusive(&g_killLogLock);
  for (auto &k : g_killLogs) {
    if (k.pri != pri)
      continue;
    for (int i = 0; i < k.n && o + 12 < outLen; ++i)
      o += _snprintf_s(out + o, outLen - o, _TRUNCATE, "%s%u.%u", o ? "," : "", k.victim[i], k.killer[i]);
    k = KillLog{};
    break;
  }
  ReleaseSRWLockExclusive(&g_killLogLock);
  return true;
}

// ---------------------------------------------------------------------------
// MVP and Man Of The Day ribbons (on; dn_host_no_eom_ribbons.txt /
// DN_HOST_NO_EOM_RIBBONS=1 turns them off). Added 2026-10-15.
//
// Both are end-of-match scoring events (EYScoringEventID ManOfTheDay 41, MVP
// 42), and mmogbrain keeps every end-of-match event out of the host's table:
// with them in it, every match that reached EndMatch overflowed the host's
// stack (2026-09-28). So nobody ever earned either ribbon. The original texts:
//   MVP          "Player with most RP gathered total during the match"
//   ManOfTheDay  "Score the last kill that makes your team win."
// The mod adds them to the player's event counts (ev=) itself: MVP to the
// player(s) with the highest m_score at the match result (GUESS: RP = the
// match score; ties all get it; bots have no player state and do not
// compete), Man Of The Day to the killer whose kill ends the match for his
// team (TDM family: the kill that reaches the kill limit; Onslaught: the kill
// that reaches the points target).
#define SCORING_EVENT_MAN_OF_THE_DAY 41
#define SCORING_EVENT_MVP 42
static void AddScoringEvent(void *pri, uint32_t event);
static bool EomRibbonsEnabled() {
  static int s_on = -1;
  if (s_on < 0)
    s_on = SwitchOn("DN_HOST_NO_EOM_RIBBONS", "dn_host_no_eom_ribbons.txt") ? 0 : 1;
  return s_on == 1;
}

// The match-winning kill: killer's player state and team, if the killer is a
// player.
static void AwardManOfTheDay(void *killer, int winningTeam, const char *why) {
  uint8_t *kc = (uint8_t *)killer;
  if (!EomRibbonsEnabled() || !kc || !IsReadable(kc + OFF_PC_PLAYER_STATE, 8))
    return;
  uint8_t *pri = *(uint8_t **)(kc + OFF_PC_PLAYER_STATE);
  if (!IsReadable(pri, 0x941) || !ClassChainContains((UObjectMin *)pri, "YPlayerReplicationInfo"))
    return;
  int team = pri[0x940]; // OFF_PRI_TEAM
  if (team != winningTeam) {
    Logf("ribbons: match-ending kill (%s) by team %d, winner %d -- no Man Of The Day", why, team, winningTeam);
    return;
  }
  AddScoringEvent(pri, SCORING_EVENT_MAN_OF_THE_DAY);
  Logf("ribbons: Man Of The Day -> player state %p (team %d, %s)", pri, team, why);
}

static void __fastcall HookTdmKilled(void *gameMode, void *killer, void *victimCtrl,
                                     void *victimPawn, void *damageType) {
  uint8_t *gm = (uint8_t *)gameMode;
  uint8_t *gs = nullptr;
  __try {
    gs = g_onsPointsOn ? InvasionGameState(gm) : nullptr;
    if (gs) {
      if (g_ons.gm != gameMode) {
        memset(&g_ons, 0, sizeof(g_ons));
        g_ons.gm = gameMode;
        Logf("onslaught: match %p -- points rule armed (kill limit %d lifted, "
             "points to win %d)", gameMode, *(int32_t *)(gm + OFF_GM_KILLS_FOR_WINNING),
             *(int32_t *)(gs + OFF_GS_ONS_POINTS_TO_WIN));
      }
      // The deathmatch check runs inside the original call; keep it from
      // ending Onslaught on kills.
      *(int32_t *)(gm + OFF_GM_KILLS_FOR_WINNING) = ONS_KILL_LIMIT_OFF;
    }
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    gs = nullptr;
  }

  int32_t kills1 = 0, kills2 = 0;
  if (gs) {
    kills1 = *(int32_t *)(gs + OFF_GS_KILLS_T1);
    kills2 = *(int32_t *)(gs + OFF_GS_KILLS_T2);
  }

  // The assault and command ships are AI targets only while their kill is
  // scored (see SpawnOnslaughtAIShip): set the byte for the original's
  // AI-scoring route (victim vtable +0x7E0), clear it again after so the
  // game's ship-list removal on death runs for them.
  int victimSize = gs ? CreepSize(victimPawn) : -1;
  bool flagged = false;
  if (victimSize >= 1 && IsReadable((uint8_t *)victimPawn + OFF_PAWN_IS_AI_TARGET, 1) &&
      !((uint8_t *)victimPawn)[OFF_PAWN_IS_AI_TARGET]) {
    ((uint8_t *)victimPawn)[OFF_PAWN_IS_AI_TARGET] = 1;
    flagged = true;
  }
  // The destroyed ship's class, read before the original runs (it may
  // destroy the pawn). Kill detail for daily contracts, see RecordKill.
  // Team Elimination: the class of the ship lost, for TeOnShipKilled.
  int teVictimClass = -1;
  __try {
    if (victimPawn && IsReadable((uint8_t *)victimPawn + OFF_PAWN_SHIP_CLASS, 1))
      teVictimClass = ((uint8_t *)victimPawn)[OFF_PAWN_SHIP_CLASS];
  } __except (EXCEPTION_EXECUTE_HANDLER) {
  }
  int victimClass = -1;
  if (KillDetailEnabled() && killer && killer != victimCtrl && victimPawn &&
      IsReadable((uint8_t *)victimPawn + OFF_PAWN_SHIP_CLASS, 1))
    victimClass = ((uint8_t *)victimPawn)[OFF_PAWN_SHIP_CLASS];
  // Man Of The Day, TDM family: the original call ends the match when a
  // team's kills reach the game mode's m_killsForWinning (+0x9DC, via
  // 0x3685C0). Lifted to ONS_KILL_LIMIT_OFF in Onslaught, so it never fires
  // there (Onslaught is handled where its points end the match).
  int32_t limit = 0, before1 = 0, before2 = 0;
  uint8_t *tdmGS = nullptr;
  __try {
    if (EomRibbonsEnabled() && killer && killer != victimCtrl && IsReadable(gm + OFF_GM_KILLS_FOR_WINNING, 4) &&
        IsReadable(gm + OFF_GM_GAMESTATE, 8)) {
      tdmGS = *(uint8_t **)(gm + OFF_GM_GAMESTATE);
      limit = *(int32_t *)(gm + OFF_GM_KILLS_FOR_WINNING);
      if (tdmGS && IsReadable(tdmGS + OFF_GS_KILLS_T2, 4)) {
        before1 = *(int32_t *)(tdmGS + OFF_GS_KILLS_T1);
        before2 = *(int32_t *)(tdmGS + OFF_GS_KILLS_T2);
      } else {
        tdmGS = nullptr;
      }
    }
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    tdmGS = nullptr;
  }
  g_origTdmKilled(gameMode, killer, victimCtrl, victimPawn, damageType);
  if (tdmGS && limit > 0 && limit < ONS_KILL_LIMIT_OFF) {
    __try {
      int32_t after1 = *(int32_t *)(tdmGS + OFF_GS_KILLS_T1);
      int32_t after2 = *(int32_t *)(tdmGS + OFF_GS_KILLS_T2);
      if (before1 < limit && after1 >= limit)
        AwardManOfTheDay(killer, 1, "kill limit");
      else if (before2 < limit && after2 >= limit)
        AwardManOfTheDay(killer, 2, "kill limit");
    } __except (EXCEPTION_EXECUTE_HANDLER) {
    }
  }
  if (victimClass >= 0) {
    __try {
      RecordKill(killer, (uint8_t)victimClass);
    } __except (EXCEPTION_EXECUTE_HANDLER) {
    }
  }
  if (flagged)
    ((uint8_t *)victimPawn)[OFF_PAWN_IS_AI_TARGET] = 0;
  __try {
    TeOnShipKilled(gameMode, victimCtrl, teVictimClass);
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    Logf("te: EXCEPTION handling a lost ship");
  }

  if (!gs)
    return;
  __try {
    int size = CreepSize(victimPawn);
    // The assault ship and command ship are not creeps (no vtable +0x7E0),
    // so the deathmatch handler just counted them as a player kill. They
    // score as creeps only.
    if (size >= 0) {
      if (*(int32_t *)(gs + OFF_GS_KILLS_T1) > kills1)
        *(int32_t *)(gs + OFF_GS_KILLS_T1) = kills1;
      if (*(int32_t *)(gs + OFF_GS_KILLS_T2) > kills2)
        *(int32_t *)(gs + OFF_GS_KILLS_T2) = kills2;
    }
    int scorer = 0;
    if (size >= 0) {
      void *killerShip = (killer && IsReadable((uint8_t *)killer + OFF_CONTROLLER_PAWN, 8))
                             ? *(void **)((uint8_t *)killer + OFF_CONTROLLER_PAWN) : nullptr;
      scorer = ShipTeam(killerShip);
      int victimTeam = ShipTeam(victimPawn);
      if (scorer == 0 || scorer == victimTeam)
        scorer = victimTeam == 1 ? 2 : victimTeam == 2 ? 1 : 0;
      if (scorer) {
        g_ons.creeps[scorer - 1][size]++;
        *(int32_t *)(gs + OFF_GS_ONS_CREEP_KILLS + 4 * (3 * (scorer - 1) + size)) =
            g_ons.creeps[scorer - 1][size];
      }
    }
    int p1 = OnslaughtPoints(gs, 1), p2 = OnslaughtPoints(gs, 2);
    int perPlayer, perCreep[3], target;
    OnslaughtRules(gs, &perPlayer, perCreep, &target);
    Logf("onslaught: %s %s -> team %d | points T1 %d (kills %d) T2 %d (kills %d) / %d",
         size >= 0 ? (size == 0 ? "small creep" : size == 1 ? "mid creep" : "big creep") : "kill",
         ClassNameOf(victimPawn), size >= 0 ? scorer : 0, p1,
         *(int32_t *)(gs + OFF_GS_KILLS_T1), p2, *(int32_t *)(gs + OFF_GS_KILLS_T2), target);
    if (!g_ons.ended && (p1 >= target || p2 >= target)) {
      g_ons.ended = true;
      SetLeaderByPoints(gs, p1, p2);
      Logf("onslaught: team %d reached %d points (T1 %d, T2 %d) -- ending the match",
           p1 >= p2 ? 1 : 2, target, p1, p2);
      AwardManOfTheDay(killer, p1 >= p2 ? 1 : 2, "onslaught points");
      void **vt = *(void ***)gm;
      ((void(__fastcall *)(void *))vt[VT_GM_END_MATCH / 8])(gm);
    }
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    Logf("onslaught: EXCEPTION while counting a kill");
  }
}

// The leader/winner by points, not kills, in Onslaught. The original's
// "a team has no players" branches (+0x4F8/+0x4FC) are kept.
static void __fastcall HookDetermineLeader(void *gameMode) {
  g_origDetermineLeader(gameMode);
  __try {
    TeFixLeader((uint8_t *)gameMode);
  } __except (EXCEPTION_EXECUTE_HANDLER) {
  }
  __try {
    uint8_t *gs = InvasionGameState((uint8_t *)gameMode);
    if (!gs || g_ons.gm != gameMode)
      return;
    uint8_t byKills = gs[OFF_GS_WINNING_TEAM];
    int p1 = OnslaughtPoints(gs, 1), p2 = OnslaughtPoints(gs, 2);
    if (SetLeaderByPoints(gs, p1, p2) && g_ons.ended && gs[OFF_GS_WINNING_TEAM] != byKills)
      Logf("onslaught: leader by points %d (T1 %d, T2 %d), was %d by kills",
           gs[OFF_GS_WINNING_TEAM], p1, p2, byKills);
  } __except (EXCEPTION_EXECUTE_HANDLER) {
  }
}

// ---------------------------------------------------------------------------
// Onslaught AI ships (on; dn_host_no_onslaught_creeps.txt turns it off)
//
// Onslaught's fighters, assault ships and command ship never spawned: every
// kill in a live Onslaught match was a bot (2026-10-07). Verified in the exe
// and the cooked content the same day:
//   - The ships are VH_ONS_AITarget{L,M,H}_Pawn_BP (YCreepPawn). The map's
//     _Onslaught sublevel (loaded on our hosts) holds YCreepNavigationSpawn
//     actors ONS_AITarget{L,M,H}_InitialSpawn_T1/T2 and _Spawn_T1/T2:
//     m_spawnClass +0x428, m_spawnTeam +0x430 (EYTeam), m_beginMatchSpawnDelay
//     +0x434. Nothing in this exe reads them (only reflection refers to the
//     class) and YGameMode_Invasion adds no code to the deathmatch mode: the
//     spawner was server-build code.
//   - The spawn itself is in the exe: AYAICombatSceneManager::SpawnCreep
//     0x264B30 (this, UClass* creepClass, AActor* spawnAt -- its root
//     component's location and rotation --, uint8* team); server only, needs
//     a YCreepManager, respects the creep manager's per-team cap. The scene
//     manager comes from 0x2529E0 (no arguments).
//   - The rules are the game mode's own (GameInfo_Onslaught_BP): small creep
//     class +0xA58, max +0xA60 (12), respawn +0xA64 (2 s); mid +0xA68 / +0xA70
//     (6) / +0xA74 (8 s); big +0xA78, Veteran +0xA80, Legendary +0xA88, max
//     +0xA90 (1), respawn +0xA94 (180 s). The 2018 launch notes give the same
//     counts and the command ship's 180 s.
// So once a second, on an Onslaught host, this spawns the initial ships at the
// InitialSpawn points, then keeps each team at its maximum: a missing ship is
// respawned at a Spawn point once it has been missing for the respawn time.
// GUESS: the first wave goes out 20 s after the match starts (the per-point
// m_beginMatchSpawnDelay is read but its use is not traced); alive ships are
// counted from GObjects (class + team, not pending kill).
#define RVA_SPAWN_CREEP 0x264B30
#define RVA_COMBAT_SCENE_MANAGER 0x2529E0
#define RVA_GS_FLEET_TYPE 0x396C50        // as GetAISpawnTier reads it
#define OFF_CLASSPTR_CREEP_NAV_SPAWN 0x3E12298 // YCreepNavigationSpawn UClass*, once registered
#define OFF_CLASSPTR_CREEP_NAV 0x3E11A58       // YCreepNavigation UClass* (registration 0x6007C0)
#define OFF_SPAWN_CLASS 0x428
#define OFF_SPAWN_TEAM 0x430
#define OFF_GM_ONS_SMALL_CLASS 0xA58
#define OFF_GM_ONS_MID_CLASS 0xA68
#define OFF_GM_ONS_BIG_CLASS 0xA78
#define ONS_FIRST_WAVE_DELAY_MS 20000
#define RF_CDO_OR_ARCHETYPE 0x30
#define OBJ_PENDING_KILL (1 << 29)

typedef void *(__fastcall *tSpawnCreep)(void *sceneMgr, void *cls, void *spawnAt, uint8_t *team);
typedef void *(__fastcall *tGetSceneManager)();
typedef uint8_t(__fastcall *tGsFleetType)(void *gs);

struct OnsSpawnPoint {
  void *actor;
  int size;  // 0 small, 1 mid, 2 big
  int team;  // 1 or 2
  bool initial;
};
struct OnsCreepState {
  void *gm;
  DWORD firstSeen;
  bool firstWave;
  int nPoints;
  OnsSpawnPoint points[256];
  DWORD missingSince[2][3];
  DWORD lastScan;
  int spawned, failed;
};
static OnsCreepState g_onsCreeps = {};
static void *g_onsBigShip[2] = {}; // last seen command ship per team, for the position log
static void *g_onsMidShip[2] = {}; // last seen assault ship per team, likewise
static void *g_onsBigNav = nullptr; // the map's ONS_AITargetH_Navigation node, if any

static bool SwitchOn(const char *envName, const char *markerFile);

static bool OnslaughtCreepsEnabled() {
  return !SwitchOn("DN_HOST_NO_ONSLAUGHT_CREEPS", "dn_host_no_onslaught_creeps.txt");
}

static int SizeFromName(const char *n) {
  if (!n) return -1;
  if (strstr(n, "AITargetH")) return 2;
  if (strstr(n, "AITargetM")) return 1;
  if (strstr(n, "AITargetL")) return 0;
  return -1;
}

// The map's Onslaught spawn points, by their own names.
static void FindOnslaughtSpawnPoints() {
  OnsCreepState &st = g_onsCreeps;
  st.nPoints = 0;
  UObjectMin *cls = *(UObjectMin **)(g_base + OFF_CLASSPTR_CREEP_NAV_SPAWN);
  if (!cls)
    return;
  FUObjectArrayMin *arr = GObjects();
  if (!arr || !arr->Objects)
    return;
  UObjectMin *navCls = *(UObjectMin **)(g_base + OFF_CLASSPTR_CREEP_NAV);
  g_onsBigNav = nullptr;
  for (int i = 0; i < arr->NumElements && st.nPoints < 256; ++i) {
    UObjectMin *o = arr->Objects[i].Object;
    if (o && navCls && o->Class == navCls && !(o->Flags & RF_CDO_OR_ARCHETYPE) && !g_onsBigNav &&
        !(arr->Objects[i].Flags & OBJ_PENDING_KILL)) {
      const char *nn = NameText(o->Name);
      if (nn && strstr(nn, "AITargetH_Navigation"))
        g_onsBigNav = o;
      continue;
    }
    if (!o || o->Class != cls || (o->Flags & RF_CDO_OR_ARCHETYPE) ||
        (arr->Objects[i].Flags & OBJ_PENDING_KILL))
      continue;
    const char *n = NameText(o->Name);
    int size = SizeFromName(n);
    int team = ((uint8_t *)o)[OFF_SPAWN_TEAM];
    if (size < 0 || (team != 1 && team != 2))
      continue;
    OnsSpawnPoint &p = st.points[st.nPoints++];
    p.actor = o;
    p.size = size;
    p.team = team;
    p.initial = n && strstr(n, "Initial");
  }
}

// The class the game mode spawns for a size; the big one by fleet tier.
static void *OnslaughtCreepClass(uint8_t *gm, uint8_t *gs, int size, void *pointClass) {
  void *cls = nullptr;
  if (size == 0)
    cls = *(void **)(gm + OFF_GM_ONS_SMALL_CLASS);
  else if (size == 1)
    cls = *(void **)(gm + OFF_GM_ONS_MID_CLASS);
  else {
    uint8_t fleet = ((tGsFleetType)(g_base + RVA_GS_FLEET_TYPE))(gs);
    int off = fleet == 3 ? 0x10 : fleet == 2 ? 0x08 : 0;
    cls = *(void **)(gm + OFF_GM_ONS_BIG_CLASS + off);
    if (!cls)
      cls = *(void **)(gm + OFF_GM_ONS_BIG_CLASS);
  }
  return cls ? cls : pointClass;
}

// The NPC AI switch (verified 2026-10-08 in the exe; not yet verified live).
// The AYNPCAIController constructor (0x276EA0) sets byte +0xAFA to 1, and while
// it is set the controller's tick (0x2BA2A0) and its AI think timer callback
// (0x2B23B0) both return at once: no TickBehavior, no behaviour, no movement,
// no firing. Only 0x288AE0 clears it. That function also starts the think
// timer (+0xAD0, interval +0x830). The game calls it from its own NPC
// spawner (0x2693A0, at 0x26A36C) and from the game mode's NPC-spawned
// handler (vtable +0x978 = 0x378DE0), and only while the match runs
// (GameState +0x569). Our assault and command ships never got it. They were
// registered with behaviour ROUTE and sat still (host 2026-10-08 08:44). So
// once a second, while the match runs, any M/H ship whose controller is
// still halted gets it.
#define OFF_NPC_AI_HALTED 0xAFA
#define RVA_NPC_START_AI 0x288AE0
#define OFF_GS_MATCH_RUNNING 0x569
#define OFF_GS_MATCH_OVER 0x56A
typedef void(__fastcall *tNpcStartAI)(void *ctrl);
static bool ClassIsAncestor(UObjectMin *c, void *ancestor) {
  for (int depth = 0; c && ancestor && depth < 32; ++depth, c = *(UObjectMin **)((uint8_t *)c + 0x30))
    if (c == ancestor)
      return true;
  return false;
}

// Alive Onslaught ships by team and size: instances of the creep classes the
// game mode and the spawn points name, not pending kill. Compared by class
// pointer: a name lookup per object costs syscalls (IsReadable), and a
// GObjects scan doing that per object once froze a host for 90 s (README).
static void CountOnslaughtCreeps(uint8_t *gm, uint8_t *gs, int alive[2][3]) {
  memset(alive, 0, sizeof(int) * 6);
  void *cls[16];
  int csize[16], nc = 0;
  auto add = [&](void *c, int size) {
    if (!c || nc >= 16) return;
    for (int i = 0; i < nc; ++i)
      if (cls[i] == c) return;
    cls[nc] = c;
    csize[nc++] = size;
  };
  add(*(void **)(gm + OFF_GM_ONS_SMALL_CLASS), 0);
  add(*(void **)(gm + OFF_GM_ONS_MID_CLASS), 1);
  for (int v = 0; v < 3; ++v)
    add(*(void **)(gm + OFF_GM_ONS_BIG_CLASS + 8 * v), 2);
  for (int i = 0; i < g_onsCreeps.nPoints; ++i)
    add(*(void **)((uint8_t *)g_onsCreeps.points[i].actor + OFF_SPAWN_CLASS), g_onsCreeps.points[i].size);
  FUObjectArrayMin *arr = GObjects();
  if (!arr || !arr->Objects || nc == 0)
    return;
  for (int i = 0; i < arr->NumElements; ++i) {
    UObjectMin *o = arr->Objects[i].Object;
    if (!o || (o->Flags & RF_CDO_OR_ARCHETYPE) || (arr->Objects[i].Flags & OBJ_PENDING_KILL))
      continue;
    for (int c = 0; c < nc; ++c) {
      if (o->Class == cls[c]) {
        int team = ((uint8_t *)o)[OFF_SHIP_TEAM];
        if (team == 1 || team == 2) {
          alive[team - 1][csize[c]]++;
          ReportFarAway((uint8_t *)o, "onslaught ship");
          if (csize[c] >= 1 && gs && gs[OFF_GS_MATCH_RUNNING] && !gs[OFF_GS_MATCH_OVER]) {
            uint8_t *ctrl = *(uint8_t **)((uint8_t *)o + 0x3F8); // APawn::Controller
            if (ctrl && IsReadable(ctrl + OFF_NPC_AI_HALTED, 1) && ctrl[OFF_NPC_AI_HALTED] &&
                ClassIsAncestor(((UObjectMin *)ctrl)->Class, *(void **)(g_base + 0x3E18DE0))) {
              ((tNpcStartAI)(g_base + RVA_NPC_START_AI))(ctrl);
              Logf("onslaught creeps: %s %p -- controller %p AI started (+0xAFA %d)",
                   csize[c] == 2 ? "command ship" : "assault ship", o, ctrl, ctrl[OFF_NPC_AI_HALTED]);
            }
          }
          if (csize[c] == 2)
            g_onsBigShip[team - 1] = o;
          else if (csize[c] == 1)
            g_onsMidShip[team - 1] = o;
        }
        break;
      }
    }
  }
}

// The fault address of a caught exception, for the log.
static int CaptureFault(EXCEPTION_POINTERS *ep, uintptr_t *at) {
  *at = ep && ep->ExceptionRecord ? (uintptr_t)ep->ExceptionRecord->ExceptionAddress : 0;
  return EXCEPTION_EXECUTE_HANDLER;
}

// AActor::SpawnCollisionHandlingMethod (+0x179, registration 0x141D57F40) on
// the class default object, which SpawnCreep's default spawn parameters fall
// back to. The assault ships were refused at their spawn points ("Couldn't
// spawn Pawn of type VH_ONS_AITargetM_Pawn_BP_C at ONS_AITargetM_Spawn_T2_n",
// host 2026-10-07 23:41) -- the engine's answer when a spawn collides and the
// method says not to. 2 = AdjustIfPossibleButAlwaysSpawn. GUESS: the original
// server spawned them with its own parameters; this is the CDO-side
// equivalent. Once per class.
#define OFF_ACTOR_SPAWN_COLLISION 0x179
#define SPAWN_ADJUST_BUT_ALWAYS 2
static void AllowCreepSpawnWhenColliding(void *cls) {
  static void *s_done[16];
  static int s_n = 0;
  for (int i = 0; i < s_n; ++i)
    if (s_done[i] == cls)
      return;
  if (s_n >= 16)
    return;
  s_done[s_n++] = cls;
  FUObjectArrayMin *arr = GObjects();
  if (!arr || !arr->Objects)
    return;
  for (int i = 0; i < arr->NumElements; ++i) {
    UObjectMin *o = arr->Objects[i].Object;
    if (o && o->Class == cls && (o->Flags & 0x10)) { // RF_ClassDefaultObject
      uint8_t was = ((uint8_t *)o)[OFF_ACTOR_SPAWN_COLLISION];
      ((uint8_t *)o)[OFF_ACTOR_SPAWN_COLLISION] = SPAWN_ADJUST_BUT_ALWAYS;
      const char *n = NameText(((UObjectMin *)cls)->Name);
      Logf("onslaught creeps: %s spawn collision handling %d -> %d", n ? n : "?", was, SPAWN_ADJUST_BUT_ALWAYS);
      return;
    }
  }
}

// The assault ship and command ship are NOT creeps. VH_ONS_AITargetL (the
// fighter) is a YCreepPawn with m_creepAIController; AITargetM/H are ordinary
// AI ships with APawn::AIControllerClass (+0x3D8, ..._Behavior_YAICtrl_BP), and
// SpawnCreep faulted on every one of them at 0x264E4C, walking the class chain
// of creep-only field +0xCB8 (host 2026-10-07 23:50): the actor was left half
// made -- an icon and a hitbox, no ship (operator). For them this does what
// SpawnCreep does without the creep part: UWorld::SpawnActor 0x1A0C8D0 at the
// spawn point's transform (root component +0x198: rotation quat +0x150,
// location +0x160, quat -> rotator 0xC40250), SetTeam 0x5961C0, then the
// pawn's own APawn::SpawnDefaultController (vtable +0x6B0; exec thunk
// 0x1EB9560 jumps there), and a net update (+0x580), as SpawnCreep ends.
#define RVA_WORLD_SPAWN_ACTOR 0x1A0C8D0
#define RVA_SPAWN_PARAMS_CTOR 0x1CD1780
#define RVA_PAWN_SET_TEAM 0x5961C0
#define RVA_QUAT_TO_ROTATOR 0xC40250
#define OFF_ACTOR_ROOT_COMPONENT 0x198
#define OFF_COMP_WORLD_ROT 0x150
#define OFF_COMP_WORLD_LOC 0x160
#define VT_ACTOR_GET_WORLD 0x108
#define VT_PAWN_SPAWN_DEFAULT_CONTROLLER 0x6B0
#define OFF_PAWN_CONTROLLER 0x3F8            // APawn::Controller
#define OFF_PAWN_AI_CONTROLLER_CLASS 0x3D8   // APawn::AIControllerClass
#define OFF_NPC_BEHAVIOUR_STATE 0xA38        // EYCombatSceneBehavior the scene spawn resets
#define YCSB_NONE 0x14
#define VT_CONTROLLER_POSSESS 0x640          // AController::Possess (as 0x26A096 / SpawnCreep)
#define VT_CONTROLLER_POSSESS2 0x668         // called right after it in both
typedef void(__fastcall *tPossess)(void *ctrl, void *pawn);
#define OFF_CLASSPTR_NPC_AI_CONTROLLER 0x3E18DE0 // UClass* owning m_spawnActor
#define OFF_NPC_SPAWN_ACTOR 0x9EC            // TWeakObjectPtr<AActor> m_spawnActor
#define OFF_NAV_NEXT_ARRAY 0x3E8             // YCreepNavigation next points (weak)
#define OFF_NAV_NEXT_COUNT 0x3F0
#define RVA_WEAK_PTR_SET 0xD64B60            // TWeakObjectPtr = UObject*
#define RVA_WEAK_PTR_GET 0xD6AD50            // TWeakObjectPtr -> UObject*
typedef void(__fastcall *tWeakSet)(void *weak, void *obj);
typedef void *(__fastcall *tWeakGet)(void *weak);
typedef void *(__fastcall *tSpawnActor)(void *world, void *cls, const float *loc, const float *rot, void *params);
typedef void *(__fastcall *tSpawnParamsCtor)(void *params);
typedef void(__fastcall *tPawnSetTeam)(void *pawn, uint8_t *team);
typedef float *(__fastcall *tQuatToRot)(const float *quat, float *rot);
typedef void *(__fastcall *tGetWorld)(void *actor);
typedef void(__fastcall *tActorVoidFn)(void *actor);

// Why the assault and command ships sat still (verified 2026-10-08 from the
// cooked Blueprints and the exe; not yet verified live):
//   - Their controllers (VH_ONS_AITarget{M,H}_Behavior_YAICtrl_BP, decoded from
//     the Kismet bytecode) move the ship ONLY through the combat scene manager:
//     TickBehavior -> RouteStart = cast<YCreepNavigation>(m_spawnActor) ->
//     combatManager.ReplacePawnNPCBehaviours(pawn, [{m_behaviorTime -1,
//     m_behaviorType ROUTE (16), m_newControlValues RouteController (None),
//     m_location RouteStart, m_targetActor GetNPCId(pawn)}], false). The
//     combat manager is GetPlayerController(0).GetCombatManager() (thunk
//     0x74FFA0 -> the scene manager singleton 0x2529E0).
//   - And only when m_runBehaviourTree is set; otherwise it draws "Behavior
//     Tree Disabled". m_runBehaviourTree is the controller byte +0x780 (its
//     reflection setter 0x786BE0), which the scene manager sets when it spawns
//     an NPC (0x2693A0: = !entry+0xA0) and nothing else did for ours.
//   - ReplacePawnNPCBehaviours (exec 0x76F2D0 -> 0x2600E0) does nothing unless
//     GetNPCId (0x254360) finds the pawn in the scene manager's NPC table
//     (+0x3C0 data, +0x3C8 count, 0xA8-byte entries, weak pawn at +0x8C):
//     ours were never in it, so it returned -1.
// So this registers the ship the way the scene manager's own NPC spawn does
// (0x2693A0 ~0x26A096): AddNPCSpawn (0x23A280, the native behind the
// AddNPCSpawn UFunction) with the pawn class (+0x20), spawn point (+0x30) and
// team (+0x38); then the instanced flag (+0x88 = 1, so the scene manager's own
// tick never spawns the entry again) and the weak pawn (+0x8C), the
// controller's scene index (+0xAAC) and +0xAB0 (= entry+0x10), behaviour state
// YCSB_NONE (+0xA38), m_runBehaviourTree; then the ROUTE behaviour the
// Blueprint itself asks for, through ReplaceNPCBehaviours (0x25FE80: stores
// the behaviours, sets +0x780, NextBehaviour 0x28E340, register 0x254990).
// NOT done (left for evidence): the scene manager also spawns an NPC
// PlayerState, calls the level script's OnNPCSpawned (0x572220) and the game
// mode's vtable +0x978.
#define RVA_SCENE_ADD_NPC_SPAWN 0x23A280     // (sceneMgr, FYNPCSpawn*) -> id
#define RVA_SCENE_REPLACE_NPC_BEHAVIOURS 0x25FE80 // (sceneMgr, id, TArray* by value, bool)
#define RVA_FMEMORY_MALLOC 0xC06B70          // (size, align); GMalloc vtable +0x10
#define OFF_SCENE_NPCS 0x3C0                 // TArray<FYNPCSpawn> data; count +0x3C8
#define OFF_SCENE_NPCS_COUNT 0x3C8
#define OFF_SCENE_RANDOM_CLASS 0x418         // AddNPCSpawn replaces +0x20 with a random class when set
#define NPC_SPAWN_SIZE 0xA8
#define OFF_NPCSPAWN_TYPE 0x10               // int, AddNPCSpawn's default 5; copied to ctrl +0xAB0
#define OFF_NPCSPAWN_PAWN_CLASS 0x20
#define OFF_NPCSPAWN_SPAWN_POINT 0x30
#define OFF_NPCSPAWN_TEAM 0x38
#define OFF_NPCSPAWN_INSTANCED 0x88
#define OFF_NPCSPAWN_PAWN 0x8C               // TWeakObjectPtr<APawn>
#define OFF_NPC_RUN_BEHAVIOUR_TREE 0x780
#define OFF_NPC_SCENE_INDEX 0xAAC
#define OFF_NPC_SCENE_TYPE 0xAB0
#define YCSB_ROUTE 16
#define BEHAVIOUR_DATA_SIZE 0x28             // FYCombatSceneBehaviorData (0x702345)
typedef int(__fastcall *tSceneAddNpcSpawn)(void *scene, void *spawn);
typedef void(__fastcall *tSceneReplaceBehaviours)(void *scene, int id, void *behaviours, uint8_t keepTree);
typedef void *(__fastcall *tFMemoryMalloc)(size_t size, uint32_t align);
struct TArrayRaw {
  void *data;
  int32_t num, max;
};

static bool ClassIsA(UObjectMin *c, void *ancestor) {
  for (; c && ancestor; c = *(UObjectMin **)((uint8_t *)c + OFF_USTRUCT_SUPER))
    if (c == ancestor)
      return true;
  return false;
}

// Returns the scene NPC id, or -1.
static int RegisterOnslaughtNPC(void *pawn, void *ctrl, void *cls, void *routeStart, int team) {
  uint8_t *scene = (uint8_t *)((tGetSceneManager)(g_base + RVA_COMBAT_SCENE_MANAGER))();
  if (!scene)
    return -1;
  // AddNPCSpawn's own defaults (exec thunk 0x73FAE0).
  alignas(16) uint8_t spawn[NPC_SPAWN_SIZE] = {};
  *(float *)(spawn + 0x5C) = 10.0f;
  *(float *)(spawn + 0x9C) = -1.0f;
  *(int32_t *)(spawn + 0x8C) = -1; // weak pointers: index -1 = null
  *(int32_t *)(spawn + 0x94) = -1;
  *(int32_t *)(spawn + OFF_NPCSPAWN_TYPE) = 5;
  *(void **)(spawn + OFF_NPCSPAWN_PAWN_CLASS) = cls;
  *(void **)(spawn + OFF_NPCSPAWN_SPAWN_POINT) = routeStart;
  spawn[OFF_NPCSPAWN_TEAM] = (uint8_t)team;
  uint8_t randomClass = scene[OFF_SCENE_RANDOM_CLASS];
  scene[OFF_SCENE_RANDOM_CLASS] = 0;
  int id = ((tSceneAddNpcSpawn)(g_base + RVA_SCENE_ADD_NPC_SPAWN))(scene, spawn);
  scene[OFF_SCENE_RANDOM_CLASS] = randomClass;
  if (id < 0 || id >= *(int32_t *)(scene + OFF_SCENE_NPCS_COUNT))
    return -1;
  uint8_t *entry = *(uint8_t **)(scene + OFF_SCENE_NPCS) + (size_t)id * NPC_SPAWN_SIZE;
  entry[OFF_NPCSPAWN_INSTANCED] = 1;
  ((tWeakSet)(g_base + RVA_WEAK_PTR_SET))(entry + OFF_NPCSPAWN_PAWN, pawn);
  uint8_t *c = (uint8_t *)ctrl;
  *(int32_t *)(c + OFF_NPC_SCENE_INDEX) = id;
  *(int32_t *)(c + OFF_NPC_SCENE_TYPE) = *(int32_t *)(entry + OFF_NPCSPAWN_TYPE);
  c[OFF_NPC_BEHAVIOUR_STATE] = YCSB_NONE;
  c[OFF_NPC_RUN_BEHAVIOUR_TREE] = 1;
  // The Blueprint's ROUTE, now, so the ship moves even if its BeginPlay found
  // no player controller to take the combat manager from. The callee takes
  // the array by value and frees it (FMemory::Free 0xBFC9C0).
  uint8_t *b = (uint8_t *)((tFMemoryMalloc)(g_base + RVA_FMEMORY_MALLOC))(BEHAVIOUR_DATA_SIZE, 0);
  if (b) {
    memset(b, 0, BEHAVIOUR_DATA_SIZE);
    *(float *)(b + 0x00) = -1.0f;              // m_behaviorTime
    b[0x08] = YCSB_ROUTE;                      // m_behaviorType
    *(void **)(b + 0x10) = nullptr;            // m_newControlValues (RouteController, unset in the BP)
    *(void **)(b + 0x18) = routeStart;         // m_location
    *(int32_t *)(b + 0x20) = id;               // m_targetActor (the BP passes GetNPCId(pawn))
    TArrayRaw arr = {b, 1, 1};
    ((tSceneReplaceBehaviours)(g_base + RVA_SCENE_REPLACE_NPC_BEHAVIOURS))(scene, id, &arr, 0);
  }
  return id;
}

static void *SpawnOnslaughtAIShip(uint8_t *gm, void *cls, void *at, int team, bool bigShip) {
  uint8_t *comp = *(uint8_t **)((uint8_t *)at + OFF_ACTOR_ROOT_COMPONENT);
  if (!comp)
    return nullptr;
  float quat[4], loc[3], rot[3] = {0, 0, 0};
  memcpy(quat, comp + OFF_COMP_WORLD_ROT, sizeof(quat));
  memcpy(loc, comp + OFF_COMP_WORLD_LOC, sizeof(loc));
  ((tQuatToRot)(g_base + RVA_QUAT_TO_ROTATOR))(quat, rot);
  void *world = ((tGetWorld)(*(void ***)gm)[VT_ACTOR_GET_WORLD / 8])(gm);
  if (!world)
    return nullptr;
  alignas(16) uint8_t params[0x80] = {};
  ((tSpawnParamsCtor)(g_base + RVA_SPAWN_PARAMS_CTOR))(params);
  void *pawn = ((tSpawnActor)(g_base + RVA_WORLD_SPAWN_ACTOR))(world, cls, loc, rot, params);
  if (!pawn)
    return nullptr;
  uint8_t t = (uint8_t)team;
  ((tPawnSetTeam)(g_base + RVA_PAWN_SET_TEAM))(pawn, &t);
  // Mark it an AI target. The kill handler (0x375850) sends a kill to the
  // game mode's AI-ship scoring -- scoring manager vtable +0x208 -> the
  // Invasion handler 0x415AF0 ("Invasion ScoringEvent - Kill AI!!!"), which
  // picks AITargetH/M/L by the victim's class against the game mode's own
  // creep classes (+0xA78/+0xA80/+0xA88, +0xA68, +0xA58) -- only when the
  // victim's YPawn vtable +0x7E0 is true, and that is just the byte at
  // +0x950 (0x57FD70). Fighters have it; the assault ship and command ship
  // Blueprints do not, so destroying them scored nothing (operator,
  // 2026-10-08) although the handler names their classes: the server that
  // spawned them set it.
  // NOT set here any more (2026-10-08): it is set only for the kill itself,
  // in HookTdmKilled. As an AI target the ship was never registered in the
  // game's list of ships (0x57FF20 adds only pawns whose +0x7E0 is false; the
  // death/destroy removals 0x571500/0x575970 skip them too), and that list is
  // where the Onslaught bots' GetCommandShip (0x28BDA0: ship class 0x1D,
  // YSC_AI_CREEP_LARGE) looks. Every bot got None ("Accessed None" floods from
  // VH_ONS_ScoutLightStandard_Behavior TickBehaviorPerception, host logs
  // 2026-10-08) and had no objective. Off, the ship is also in the scene
  // manager's ship list that turrets (CAPITALSHIP) and the AI perceive.
  void **vt = *(void ***)pawn;
  // The controller, set up the way the combat scene manager sets up the NPC
  // ships it spawns (0x2693A0, around 0x26A096): spawn the pawn's
  // AIControllerClass (+0x3D8), set its m_spawnActor (TWeakObjectPtr +0x9EC)
  // and m_runBehaviourTree, possess (vtable +0x640, then +0x668), then
  // register it with the scene manager (RegisterOnslaughtNPC above). Two
  // earlier versions skipped the registration ("these ships belong to no
  // scene") and every assault ship and command ship sat still (host
  // 2026-10-08 00:51, operator): the Blueprint's only move order goes through
  // the scene manager's NPC table.
  // m_spawnActor is the spawn point itself, as the scene manager sets it
  // (entry +0x30), when it is a YCreepNavigation -- the Blueprint casts it to
  // one to start its ROUTE from; else the spawn point's first next-navigation
  // point (+0x3E8 weak array, count +0x3F0).
  void *ctrl = *(void **)((uint8_t *)pawn + OFF_PAWN_CONTROLLER);
  bool hadController = ctrl != nullptr;
  if (!ctrl) {
    void *ctrlCls = *(void **)((uint8_t *)pawn + OFF_PAWN_AI_CONTROLLER_CLASS);
    if (ctrlCls) {
      alignas(16) uint8_t cparams[0x80] = {};
      ((tSpawnParamsCtor)(g_base + RVA_SPAWN_PARAMS_CTOR))(cparams);
      ctrl = ((tSpawnActor)(g_base + RVA_WORLD_SPAWN_ACTOR))(world, ctrlCls, loc, rot, cparams);
    }
  }
  if (!ctrl) {
    Logf("onslaught creeps: %p got no controller (no AIControllerClass, or its spawn failed)", pawn);
  } else {
    bool isNpc = ClassIsA(((UObjectMin *)ctrl)->Class, *(void **)(g_base + OFF_CLASSPTR_NPC_AI_CONTROLLER));
    void *navCls = *(void **)(g_base + OFF_CLASSPTR_CREEP_NAV);
    void *nav = nullptr;
    int next = *(int32_t *)((uint8_t *)at + OFF_NAV_NEXT_COUNT);
    if (ClassIsA(((UObjectMin *)at)->Class, navCls))
      nav = at;
    else if (next > 0)
      nav = ((tWeakGet)(g_base + RVA_WEAK_PTR_GET))(*(void **)((uint8_t *)at + OFF_NAV_NEXT_ARRAY));
    else if (bigShip && g_onsBigNav)
      // GUESS: a command ship spawn point that is no navigation point and has
      // no next point; the map's command-ship route node is where it heads.
      nav = g_onsBigNav;
    if (isNpc) {
      ((tWeakSet)(g_base + RVA_WEAK_PTR_SET))((uint8_t *)ctrl + OFF_NPC_SPAWN_ACTOR, nav ? nav : at);
      ((uint8_t *)ctrl)[OFF_NPC_BEHAVIOUR_STATE] = YCSB_NONE;
      ((uint8_t *)ctrl)[OFF_NPC_RUN_BEHAVIOUR_TREE] = 1;
    }
    void **cvt = *(void ***)ctrl;
    ((tPossess)cvt[VT_CONTROLLER_POSSESS / 8])(ctrl, pawn);
    ((tPossess)cvt[VT_CONTROLLER_POSSESS2 / 8])(ctrl, pawn);
    int npcId = -1;
    if (isNpc && nav)
      npcId = RegisterOnslaughtNPC(pawn, ctrl, cls, nav, team);
    const char *an = NameText(((UObjectMin *)at)->Name);
    const char *nn = nav ? NameText(((UObjectMin *)nav)->Name) : nullptr;
    const char *cn = NameText(((UObjectMin *)ctrl)->Class->Name);
    Logf("onslaught creeps: %p controller %s (%s, %s), spawn point %s (%d next); route start %s; "
         "scene NPC id %d, behaviour %d, run tree %d",
         pawn, cn ? cn : "?", hadController ? "auto-possessed" : "spawned here",
         isNpc ? "NPC" : "NOT an NPC controller", an ? an : "?", next, nav ? (nn ? nn : "?") : "NONE", npcId,
         ((uint8_t *)ctrl)[OFF_NPC_BEHAVIOUR_STATE], ((uint8_t *)ctrl)[OFF_NPC_RUN_BEHAVIOUR_TREE]);
  }
  ((tActorVoidFn)vt[0x580 / 8])(pawn); // AActor::ForceNetUpdate (VT_ACTOR_FORCE_NET_UPDATE)
  return pawn;
}

static bool SpawnOnslaughtCreep(uint8_t *gm, uint8_t *gs, int team, int size, bool initialOnly) {
  OnsCreepState &st = g_onsCreeps;
  int cand[256], nc = 0;
  for (int i = 0; i < st.nPoints; ++i) {
    OnsSpawnPoint &p = st.points[i];
    if (p.team == team && p.size == size && p.initial == initialOnly)
      cand[nc++] = i;
  }
  if (nc == 0)
    for (int i = 0; i < st.nPoints; ++i)
      if (st.points[i].team == team && st.points[i].size == size)
        cand[nc++] = i;
  if (nc == 0)
    return false;
  OnsSpawnPoint &p = st.points[cand[rand() % nc]];
  void *scene = ((tGetSceneManager)(g_base + RVA_COMBAT_SCENE_MANAGER))();
  if (!scene) {
    if (st.failed++ < 3)
      Logf("onslaught creeps: no AYAICombatSceneManager in the world; cannot spawn");
    return false;
  }
  void *cls = OnslaughtCreepClass(gm, gs, size, *(void **)((uint8_t *)p.actor + OFF_SPAWN_CLASS));
  if (!cls)
    return false;
  uint8_t t = (uint8_t)team;
  const char *cn = NameText(((UObjectMin *)cls)->Name);
  const char *pn = NameText(((UObjectMin *)p.actor)->Name);
  AllowCreepSpawnWhenColliding(cls);
  void *pawn = nullptr;
  uintptr_t faultAt = 0;
  __try {
    pawn = size == 0 ? ((tSpawnCreep)(g_base + RVA_SPAWN_CREEP))(scene, cls, p.actor, &t)
                     : SpawnOnslaughtAIShip(gm, cls, p.actor, team, size == 2);
  } __except (CaptureFault(GetExceptionInformation(), &faultAt)) {
    if (st.failed++ < 40)
      Logf("onslaught creeps: EXCEPTION at exe+0x%llX spawning %s for team %d at %s",
           (unsigned long long)(faultAt - g_base), cn ? cn : "?", team, pn ? pn : "?");
    return false;
  }
  if (pawn) {
    st.spawned++;
    Logf("onslaught creeps: spawned %s for team %d at %s -> %p", cn ? cn : "?", team, pn ? pn : "?", pawn);
  } else if (st.failed++ < 20) {
    Logf("onslaught creeps: SpawnCreep returned null (%s, team %d, at %s)", cn ? cn : "?", team, pn ? pn : "?");
  }
  return pawn != nullptr;
}

// Diagnostics for "turrets, bots and the AI ships never target each other"
// (operator report 2026-10-08). Verified in the exe, the candidates all come
// from the combat scene manager. Its tick (0x2693A0) rebuilds three lists from
// the world's pawns every +0x3FC seconds:
//   - +0x448/+0x450: ships that are not AI targets (vtable +0x7E0) and alive
//     (+0x4E8);
//   - +0x458/+0x460: AI targets whose controller is no YFighterDroneAIController
//     -- the fighters, assault ships and command ships;
//   - +0x468/+0x470: fighter drones.
// Turrets (0x272490) cycle them by their m_targetShipTypes (+0x540: the
// support auto gun has CAPITALSHIP, CREEP, JET -- all three). The AI's candidate
// pass (0x2C6180) drops pawns whose state byte +0x941 (0x57BD10) is 2 or 3.
// This logs the list sizes, how many of list 2 are the mod's ships, and each
// sampled ship's state, so one match shows which step drops them.
#define OFF_PAWN_STATE 0x941
static void LogTargetLists() {
  uint8_t *sm = (uint8_t *)((tGetSceneManager)(g_base + RVA_COMBAT_SCENE_MANAGER))();
  if (!sm || !IsReadable(sm + 0x448, 0x30))
    return;
  int n[3] = {*(int32_t *)(sm + 0x450), *(int32_t *)(sm + 0x460), *(int32_t *)(sm + 0x470)};
  int ours = 0, oursShips = 0, states[4] = {0, 0, 0, 0};
  uint8_t *ships = *(uint8_t **)(sm + 0x448);
  if (ships && n[0] > 0 && n[0] < 4096 && IsReadable(ships, (size_t)n[0] * 8))
    for (int i = 0; i < n[0]; ++i) {
      uint8_t *p = (uint8_t *)((tWeakGet)(g_base + RVA_WEAK_PTR_GET))(ships + (size_t)i * 8);
      if (p && CreepSize(p) >= 1)
        ++oursShips;
    }
  uint8_t *data = *(uint8_t **)(sm + 0x458);
  if (data && n[1] > 0 && n[1] < 4096 && IsReadable(data, (size_t)n[1] * 8))
    for (int i = 0; i < n[1]; ++i) {
      uint8_t *p = (uint8_t *)((tWeakGet)(g_base + RVA_WEAK_PTR_GET))(data + (size_t)i * 8);
      if (!p || !IsReadable(p + OFF_PAWN_STATE, 1))
        continue;
      if (CreepSize(p) >= 0)
        ++ours;
      int st = p[OFF_PAWN_STATE];
      if (st >= 0 && st < 4)
        ++states[st];
    }
  Logf("onslaught targets: scene manager lists ships %d (%d assault/command ships) / AI targets %d (%d Onslaught "
       "ships; states 0:%d 1:%d 2:%d 3:%d) / drones %d", n[0], oursShips, n[1], ours, states[0], states[1], states[2],
       states[3], n[2]);
}

static void OnslaughtCreepTick(uint8_t *gm) {
  static int s_enabled = -1;
  if (s_enabled < 0)
    s_enabled = OnslaughtCreepsEnabled() ? 1 : 0;
  if (!s_enabled)
    return;
  __try {
    uint8_t *gs = InvasionGameState(gm);
    if (!gs)
      return;
    OnsCreepState &st = g_onsCreeps;
    DWORD now = GetTickCount();
    if (st.gm != gm) {
      memset(&st, 0, sizeof(st));
      st.gm = gm;
      st.firstSeen = now;
    }
    if (g_ons.gm == gm && g_ons.ended)
      return;
    if (IsReadable(gs + OFF_GS_WINNING_TEAM_FINAL, 1) && gs[OFF_GS_WINNING_TEAM_FINAL] != 0)
      return; // the match has a result
    if (now - st.firstSeen < ONS_FIRST_WAVE_DELAY_MS)
      return;
    if (st.nPoints == 0 || now - st.lastScan > 30000) {
      st.lastScan = now;
      int before = st.nPoints;
      FindOnslaughtSpawnPoints();
      if (st.nPoints != before)
        Logf("onslaught creeps: %d spawn points on this map", st.nPoints);
      if (st.nPoints == 0)
        return;
    }
    int maxN[3] = {*(int32_t *)(gm + 0xA60), *(int32_t *)(gm + 0xA70), *(int32_t *)(gm + 0xA90)};
    int every[3] = {*(int32_t *)(gm + 0xA64), *(int32_t *)(gm + 0xA74), *(int32_t *)(gm + 0xA94)};
    const int defMax[3] = {12, 6, 1}, defEvery[3] = {2, 8, 180};
    for (int k = 0; k < 3; ++k) {
      if (maxN[k] <= 0 || maxN[k] > 64) maxN[k] = defMax[k];
      if (every[k] <= 0 || every[k] > 600) every[k] = defEvery[k];
    }
    int alive[2][3];
    CountOnslaughtCreeps(gm, gs, alive);
    static DWORD s_lastCountLog = 0;
    if (now - s_lastCountLog > 15000) {
      s_lastCountLog = now;
      Logf("onslaught creeps: alive T1 %d/%d/%d T2 %d/%d/%d (small/mid/big); spawned %d, failed %d",
           alive[0][0], alive[0][1], alive[0][2], alive[1][0], alive[1][1], alive[1][2], st.spawned, st.failed);
      for (int i = 0; i < 4; ++i) {
        int t = i & 1, big = i < 2;
        uint8_t *ship = (uint8_t *)(big ? g_onsBigShip[t] : g_onsMidShip[t]);
        uint8_t *comp = (ship && alive[t][big ? 2 : 1] > 0) ? *(uint8_t **)(ship + 0x198) : nullptr;
        uint8_t *ctrl = ship ? *(uint8_t **)(ship + OFF_PAWN_CONTROLLER) : nullptr;
        int behaviour = ctrl && IsReadable(ctrl + OFF_NPC_BEHAVIOUR_STATE, 1) ? ctrl[OFF_NPC_BEHAVIOUR_STATE] : -1;
        if (comp && IsReadable(comp + 0x160, 12))
          Logf("onslaught creeps: %s T%d %p at (%.0f, %.0f, %.0f), behaviour %d, state %d, ai target %d, team %d, "
               "controller %p", big ? "command ship" : "assault ship", t + 1, ship,
               *(float *)(comp + 0x160), *(float *)(comp + 0x164), *(float *)(comp + 0x168), behaviour,
               ship[OFF_PAWN_STATE], ship[OFF_PAWN_IS_AI_TARGET], ship[OFF_SHIP_TEAM], ctrl);
      }
      LogTargetLists();
    }
    if (!st.firstWave) {
      st.firstWave = true;
      Logf("onslaught creeps: first wave (max small %d / mid %d / big %d per team)", maxN[0], maxN[1], maxN[2]);
      for (int t = 1; t <= 2; ++t)
        for (int k = 0; k < 3; ++k)
          for (int n = alive[t - 1][k]; n < maxN[k]; ++n)
            if (!SpawnOnslaughtCreep(gm, gs, t, k, true))
              break;
      return;
    }
    for (int t = 1; t <= 2; ++t)
      for (int k = 0; k < 3; ++k) {
        DWORD &since = st.missingSince[t - 1][k];
        if (alive[t - 1][k] >= maxN[k]) {
          since = 0;
          continue;
        }
        if (!since) {
          since = now;
          continue;
        }
        if (now - since >= (DWORD)every[k] * 1000) {
          SpawnOnslaughtCreep(gm, gs, t, k, false);
          since = now; // the next missing one waits its own respawn time
        }
      }
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    Logf("onslaught creeps: EXCEPTION 0x%08X in the spawner tick", GetExceptionCode());
  }
}

// ---------------------------------------------------------------------------
// Conquest score (on; dn_host_no_conquest_score.txt / DN_HOST_NO_CONQUEST_SCORE=1
// turns it off)
//
// No Conquest match on our hosts ever ended: all 10 TER host logs up to
// 2026-10-08 ran until the reaper killed them (one 25 min), none reached
// ClientStartEndOfMatchTransition.
//
// The rules (Grey Box, "Game Update Preview: Conquest Mode", 2018-04-13):
// "Every 10 seconds the percentage of overall territory each team controls is
// added to their Score ... The first team to 2,500 points wins!" The cooked
// GameMode_TER_BP agrees: m_scoreForWinning 2500, and the Territory mode's own
// m_scoringTerritorySecuredUpdateTime 10.
//
// Verified 2026-10-08 (raw .text scan, leaf functions included):
//   - GameMode_TER_BP is YGameMode_Territory (size 0xA30) <- the capture-points
//     mode (0xA10): m_scoreForWinning +0x9F0 (int), m_territoryScoreMultiplier
//     +0x9F4 (float), m_territoryScoreThreshold +0x9F8 (int),
//     m_scoreUpdateInterval +0xA0C (int, 9), m_scoringTerritorySecuredUpdateTime
//     +0xA10 (float, 10). Territory overrides nothing but its destructor.
//   - GameState_TER_BP is YGameState_TER <- YGameState_CapturePointsGame:
//     the team scores m_currentTicketCountT1/T2 +0x1E08/+0x1E0C (int, the HUD's
//     scores and voice lines read them), m_scoreForWinning +0x1E10 (int),
//     m_currentTerritoryT1/T2 +0x1E80/+0x1E84 (int, the HUD's % bar;
//     getters 0x3968C0/0x3968D0, OnRep 0x38F4F0).
//   - NOTHING writes those five GameState fields but the constructors, and
//     nothing reads the game mode's score target or intervals: the scoring was
//     server-build code, like Onslaught's.
//   - What the exe DOES do server-side is the territory itself: the GameState
//     tick (0x3AAB90, GameState_TER vtable slot 122, runs while gs+0x569 &&
//     !gs+0x56A) rebuilds a per-tile influence map from its influence sources
//     (0x3ACB60: gs+0x1FE8, 0x40-byte entries -> AYTerritoryVolume query
//     0x5AEC50 -> merge 0x38DFE0) into gs+0x1EF8, a TMap<uint32 tile,
//     {float T1, float T2}> (0x18-byte set elements; sparse-array Num +0x08,
//     NumFree +0x34, allocation bits inline +0x10 / heap +0x20). Its per-ship
//     pass (0x394A30) gives a tile to team 2 when T1 <= T2 (and T1+T2 > 0),
//     else team 1, and divides by the volume's grid size (m_gameVolume
//     gs+0x2030, floats +0x40C x +0x410).
// So once a second this counts the map's tiles per team the way 0x394A30
// does, writes each team's percentage of the whole grid to the GameState, and
// every m_scoringTerritorySecuredUpdateTime seconds adds it to the team's
// score; at m_scoreForWinning it sets the winner (+0x4E8) and calls EndMatch
// (vtable +0x668), as the Onslaught rule does.
//
// WHICH map (corrected 2026-10-28, verified in the exe): the tick keeps FOUR
// tile maps, all TMap<uint32 tile, {float T1, float T2}> with the layout above:
//   +0x1EF8 the capture points' influence (0x3ACB60, rebuilt on every owner
//           change through the capture point's vtable +0x5F0 = 0x536510 --
//           but from each source's OWN team copy, which only 0x3A2610
//           updates; corrected 2026-10-29, see SyncPointOwner),
//   +0x1F48 the ships' claims (GatherShipTerritoryData, one ship per tick:
//           a ship's tile counts only where no capture point already gives
//           it to the ship's team or a stronger influence holds it -- the
//           patch notes' "enemy ships ... negate a portion of their claim"),
//   +0x1F98 the links (0x38FAB0 + 0x3944C0, when m_bAllowTerritoryLinking
//           gs+0x1E2F -- 1 from the constructor; GameState_TER_BP sets
//           m_linkingRangeMulti / m_linkingBonusRadiusMulti 1.5 and
//           m_linkBreakingRadius 6000),
//   +0x1EA8 the TERRITORY: once every ship has been gathered, 0x3AAB90 clears
//           it, copies the point map in (0x38C030), adds each link tile that
//           beats it, and adds the ship map (0x385670, source gs+0x1F48 at
//           0x3AB15F). It keeps that until the next pass.
// Until 2026-10-28 this counted the point map plus each ship's +0x858, which
// misses every link tile, and took a team's percentage as its share of the
// CLAIMED tiles (an operator choice, 2026-10-08, made when the whole-grid
// share of those counts moved too slowly). The original is the whole grid:
// the patch notes' "percentage of overall territory", and the HUD
// (UI_TER_HUD_WBP) draws a neutral-area bar between the teams'
// (m_neutralAreaScoreBarWidget), so the two need not add up to 100. The
// claimed-share count is still there behind dn_host_conquest_claimed_share.txt.
// GUESS: the multiplier is applied when the designers set one (> 0), else 1
// (GameMode_TER_BP sets none, so the field reads 0: as a factor nobody would
// ever score); the threshold is "a team must hold more than this % to score";
// percentages round to nearest. m_scoreUpdateInterval (9) is not used: the
// patch notes' 10 s is the Territory mode's own field.
#define YGMT_TER 6                        // host log "type 6" on ?game=TER
#define OFF_GM_TER_SCORE_FOR_WINNING 0x9F0
#define OFF_GM_TER_SCORE_MULTIPLIER 0x9F4
#define OFF_GM_TER_SCORE_THRESHOLD 0x9F8
#define OFF_GM_TER_SCORE_INTERVAL 0xA10
#define OFF_GS_TER_SCORE_T1 0x1E08
#define OFF_GS_TER_SCORE_T2 0x1E0C
#define OFF_GS_TER_SCORE_FOR_WINNING 0x1E10
#define OFF_GS_TER_TERRITORY_T1 0x1E80
#define OFF_GS_TER_TERRITORY_T2 0x1E84
#define OFF_GS_TER_TILE_MAP 0x1EF8      // capture points' influence
#define OFF_GS_TER_TERRITORY_MAP 0x1EA8 // points + links + ships: the territory
#define OFF_GS_TER_VOLUME 0x2030
#define OFF_TER_VOLUME_GRID 0x40C
#define TER_TILE_ELEMENT 0x18

static bool ConquestScoreEnabled() {
  return !SwitchOn("DN_HOST_NO_CONQUEST_SCORE", "dn_host_no_conquest_score.txt");
}

// Tiles per team in one of the GameState's tile maps; false if unreadable.
static bool CountTerritoryTiles(uint8_t *gs, int mapOff, int *t1, int *t2, int *mapTiles) {
  *t1 = *t2 = *mapTiles = 0;
  uint8_t *map = gs + mapOff;
  uint8_t *data = *(uint8_t **)map;
  int num = *(int32_t *)(map + 0x08);
  int numFree = *(int32_t *)(map + 0x34);
  if (num <= 0)
    return true;
  if (!data || num > 4 * 1024 * 1024 || !IsReadable(data, (size_t)num * TER_TILE_ELEMENT))
    return false;
  uint32_t *bits = *(uint32_t **)(map + 0x20) ? *(uint32_t **)(map + 0x20) : (uint32_t *)(map + 0x10);
  if (numFree > 0 && !IsReadable(bits, ((size_t)num + 31) / 32 * 4))
    return false;
  for (int i = 0; i < num; ++i) {
    if (numFree > 0 && !(bits[i >> 5] & (1u << (i & 31))))
      continue;
    float a = *(float *)(data + (size_t)i * TER_TILE_ELEMENT + 4);
    float b = *(float *)(data + (size_t)i * TER_TILE_ELEMENT + 8);
    ++*mapTiles;
    if (a + b > 0.0f) {
      if (a <= b) ++*t2;
      else ++*t1;
    }
  }
  return true;
}

struct ConquestMatch {
  void *gm;
  DWORD lastScore, lastLog, lastPointScan, armedAt;
  bool ended, pointsReady;
  int contributionTicks, contributionCalls, captureCalls;
  uint8_t *cps[16]; // the capture points SetUpCapturePoints found
  int numCps, ownerSyncs;
  float bestPotential; // highest TerritoryContribution ratio seen (diagnostic)
};
static ConquestMatch g_ter = {};

// The capture points (verified 2026-10-08 in the exe and MP_Gorge_Territory;
// not yet verified live). Operator test 10:24: "no capture indicator, and I
// couldn't capture them", and the score rule above logged "0 in the map" all
// match. Each _Territory sublevel places CapturePoint_TER_BP actors -- A and B
// (capturable, m_timeToOwn 20) and the two team bases (m_bIsLocked,
// m_initialOwningTeam) -- and NOTHING in this exe sets them up:
//   - YCapturePoint_TER (UClass* at 0x3E11FA0, size 0x500; ctor 0x52CF40)
//     starts with m_isActive (+0x4B0) false, m_ownedByTeam (+0x450) none and
//     m_stationIndex (+0x3E4) unset.
//   - Its tick (0x55F030, vtable slot 122) runs the capture logic (0x5583F0)
//     only when m_isActive is set (and +0x400, which the TER ctor sets, and the
//     match runs). Activate (exec 0x73CC40 -> 0x533420) sets it.
//   - The GameState's capture-point setup 0x39F750 -- the one that fills the
//     influence sources (gs+0x1FE8, one 0x40-byte entry per m_stationIndex:
//     owner team, location, radius +0x4FC, linkable points) and builds the
//     tile map (0x3ACB60) -- skips points whose index is below 0 ("Tried
//     initializing a capture point with an invalid index") and has no caller
//     in this exe at all. Captures call the GameState back (owner change ->
//     vtable +0x838 = 0x3A2610 -> rebuild) once it has run.
//   - SetCapturePoint (exec 0x7761C0 -> 0x558CD0, arg EYTeam) sets the owner.
// So once a second until it has happened, this gives every capture point an
// index (by name -- GUESS: the original order is not known; only uniqueness
// matters to the code above), the team bases their m_initialOwningTeam, calls
// Activate on each, then runs 0x39F750.
#define OFF_CLASSPTR_CAPTURE_POINT_TER 0x3E11FA0
#define OFF_CP_STATION_INDEX 0x3E4
#define OFF_CP_LOCKED 0x401
#define OFF_CP_INITIAL_OWNER 0x402
#define OFF_CP_OWNER 0x450
#define OFF_CP_ACTIVE 0x4B0
#define RVA_CP_SET_OWNER 0x558CD0
#define RVA_CP_ACTIVATE 0x533420
#define RVA_GS_TER_SETUP_CAPTURE_POINTS 0x39F750
// The GameState's own ship pass (0x394A30, from its tick 0x3AAB90) leaves each
// ship's claimed tile count at pawn +0x858 -- tiles where neither the
// capture-point map already gives them to the ship's team nor a stronger
// influence wins, and not tiles an ally already claimed -- over the ships it
// gathered at gs+0x1E88 (TArray<TWeakObjectPtr<APawn>>, count +0x1E90).
#define OFF_GS_TER_SHIPS 0x1E88
#define OFF_PAWN_TER_TILES 0x858
typedef void(__fastcall *tCpSetOwner)(void *cp, uint8_t team);
typedef void(__fastcall *tCpActivate)(void *cp);
typedef void(__fastcall *tGsSetupCapturePoints)(void *gs);

static bool SetUpCapturePoints(uint8_t *gs) {
  void *cpCls = *(void **)(g_base + OFF_CLASSPTR_CAPTURE_POINT_TER);
  FUObjectArrayMin *arr = GObjects();
  if (!cpCls || !arr || !arr->Objects)
    return false;
  uint8_t *cps[16];
  const char *names[16];
  int n = 0;
  for (int i = 0; i < arr->NumElements && n < 16; ++i) {
    UObjectMin *o = arr->Objects[i].Object;
    if (!o || (o->Flags & RF_CDO_OR_ARCHETYPE) || (arr->Objects[i].Flags & OBJ_PENDING_KILL))
      continue;
    UObjectMin *c = o->Class;
    bool is = false;
    for (int d = 0; c && d < 3; ++d, c = *(UObjectMin **)((uint8_t *)c + OFF_USTRUCT_SUPER))
      if (c == cpCls) { is = true; break; }
    if (!is)
      continue;
    names[n] = NameText(o->Name);
    cps[n++] = (uint8_t *)o;
  }
  if (n == 0)
    return false;
  for (int a = 1; a < n; ++a) // by name: A, B, ..., Team1Base, Team2Base
    for (int b = a; b > 0 && names[b - 1] && names[b] && strcmp(names[b - 1], names[b]) > 0; --b) {
      uint8_t *t = cps[b]; cps[b] = cps[b - 1]; cps[b - 1] = t;
      const char *tn = names[b]; names[b] = names[b - 1]; names[b - 1] = tn;
    }
  for (int i = 0; i < n; ++i) {
    uint8_t *cp = cps[i];
    if (*(int32_t *)(cp + OFF_CP_STATION_INDEX) < 0 || *(int32_t *)(cp + OFF_CP_STATION_INDEX) >= n)
      *(int32_t *)(cp + OFF_CP_STATION_INDEX) = i;
    uint8_t initial = cp[OFF_CP_INITIAL_OWNER];
    if ((initial == 1 || initial == 2) && cp[OFF_CP_OWNER] != initial)
      ((tCpSetOwner)(g_base + RVA_CP_SET_OWNER))(cp, initial);
    if (!cp[OFF_CP_ACTIVE])
      ((tCpActivate)(g_base + RVA_CP_ACTIVATE))(cp);
    Logf("conquest: capture point %s -- index %d, owner %d (initial %d), locked %d, active %d", names[i] ? names[i] : "?",
         *(int32_t *)(cp + OFF_CP_STATION_INDEX), cp[OFF_CP_OWNER], initial, cp[OFF_CP_LOCKED], cp[OFF_CP_ACTIVE]);
  }
  ((tGsSetupCapturePoints)(g_base + RVA_GS_TER_SETUP_CAPTURE_POINTS))(gs);
  Logf("conquest: %d capture points set up; influence sources %d, tile map %d tiles", n,
       *(int32_t *)(gs + 0x1FF0), *(int32_t *)(gs + OFF_GS_TER_TILE_MAP + 8));
  for (int i = 0; i < n; ++i)
    g_ter.cps[i] = cps[i];
  g_ter.numCps = n;
  return true;
}

// A captured point's territory (on; dn_host_no_conquest_point_owner.txt /
// DN_HOST_NO_CONQUEST_POINT_OWNER=1 turns it off)
//
// Player reports 2026-10-29: the two capturable points "that should claim a
// lot of map for your team still cannot be captured". They do flip (6 and 9
// captures in the 07:33 and 00:04 Skybridge matches), but the point map never
// changed: "points 33/33 of 66" from the first update to the last, in all 273
// updates of both matches -- the two bases' tiles, and nothing for A or B,
// whoever held them. Verified in the exe 2026-10-29:
//   - Each influence source (gs+0x1FE8, 0x40-byte entries, count gs+0x1FF0)
//     carries its OWN copy of the point's team at +0x00, written once by the
//     setup 0x39F750 (A and B are neutral then, so they claim nothing). The
//     tile map 0x3ACB60 reads only that copy.
//   - The one function that updates it is GameState_TER vtable +0x838 =
//     0x3A2610 (gs, EYTeam, station index, FString* letter, TArray<AController*>*
//     capturers): it writes the team, rebuilds the map (0x3ACB60), and plays
//     the voice lines -- "#Gamemode_TER_SecuredCP" for the capturers (matched
//     by pawn +0x3F8), "#Gamemode_TER_Team/EnemySecuredCPAlpha/Beta/Charly"
//     (0x3843B0, by the letter A/B/C) for every other ship. Nothing in this
//     exe calls it (no direct caller; no vtable call reaches it): the server
//     build's capture handler did.
//   - A capture's own path (OnCaptured 0x55E0D0 -> the point's vtable +0x5F0
//     = 0x536510) rebuilds the map from the stale copy, and tells the GameState
//     only through the FIRST LOCAL player controller's CapturePointOwnerChanged
//     (vtable +0x1008, an empty `ret` in YPlayerController) -- the client's
//     minimap path, not the server's.
// So after a capture this calls 0x3A2610 with the point's owner (+0x450),
// station index (+0x3E4), m_UILetter (+0x3D0, FString) and the capturing
// team's ships' controllers in range; and once a second any point whose copy
// still disagrees (a neutralisation, or a path this does not hook) is synced
// with no capturers. The links (+0x1F98, m_linkableCPs, built in the
// GameState tick from the same entries) follow the copy too.
#define OFF_GS_TER_SOURCES 0x1FE8 // TArray of 0x40-byte influence sources, count +0x1FF0
#define TER_SOURCE_SIZE 0x40
#define VT_GS_TER_CP_OWNER_CHANGED 0x838
#define RVA_GS_TER_CP_OWNER_CHANGED 0x3A2610
#define OFF_CP_UI_LETTER 0x3D0
struct TArrayPtrMin {
  void **data;
  int32_t num, max;
};
typedef void(__fastcall *tGsCpOwnerChanged)(void *gs, uint32_t team, int32_t index, void *letter,
                                            TArrayPtrMin *capturers);

static bool ConquestPointOwnerEnabled() {
  static int s_on = -1;
  if (s_on < 0)
    s_on = SwitchOn("DN_HOST_NO_CONQUEST_POINT_OWNER", "dn_host_no_conquest_point_owner.txt") ? 0 : 1;
  return s_on == 1;
}

// Sync one point's influence-source team with its owner. Returns true when it
// called the GameState (the copy disagreed).
static bool SyncPointOwner(uint8_t *gs, uint8_t *cp, void **capturers, int numCapturers, const char *why) {
  if (!gs || !cp || !IsReadable(gs + OFF_GS_TER_SOURCES, 16) || !IsReadable(cp + OFF_CP_OWNER, 1) ||
      !IsReadable(cp + OFF_CP_STATION_INDEX, 4) || !IsReadable(cp + OFF_CP_UI_LETTER, 16))
    return false;
  // Only a YGameState_TER has 0x3A2610 in that slot.
  void **vt = *(void ***)gs;
  if (!IsReadable(vt + VT_GS_TER_CP_OWNER_CHANGED / 8, 8) ||
      vt[VT_GS_TER_CP_OWNER_CHANGED / 8] != (void *)(g_base + RVA_GS_TER_CP_OWNER_CHANGED))
    return false;
  uint8_t *src = *(uint8_t **)(gs + OFF_GS_TER_SOURCES);
  int count = *(int32_t *)(gs + OFF_GS_TER_SOURCES + 8);
  int idx = *(int32_t *)(cp + OFF_CP_STATION_INDEX);
  if (!src || idx < 0 || idx >= count || !IsReadable(src + (size_t)idx * TER_SOURCE_SIZE, 4))
    return false;
  uint32_t had = *(uint32_t *)(src + (size_t)idx * TER_SOURCE_SIZE);
  uint32_t owner = cp[OFF_CP_OWNER];
  if (had == owner)
    return false;
  int before1 = 0, before2 = 0, tiles = 0;
  CountTerritoryTiles(gs, OFF_GS_TER_TILE_MAP, &before1, &before2, &tiles);
  TArrayPtrMin arr = {capturers, numCapturers, numCapturers};
  ((tGsCpOwnerChanged)vt[VT_GS_TER_CP_OWNER_CHANGED / 8])(gs, owner, idx, cp + OFF_CP_UI_LETTER, &arr);
  int after1 = 0, after2 = 0;
  CountTerritoryTiles(gs, OFF_GS_TER_TILE_MAP, &after1, &after2, &tiles);
  ++g_ter.ownerSyncs;
  const char *nm = NameText(((UObjectMin *)cp)->Name);
  Logf("conquest: %s (index %d) owner %u -> %u (%s, %d capturer(s)) -- GameState told (0x3A2610); point map T1 %d -> "
       "%d, T2 %d -> %d tiles", nm ? nm : "?", idx, had, owner, why, numCapturers, before1, after1, before2, after2);
  return true;
}

// Tiles each team's ships claimed (the GameState's own per-ship counts).
static void CountShipTiles(uint8_t *gs, int *t1, int *t2, int *ships) {
  *t1 = *t2 = *ships = 0;
  uint8_t *data = *(uint8_t **)(gs + OFF_GS_TER_SHIPS);
  int num = *(int32_t *)(gs + OFF_GS_TER_SHIPS + 8);
  if (!data || num <= 0 || num > 256 || !IsReadable(data, (size_t)num * 8))
    return;
  for (int i = 0; i < num; ++i) {
    uint8_t *pawn = (uint8_t *)((tWeakGet)(g_base + RVA_WEAK_PTR_GET))(data + (size_t)i * 8);
    if (!pawn || !IsReadable(pawn + OFF_PAWN_TER_TILES, 4))
      continue;
    int tiles = *(int32_t *)(pawn + OFF_PAWN_TER_TILES);
    int team = ShipTeam(pawn);
    if (tiles <= 0 || !team)
      continue;
    ++*ships;
    (team == 1 ? *t1 : *t2) += tiles;
  }
}

// ---------------------------------------------------------------------------
// Conquest personal score (on; dn_host_no_ter_scoring.txt /
// DN_HOST_NO_TER_SCORING=1 turns it off)
//
// The scoring table has four Conquest rows -- TerritoryContribution
// ("TERRITORY HELD"), TerritoryCleared ("HARVESTER"), TerritoryCapturedPoint
// ("POINT SECURED"), TerritoryProtectCP ("POINT DEFENDED") -- and no Conquest
// host ever scored one. Verified in the exe 2026-10-28:
//   - They belong to UYScoringEventManager_Territory (class 0x614A90, parent
//     YScoringEventManager_MP, size 0x7C0). Its init (vtable +0x1E0 =
//     0x422980) runs the MP init (0x3FD6C0: the kill/assist/... handlers),
//     then binds CheckTerritoryCleared (0x416A30) and CheckProtectCP
//     (0x416430) to the manager's kill delegate (+0xA0, the one every kill
//     broadcasts), CheckTerritoryConquered (0x416D30) to +0x670, a no-op to
//     +0x6E0 (TerritoryCapturingPoint, no score) and
//     CheckTerritoryCapturedPoint (0x4168C0) to +0x750.
//   - NOTHING in the exe creates that class. Every multiplayer game mode makes
//     its manager in 0x369AF0 / 0x369A20 (two identical copies in different
//     game mode vtables): NewObject<YScoringEventManager_MP>(gm,
//     "ScoringEventManager") into gm+0x740, then vtable +0x1E0(mgr, world,
//     gm). YGameMode_Territory overrides nothing but its destructor, so the
//     server build's override that made the Territory manager is gone, and
//     Conquest has been running the plain MP manager.
//   - And nothing broadcasts +0x670 or +0x750: that was server game mode
//     code too.
// So: for a YGameMode_Territory, the creator makes the Territory manager
// instead (same outer, name, flags and init). Kills then reach the two kill
// checks on their own. Every score update (10 s) this calls
// CheckTerritoryConquered for each ship the GameState counts (gs+0x1E88), and
// after a capture (0x55E0D0, below) CheckTerritoryCapturedPoint for each
// controller of the new owner's ships in the point's radius -- the list the
// exe's own capture code builds for its owner-change delegate (0x542990:
// m_pawnsWithinRange +0x420 of that team -> APawn::Controller).
// The checks themselves are the game's: CheckTerritoryConquered scores when
// the ship holds more of its potential territory than the row's first value
// (0.6; GetPawnTerritoryPotentialClaimedPercentage 0x41FC80 = pawn +0x854,
// cut by the second value 0.75 for every ally whose radius it is in, over its
// own radius' share of the grid).
// GUESS: the conquered check runs at each score update (the original's
// interval is unknown; the ribbon wants 20 of them), and every capture scores
// for the capturers, neutral or enemy point alike (the check does not ask).
#define RVA_GM_CREATE_SCORING_MGR_A 0x369AF0
#define RVA_GM_CREATE_SCORING_MGR_B 0x369A20
#define RVA_SCORING_MGR_TERRITORY_CLASS 0x614A90 // StaticClass(L"/Script/DreadGame")
#define RVA_STATIC_CONSTRUCT_OBJECT 0xD759E0     // as 0x369AF0 calls it
#define RVA_FNAME_FROM_WIDE 0xC9CFA0             // FName(const TCHAR*, FNAME_Add)
#define RVA_TER_CHECK_CONQUERED 0x416D30
#define RVA_TER_CHECK_CAPTURED_POINT 0x4168C0
#define RVA_CP_ON_CAPTURED 0x55E0D0              // (cp, bool silent)
#define RVA_PAWN_GET_HEALTH 0x579170             // leaf: health component +0x9A8 -> +0x1D8
#define OFF_GM_SCORING_MGR 0x740
#define VT_SCORING_MGR_INIT 0x1E0
#define OFF_CP_PAWNS_IN_RANGE 0x420              // TArray<APawn*> m_pawnsWithinRange
#define TER_SCORING_EVENT_SIZE 0x40              // the checks read +0x08 (controller)
// GetPawnTerritoryPotentialClaimedPercentage (mgr, pawn, crowding factor):
// pawn +0x854 (its claimed tiles / the grid, set by the GameState's ship pass
// at 0x394E84) over pi r^2 / grid area, r from gs+0x2020 by ship class.
#define RVA_TER_POTENTIAL_CLAIMED 0x41FC80
typedef float(__fastcall *tTerPotentialClaimed)(void *mgr, void *pawn, float crowding);
typedef void(__fastcall *tGmCreateScoringMgr)(void *gm);
typedef void *(__fastcall *tStaticClassFn)(const wchar_t *package);
typedef void *(__fastcall *tStaticConstructObject)(void *cls, void *outer, uint64_t name, uint32_t flags,
                                                   uint32_t internalFlags, void *tmpl, bool copyTransients,
                                                   void *instancingGraph);
typedef uint64_t *(__fastcall *tFNameFromWide)(uint64_t *out, const wchar_t *s, int find);
typedef void(__fastcall *tTerCheck)(void *mgr, void *event);
typedef float(__fastcall *tPawnGetHealth)(void *pawn);
typedef void(__fastcall *tCpOnCaptured)(void *cp, char silent);
static tGmCreateScoringMgr g_origCreateScoringMgrA = nullptr;
static tGmCreateScoringMgr g_origCreateScoringMgrB = nullptr;
static tCpOnCaptured g_origCpOnCaptured = nullptr;

static void *TerritoryScoringClass() {
  static void *s_cls = nullptr;
  if (!s_cls)
    s_cls = ((tStaticClassFn)(g_base + RVA_SCORING_MGR_TERRITORY_CLASS))(L"/Script/DreadGame");
  return s_cls;
}

// The Territory manager of this game mode, or null.
static void *TerritoryScoringManager(uint8_t *gm) {
  if (!gm || !IsReadable(gm + OFF_GM_SCORING_MGR, 8))
    return nullptr;
  UObjectMin *mgr = *(UObjectMin **)(gm + OFF_GM_SCORING_MGR);
  void *cls = TerritoryScoringClass();
  return mgr && cls && IsReadable(mgr, sizeof(UObjectMin)) && mgr->Class == cls ? mgr : nullptr;
}

static bool CreateTerritoryScoringManager(uint8_t *gm) {
  if (!ClassChainContains((UObjectMin *)gm, "YGameMode_Territory"))
    return false;
  void *cls = TerritoryScoringClass();
  if (!cls) {
    Logf("conquest scoring: YScoringEventManager_Territory class not found -- the MP manager stays");
    return false;
  }
  uint64_t name = 0;
  ((tFNameFromWide)(g_base + RVA_FNAME_FROM_WIDE))(&name, L"ScoringEventManager", 1);
  void *mgr = ((tStaticConstructObject)(g_base + RVA_STATIC_CONSTRUCT_OBJECT))(cls, gm, name, 0, 0, nullptr, false, nullptr);
  *(void **)(gm + OFF_GM_SCORING_MGR) = mgr;
  if (mgr) {
    void *world = ((tGetWorld)(*(void ***)gm)[VT_ACTOR_GET_WORLD / 8])(gm);
    ((void(__fastcall *)(void *, void *, void *))(*(void ***)mgr)[VT_SCORING_MGR_INIT / 8])(mgr, world, gm);
  }
  Logf("conquest scoring: game mode %p -- scoring manager %p %s (the server build's Territory manager; "
       "enabled %d)", gm, mgr, ClassNameOf(mgr), mgr ? ((uint8_t *)mgr)[0x5E0] : -1);
  return true;
}

static bool TerScoringEnabled() {
  static int s_on = -1;
  if (s_on < 0)
    s_on = SwitchOn("DN_HOST_NO_TER_SCORING", "dn_host_no_ter_scoring.txt") ? 0 : 1;
  return s_on == 1;
}

static void __fastcall HookCreateScoringMgrA(void *gm) {
  bool done = false;
  __try {
    done = TerScoringEnabled() && CreateTerritoryScoringManager((uint8_t *)gm);
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    Logf("conquest scoring: EXCEPTION 0x%08X creating the Territory manager", GetExceptionCode());
  }
  if (!done)
    g_origCreateScoringMgrA(gm);
}

static void __fastcall HookCreateScoringMgrB(void *gm) {
  bool done = false;
  __try {
    done = TerScoringEnabled() && CreateTerritoryScoringManager((uint8_t *)gm);
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    Logf("conquest scoring: EXCEPTION 0x%08X creating the Territory manager", GetExceptionCode());
  }
  if (!done)
    g_origCreateScoringMgrB(gm);
}

// One Territory check for one controller (the event's +0x08 weak pointer).
static void TerCheckFor(void *mgr, uintptr_t rva, void *controller) {
  alignas(16) uint8_t ev[TER_SCORING_EVENT_SIZE] = {};
  ((tWeakSet)(g_base + RVA_WEAK_PTR_SET))(ev + 8, controller);
  ((tTerCheck)(g_base + rva))(mgr, ev);
}

// CheckTerritoryConquered for every live ship the GameState counts.
static int TerContributionPass(uint8_t *gm, uint8_t *gs) {
  void *mgr = TerritoryScoringManager(gm);
  if (!mgr)
    return 0;
  uint8_t *data = *(uint8_t **)(gs + OFF_GS_TER_SHIPS);
  int num = *(int32_t *)(gs + OFF_GS_TER_SHIPS + 8);
  if (!data || num <= 0 || num > 256 || !IsReadable(data, (size_t)num * 8))
    return 0;
  int calls = 0;
  for (int i = 0; i < num; ++i) {
    uint8_t *pawn = (uint8_t *)((tWeakGet)(g_base + RVA_WEAK_PTR_GET))(data + (size_t)i * 8);
    if (!pawn || !IsReadable(pawn + OFF_PAWN_CONTROLLER, 8) || !ShipTeam(pawn))
      continue;
    void *ctrl = *(void **)(pawn + OFF_PAWN_CONTROLLER);
    if (!ctrl || !(((tPawnGetHealth)(g_base + RVA_PAWN_GET_HEALTH))(pawn) > 0.0f))
      continue;
    TerCheckFor(mgr, RVA_TER_CHECK_CONQUERED, ctrl);
    ++calls;
    // Diagnostic for "TerritoryContribution never awards" (0 of 3,890 checks
    // in the two 10-29 Skybridge matches): the check scores when this ratio
    // (the ship's claimed share over its potential, allies within their
    // radius cutting it by the row's second value) beats the row's first,
    // VAL(0.6;0.75). The 0.75 here is that table value, for the log only.
    float ratio = ((tTerPotentialClaimed)(g_base + RVA_TER_POTENTIAL_CLAIMED))(mgr, pawn, 0.75f);
    if (ratio == ratio && ratio > g_ter.bestPotential)
      g_ter.bestPotential = ratio;
  }
  return calls;
}

// YCapturePoint::OnCaptured (0x55E0D0, silent = the setup path): after the
// game's own work, CheckTerritoryCapturedPoint for each capturer.
static void __fastcall HookCpOnCaptured(void *cp, char silent) {
  g_origCpOnCaptured(cp, silent);
  if (silent)
    return;
  __try {
    uint8_t *gm = (uint8_t *)g_ter.gm;
    uint8_t *p = (uint8_t *)cp;
    if (!gm || !IsReadable(p + OFF_CP_OWNER, 1) || !IsReadable(p + OFF_CP_PAWNS_IN_RANGE, 16))
      return;
    int team = p[OFF_CP_OWNER];
    uint8_t **pawns = *(uint8_t ***)(p + OFF_CP_PAWNS_IN_RANGE);
    int num = *(int32_t *)(p + OFF_CP_PAWNS_IN_RANGE + 8);
    // The capturing team's ships in range, by controller.
    void *ctrls[64];
    const char *kinds[64];
    int nCtrls = 0;
    if ((team == 1 || team == 2) && pawns && num > 0 && num <= 128 && IsReadable(pawns, (size_t)num * 8))
      for (int i = 0; i < num && nCtrls < 64; ++i) {
        uint8_t *pawn = pawns[i];
        if (!pawn || ShipTeam(pawn) != team || !IsReadable(pawn + OFF_PAWN_CONTROLLER, 8))
          continue;
        void *ctrl = *(void **)(pawn + OFF_PAWN_CONTROLLER);
        if (!ctrl)
          continue;
        kinds[nCtrls] = ClassNameOf(ctrl);
        ctrls[nCtrls++] = ctrl;
      }
    if (ConquestPointOwnerEnabled() && IsReadable(gm + OFF_GM_GAMESTATE, 8))
      SyncPointOwner(*(uint8_t **)(gm + OFF_GM_GAMESTATE), p, ctrls, nCtrls, "captured");
    void *mgr = TerScoringEnabled() ? TerritoryScoringManager(gm) : nullptr;
    if (!mgr)
      return;
    for (int i = 0; i < nCtrls; ++i)
      TerCheckFor(mgr, RVA_TER_CHECK_CAPTURED_POINT, ctrls[i]);
    g_ter.captureCalls += nCtrls;
    Logf("conquest scoring: %s captured by team %d -- TerritoryCapturedPoint checked for %d capturer(s) of %d in range "
         "(%s%s%s)", NameText(((UObjectMin *)cp)->Name) ? NameText(((UObjectMin *)cp)->Name) : "?", team, nCtrls, num,
         nCtrls > 0 ? kinds[0] : "none", nCtrls > 1 ? ", " : "", nCtrls > 1 ? kinds[1] : "");
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    Logf("conquest scoring: EXCEPTION 0x%08X after a capture", GetExceptionCode());
  }
}

// Conquest spawns (on; dn_host_no_conquest_spawns.txt /
// DN_HOST_NO_CONQUEST_SPAWNS=1 turns it off)
//
// Player report 2026-10-29 (Skybridge): "I spawned into enemy team twice in
// this match and have seen enemies spawn near ours." Verified in the exe and
// the maps the same day:
//   - Respawns go through YGameMode's player-start chooser 0x36C1A0 (vtable
//     +0x5F0 of every multiplayer mode; "Spawning a pawn for player" 25 times
//     for one player in that match). It takes every active YPlayerStart whose
//     m_team (+0x3F0) is none or the player's team (PC +0xC30), asks the mode
//     whether the start suits it (vtable +0x8B8), then rates them
//     (m_useSpawnScoring, +0x6A3) or, with that off, picks one AT RANDOM.
//   - GameMode_TER_BP is the only mode Blueprint that turns m_useSpawnScoring
//     off, and YGameMode_Territory's +0x8B8 is the shared `mov al,1; ret` stub
//     0x15FBF00, as for every mode but Invasion -- Invasion's own (0x3756A0)
//     admits a start only when its m_gameMode (+0x418) is 1..2 (YGMF_ALL,
//     YGMF_TDM).
//   - Each map's _Territory sublevel places its own starts, tagged
//     m_gameMode YGMF_TER and a team (Skybridge 8 + 8, Gorge 13 + 13; enum
//     YGMF_NONE/ALL/TDM/TE/TER = 0..4, the YPlayerStart ctor 0x5A8E50 defaults
//     to ALL). The persistent level keeps the other modes' starts, all active
//     (ActivateBattlePlayerStarts 0x3B54D0 enables every start that is not an
//     orbit start): MP_Skybridge_P has 90 YGMF_TDM and 47 YGMF_TE starts with
//     NO team, spread over the whole map, so a Conquest respawn landed on a
//     TER start of its own team about 8 times in 160.
// So for a YGameMode_Territory this hides (m_active +0x424 off) every start
// tagged for another mode around the game's own chooser, restoring each one
// after; the rest of the choice is the game's. GUESS: the server build's
// Territory mode filtered like Invasion's, for YGMF_ALL and YGMF_TER; the
// exe has only the stub, but the designers tagged every Conquest start TER
// and switched the rating off. If nothing is left (all of the team's starts
// blocked), the unfiltered choice stands.
#define RVA_GM_CHOOSE_PLAYER_START 0x36C1A0
#define OFF_CLASSPTR_PLAYER_START 0x3E11F80
#define OFF_PS_TEAM 0x3F0
#define OFF_PS_GAME_MODE 0x418
#define OFF_PS_ACTIVE 0x424
#define YGMF_ALL 1
#define YGMF_TER 4
typedef void *(__fastcall *tChoosePlayerStart)(void *gm, void *ctrl);
static tChoosePlayerStart g_origChoosePlayerStart = nullptr;

static bool ConquestSpawnsEnabled() {
  static int s_on = -1;
  if (s_on < 0)
    s_on = SwitchOn("DN_HOST_NO_CONQUEST_SPAWNS", "dn_host_no_conquest_spawns.txt") ? 0 : 1;
  return s_on == 1;
}

static bool IsTerritoryMode(void *gm) {
  static void *s_gm = nullptr;
  static bool s_ter = false;
  if (gm != s_gm) {
    s_gm = gm;
    s_ter = gm && ClassChainContains((UObjectMin *)gm, "YGameMode_Territory");
  }
  return s_ter;
}

// The starts are reshaped around the game's own chooser by byte patches
// (m_active, m_onlyFighterJets, m_team), each put back right after.
#define OFF_PS_ONLY_JETS 0x425 // m_onlyFighterJets: the chooser skips such starts
#define YGMF_TE 3
struct StartPatch {
  uint8_t *p;
  uint16_t off;
  uint8_t old;
};
static StartPatch g_startPatches[2048];
static int g_numStartPatches = 0;
static void PatchStart(uint8_t *ps, int off, uint8_t v) {
  if (ps[off] == v || g_numStartPatches >= 2048)
    return;
  g_startPatches[g_numStartPatches++] = {ps, (uint16_t)off, ps[off]};
  ps[off] = v;
}
static void RestoreStarts() {
  while (g_numStartPatches > 0) {
    StartPatch &sp = g_startPatches[--g_numStartPatches];
    sp.p[sp.off] = sp.old;
  }
}
// Every live YPlayerStart; returns how many (at most cap).
static int AllPlayerStarts(uint8_t **out, int cap) {
  void *cls = *(void **)(g_base + OFF_CLASSPTR_PLAYER_START);
  FUObjectArrayMin *arr = GObjects();
  if (!cls || !arr || !arr->Objects)
    return 0;
  int n = 0;
  for (int i = 0; i < arr->NumElements && n < cap; ++i) {
    UObjectMin *o = arr->Objects[i].Object;
    if (!o || (o->Flags & RF_CDO_OR_ARCHETYPE) || (arr->Objects[i].Flags & OBJ_PENDING_KILL) ||
        !ClassIsAncestor(o->Class, cls))
      continue;
    out[n++] = (uint8_t *)o;
  }
  return n;
}

// Conquest: hide every active start tagged for another mode.
static void PrepareConquestStarts(int *hidden, int *ours) {
  static uint8_t *s_all[1024];
  int n = AllPlayerStarts(s_all, 1024);
  *hidden = *ours = 0;
  for (int i = 0; i < n; ++i) {
    uint8_t *ps = s_all[i];
    uint8_t mode = ps[OFF_PS_GAME_MODE];
    if (mode == YGMF_ALL || mode == YGMF_TER) {
      *ours += ps[OFF_PS_ACTIVE] ? 1 : 0;
      continue;
    }
    if (ps[OFF_PS_ACTIVE]) {
      PatchStart(ps, OFF_PS_ACTIVE, 0);
      ++*hidden;
    }
  }
}

static bool IsTeamEliminationMode(void *gm);
static const char *PrepareTeStarts(void *ctrl, int *hidden, int *ours); // Team Elimination section

static void *__fastcall HookChoosePlayerStart(void *gm, void *ctrl) {
  bool ter = IsTerritoryMode(gm), te = !ter && IsTeamEliminationMode(gm);
  if (!ter && !te)
    return g_origChoosePlayerStart(gm, ctrl);
  int hidden = 0, ours = 0;
  const char *rule = "ALL/TER";
  __try {
    if (ter)
      PrepareConquestStarts(&hidden, &ours);
    else
      rule = PrepareTeStarts(ctrl, &hidden, &ours);
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    Logf("spawns: EXCEPTION 0x%08X shaping the player starts", GetExceptionCode());
  }
  bool patched = g_numStartPatches > 0;
  void *start = g_origChoosePlayerStart(gm, ctrl);
  RestoreStarts();
  bool fallback = false;
  if (!start && patched) {
    fallback = true;
    start = g_origChoosePlayerStart(gm, ctrl);
  }
  static void *s_logGm = nullptr;
  static int s_logged = 0;
  if (s_logGm != gm) {
    s_logGm = gm;
    s_logged = 0;
  }
  __try {
    if (s_logged < 40 || fallback) {
      ++s_logged;
      uint8_t *ps = (uint8_t *)start;
      const char *nm = ps ? NameText(((UObjectMin *)ps)->Name) : nullptr;
      Logf("%s spawns: %s %p -> %s (mode %d, team %d, jets only %d)%s; %d start(s) hidden, %d %s start(s) usable",
           ter ? "conquest" : "te", ClassNameOf(ctrl), ctrl, ps ? (nm ? nm : "?") : "none",
           ps ? ps[OFF_PS_GAME_MODE] : -1, ps ? ps[OFF_PS_TEAM] : -1, ps ? ps[OFF_PS_ONLY_JETS] : -1,
           fallback ? " -- FALLBACK: no start left under the rule, unfiltered choice" : "", hidden, ours, rule);
    }
  } __except (EXCEPTION_EXECUTE_HANDLER) {
  }
  return start;
}

static bool ConquestClaimedShare() {
  static int s_on = -1;
  if (s_on < 0)
    s_on = SwitchOn("DN_HOST_CONQUEST_CLAIMED_SHARE", "dn_host_conquest_claimed_share.txt") ? 1 : 0;
  return s_on == 1;
}

static void ConquestTick(uint8_t *gm) {
  static int s_enabled = -1;
  if (s_enabled < 0)
    s_enabled = ConquestScoreEnabled() ? 1 : 0;
  if (!s_enabled)
    return;
  __try {
    if (!IsReadable(gm + OFF_GM_GAMESTATE, 8) || !IsReadable(gm + OFF_GM_TER_SCORE_INTERVAL, 4))
      return;
    uint8_t *gs = *(uint8_t **)(gm + OFF_GM_GAMESTATE);
    if (!gs || !IsReadable(gs + OFF_GS_GAME_MODE_TYPE, 1) || gs[OFF_GS_GAME_MODE_TYPE] != YGMT_TER ||
        !IsReadable(gs + OFF_GS_TER_SCORE_T1, OFF_GS_TER_VOLUME + 8 - OFF_GS_TER_SCORE_T1))
      return;
    DWORD now = GetTickCount();
    int target = *(int32_t *)(gm + OFF_GM_TER_SCORE_FOR_WINNING);
    if (target <= 0 || target > 1000000)
      target = 2500; // GameMode_TER_BP
    float every = *(float *)(gm + OFF_GM_TER_SCORE_INTERVAL);
    if (!(every >= 1.0f && every <= 600.0f))
      every = 10.0f; // GameMode_TER_BP
    float mult = *(float *)(gm + OFF_GM_TER_SCORE_MULTIPLIER);
    float useMult = (mult > 0.0f && mult <= 100.0f) ? mult : 1.0f;
    int threshold = *(int32_t *)(gm + OFF_GM_TER_SCORE_THRESHOLD);
    uint8_t *vol = *(uint8_t **)(gs + OFF_GS_TER_VOLUME);
    float gw = 0, gh = 0;
    if (vol && IsReadable(vol + OFF_TER_VOLUME_GRID, 8)) {
      gw = *(float *)(vol + OFF_TER_VOLUME_GRID);
      gh = *(float *)(vol + OFF_TER_VOLUME_GRID + 4);
    }
    if (g_ter.gm != gm) {
      memset(&g_ter, 0, sizeof(g_ter));
      g_ter.gm = gm;
      g_ter.lastScore = now;
      g_ter.armedAt = now;
      *(int32_t *)(gs + OFF_GS_TER_SCORE_FOR_WINNING) = target;
      Logf("conquest: match %p -- score rule armed: %d to win, every %.1f s, multiplier %.3f (using %.3f), "
           "threshold %d; territory volume %p grid %.0f x %.0f; m_maxMatchTime %.2f min (+0x884; GameMode_TER_BP "
           "sets none, the other modes 20); territory %% of %s; linking %d; scoring manager %s",
           gm, target, every, mult, useMult, threshold, vol, gw, gh, *(float *)(gm + 0x884),
           ConquestClaimedShare() ? "the CLAIMED tiles (dn_host_conquest_claimed_share.txt)" : "the whole grid",
           gs[0x1E2F], IsReadable(gm + OFF_GM_SCORING_MGR, 8) ? ClassNameOf(*(void **)(gm + OFF_GM_SCORING_MGR)) : "?");
    }
    if (g_ter.ended)
      return;
    if (!gs[OFF_GS_MATCH_RUNNING] || gs[OFF_GS_MATCH_OVER]) {
      // Test switch for a headless host with no player
      // (dn_host_conquest_smoke.txt): start the match 20 s after arming
      // (AGameMode::StartMatch, vtable +0x660 -- the StartMatch exec thunk
      // 0x1EBD440 jumps there -- then the GameState's match-started setter
      // 0x380F70, which the pre-match countdown would call), so the capture
      // points, the territory map and the score run without anyone joining.
      static int s_smoke = -1;
      if (s_smoke < 0)
        s_smoke = SwitchOn("DN_HOST_CONQUEST_SMOKE", "dn_host_conquest_smoke.txt") ? 1 : 0;
      static void *s_smokeStarted = nullptr;
      if (s_smoke && !gs[OFF_GS_MATCH_OVER] && s_smokeStarted != gm && now - g_ter.armedAt > 20000) {
        s_smokeStarted = gm;
        Logf("conquest: SMOKE -- starting match %p with nobody joined (dn_host_conquest_smoke.txt)", gm);
        ((void(__fastcall *)(void *))(*(void ***)gm)[0x660 / 8])(gm);
        ((void(__fastcall *)(void *, char))(g_base + 0x380F70))(gs, 1);
      }
      g_ter.lastScore = now; // the score clock runs only while the match does
      return;
    }
    if (!g_ter.pointsReady && now - g_ter.lastPointScan >= 1000) {
      g_ter.lastPointScan = now;
      g_ter.pointsReady = SetUpCapturePoints(gs);
    }
    // Smoke only: 30 s into the match, give the first unlocked point to team 1
    // the way a capture's owner change does (SetCapturePoint), so the sync
    // below runs with nobody joined.
    static void *s_smokeFlipped = nullptr;
    if (g_ter.pointsReady && s_smokeFlipped != gm && now - g_ter.armedAt > 70000 &&
        SwitchOn("DN_HOST_CONQUEST_SMOKE", "dn_host_conquest_smoke.txt"))
      for (int i = 0; i < g_ter.numCps; ++i)
        if (!g_ter.cps[i][OFF_CP_LOCKED]) {
          s_smokeFlipped = gm;
          Logf("conquest: SMOKE -- giving %s to team 1", NameText(((UObjectMin *)g_ter.cps[i])->Name));
          ((tCpSetOwner)(g_base + RVA_CP_SET_OWNER))(g_ter.cps[i], 1);
          break;
        }
    if (g_ter.pointsReady && ConquestPointOwnerEnabled())
      for (int i = 0; i < g_ter.numCps; ++i)
        SyncPointOwner(gs, g_ter.cps[i], nullptr, 0, "owner changed outside a capture");
    // The territory map (+0x1EA8, see above). Before the GameState's first
    // full pass it is empty; the point map stands in until then.
    int t1 = 0, t2 = 0, mapTiles = 0, p1 = 0, p2 = 0, pointTiles = 0, s1 = 0, s2 = 0, ships = 0;
    bool ok = CountTerritoryTiles(gs, OFF_GS_TER_TERRITORY_MAP, &t1, &t2, &mapTiles);
    bool pointsOk = CountTerritoryTiles(gs, OFF_GS_TER_TILE_MAP, &p1, &p2, &pointTiles);
    CountShipTiles(gs, &s1, &s2, &ships);
    if (!ok || mapTiles == 0) {
      ok = pointsOk;
      t1 = p1;
      t2 = p2;
    }
    float total = gw * gh;
    int pct1 = 0, pct2 = 0;
    if (ConquestClaimedShare()) {
      // The operator's 2026-10-08 rule: a team's share of the claimed tiles
      // (the two add up to 100).
      if (ok && t1 + t2 > 0) {
        pct1 = (int)(100.0f * t1 / (t1 + t2) + 0.5f);
        pct2 = 100 - pct1;
      }
    } else if (ok && total > 0.0f) {
      pct1 = (int)(100.0f * t1 / total + 0.5f);
      pct2 = (int)(100.0f * t2 / total + 0.5f);
    }
    int32_t *terr = (int32_t *)(gs + OFF_GS_TER_TERRITORY_T1);
    bool changed = terr[0] != pct1 || terr[1] != pct2;
    terr[0] = pct1;
    terr[1] = pct2;
    int32_t *score = (int32_t *)(gs + OFF_GS_TER_SCORE_T1);
    if (now - g_ter.lastScore >= (DWORD)(every * 1000.0f)) {
      g_ter.lastScore = now;
      if (pct1 > threshold)
        score[0] += (int)(pct1 * useMult + 0.5f);
      if (pct2 > threshold)
        score[1] += (int)(pct2 * useMult + 0.5f);
      changed = true;
      int checked = TerScoringEnabled() ? TerContributionPass(gm, gs) : 0;
      ++g_ter.contributionTicks;
      g_ter.contributionCalls += checked;
      Logf("conquest: territory T1 %d%% T2 %d%% (%d/%d of %.0f tiles; %d in the territory map, points %d/%d of %d, "
           "ships' own claims %d/%d from %d) -> score T1 %d T2 %d / %d; TerritoryContribution checked for %d ship(s), "
           "best claimed/potential so far %.2f (scores above 0.60)",
           pct1, pct2, t1, t2, total, mapTiles, p1, p2, pointTiles, s1, s2, ships, score[0], score[1], target, checked,
           g_ter.bestPotential);
    } else if (now - g_ter.lastLog > 30000) {
      g_ter.lastLog = now;
      Logf("conquest: territory T1 %d%% T2 %d%% (%d/%d tiles of %.0f, %d in the territory map%s)", pct1, pct2, t1,
           t2, total, mapTiles, ok ? "" : ", map UNREADABLE");
    }
    if (changed)
      ((tActorVoidFn)(*(void ***)gs)[0x580 / 8])(gs); // AActor::ForceNetUpdate
    if (score[0] >= target || score[1] >= target) {
      g_ter.ended = true;
      gs[OFF_GS_WINNING_TEAM] = score[0] > score[1] ? 1 : score[1] > score[0] ? 2 : 3;
      Logf("conquest: team %d reached %d (T1 %d, T2 %d) -- ending the match; TerritoryContribution checks %d over "
           "%d updates (best claimed/potential %.2f), TerritoryCapturedPoint checks %d, point owner syncs %d",
           gs[OFF_GS_WINNING_TEAM], target, score[0], score[1], g_ter.contributionCalls, g_ter.contributionTicks,
           g_ter.bestPotential, g_ter.captureCalls, g_ter.ownerSyncs);
      void **vt = *(void ***)gm;
      ((void(__fastcall *)(void *))vt[VT_GM_END_MATCH / 8])(gm);
    }
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    Logf("conquest: EXCEPTION 0x%08X in the score tick", GetExceptionCode());
  }
}

// ---------------------------------------------------------------------------
// Bots spawned before the match starts (on; dn_host_no_npc_ai_start.txt turns
// it off)
//
// Operator report 2026-10-08: Conquest and Onslaught bots "just sit still and
// ignore the objective". Verified in the exe the same day:
//   - An NPC controller is born halted (+0xAFA = 1, ctor 0x276EA0) until
//     0x288AE0 starts its AI (see the Onslaught AI ships above).
//   - The scene manager's NPC spawn (0x2693A0) calls 0x288AE0 itself only when
//     the game mode is NOT a YGameMode_Multiplayer (class 0x601440). For the
//     multiplayer modes it leaves the start to the game mode's NPC-spawned
//     handler (vtable +0x978 = 0x378DE0, called only at 0x26A438, in that
//     spawn). That handler starts it only if the match already runs
//     (GameState +0x569), and the pawn's vtable +0x7D8 agrees.
//   - The bots are spawned when the pre-match countdown reaches 50 s, before
//     the match runs. So no one starts them, and they stay halted until they
//     die and respawn mid-match.
// So once a second, while the match runs, every scene-manager NPC (table
// +0x3C0, 0xA8-byte entries, weak pawn +0x8C) whose controller is still halted
// gets exactly what 0x378DE0 would have given it.
#define OFF_SCENE_NPC_PAWN 0x8C
#define VT_PAWN_NPC_AI_OK 0x7D8
typedef bool(__fastcall *tPawnBoolFn)(void *pawn);

// Diagnostic (2026-10-09): two Amirani Onslaught matches logged 47,490 and
// 119,500 "Native NetSerialize ... ReplicatedMovement ... failed" (about 150 a
// second for the last minutes; Gorge 0). FRepMovement fails to pack a location
// or velocity beyond about +-2^19, so an actor left the playable space and its
// movement stopped replicating. The log does not say which actor. This names
// any bot or Onslaught ship that is that far out, or has a NaN position, once
// per actor.
static void ReportFarAway(uint8_t *pawn, const char *what) {
  uint8_t *comp = IsReadable(pawn + 0x198, 8) ? *(uint8_t **)(pawn + 0x198) : nullptr;
  if (!comp || !IsReadable(comp + 0x160, 12))
    return;
  float x = *(float *)(comp + 0x160), y = *(float *)(comp + 0x164), z = *(float *)(comp + 0x168);
  bool bad = x != x || y != y || z != z || fabsf(x) > 500000.f || fabsf(y) > 500000.f || fabsf(z) > 500000.f;
  if (!bad)
    return;
  static void *s_seen[64];
  static int s_n = 0;
  for (int i = 0; i < s_n; ++i)
    if (s_seen[i] == pawn)
      return;
  // Once the list is full, stop: an unlisted actor would otherwise be logged
  // again on every tick.
  if (s_n >= 64)
    return;
  s_seen[s_n++] = pawn;
  Logf("far away: %s %s %p at (%.0f, %.0f, %.0f) -- its movement no longer replicates (RepMovement packing)", what,
       ClassNameOf(pawn), pawn, x, y, z);
}

static void NpcAiStartTick(uint8_t *gm) {
  static int s_enabled = -1;
  if (s_enabled < 0)
    s_enabled = SwitchOn("DN_HOST_NO_NPC_AI_START", "dn_host_no_npc_ai_start.txt") ? 0 : 1;
  if (!s_enabled)
    return;
  __try {
    if (!IsReadable(gm + OFF_GM_GAMESTATE, 8))
      return;
    uint8_t *gs = *(uint8_t **)(gm + OFF_GM_GAMESTATE);
    if (!gs || !IsReadable(gs + OFF_GS_MATCH_RUNNING, 2) || !gs[OFF_GS_MATCH_RUNNING] || gs[OFF_GS_MATCH_OVER])
      return;
    uint8_t *sm = (uint8_t *)((tGetSceneManager)(g_base + RVA_COMBAT_SCENE_MANAGER))();
    if (!sm || !IsReadable(sm + OFF_SCENE_NPCS, 16))
      return;
    uint8_t *data = *(uint8_t **)(sm + OFF_SCENE_NPCS);
    int n = *(int32_t *)(sm + OFF_SCENE_NPCS_COUNT);
    if (!data || n <= 0 || n > 4096 || !IsReadable(data, (size_t)n * NPC_SPAWN_SIZE))
      return;
    void *npcCls = *(void **)(g_base + OFF_CLASSPTR_NPC_AI_CONTROLLER);
    static int s_started = 0;
    // Diagnostic (Conquest, 2026-10-29: bots "standing still 9k meters away
    // from the playable map"): the actor each scene NPC slot spawns at -- the
    // entry's +0x30, whose root component (+0x198) gives the scene manager's
    // spawn transform (0x2693A0) -- once per slot and match.
    static void *s_spawnLogGm = nullptr;
    static uint8_t s_spawnLogged[256];
    if (s_spawnLogGm != gm) {
      s_spawnLogGm = gm;
      memset(s_spawnLogged, 0, sizeof(s_spawnLogged));
    }
    if (IsReadable(gs + OFF_GS_GAME_MODE_TYPE, 1) && gs[OFF_GS_GAME_MODE_TYPE] == YGMT_TER)
      for (int i = 0; i < n && i < 256; ++i) {
        uint8_t *at = *(uint8_t **)(data + (size_t)i * NPC_SPAWN_SIZE + 0x30);
        if (s_spawnLogged[i] || !at || !IsReadable(at, sizeof(UObjectMin)))
          continue;
        s_spawnLogged[i] = 1;
        uint8_t *comp = IsReadable(at + 0x198, 8) ? *(uint8_t **)(at + 0x198) : nullptr;
        float x = 0, y = 0, z = 0;
        if (comp && IsReadable(comp + 0x160, 12)) {
          x = *(float *)(comp + 0x160);
          y = *(float *)(comp + 0x164);
          z = *(float *)(comp + 0x168);
        }
        void *psCls = *(void **)(g_base + OFF_CLASSPTR_PLAYER_START);
        bool isStart = psCls && ClassIsAncestor(((UObjectMin *)at)->Class, psCls);
        const char *nm = NameText(((UObjectMin *)at)->Name);
        Logf("bots: conquest NPC slot %d spawns at %s (%s%s) (%.0f, %.0f, %.0f) mode %d team %d", i, nm ? nm : "?",
             ClassNameOf(at), isStart ? ", a player start" : "", x, y, z, isStart ? at[OFF_PS_GAME_MODE] : -1,
             isStart ? at[OFF_PS_TEAM] : -1);
      }
    for (int i = 0; i < n; ++i) {
      uint8_t *pawn = (uint8_t *)((tWeakGet)(g_base + RVA_WEAK_PTR_GET))(data + (size_t)i * NPC_SPAWN_SIZE + OFF_SCENE_NPC_PAWN);
      if (!pawn || !IsReadable(pawn + OFF_PAWN_CONTROLLER, 8))
        continue;
      ReportFarAway(pawn, "scene NPC");
      uint8_t *ctrl = *(uint8_t **)(pawn + OFF_PAWN_CONTROLLER);
      if (!ctrl || !IsReadable(ctrl + OFF_NPC_AI_HALTED, 1) || !ctrl[OFF_NPC_AI_HALTED] ||
          !ClassIsAncestor(((UObjectMin *)ctrl)->Class, npcCls))
        continue;
      if (!((tPawnBoolFn)(*(void ***)pawn)[VT_PAWN_NPC_AI_OK / 8])(pawn))
        continue;
      ((tNpcStartAI)(g_base + RVA_NPC_START_AI))(ctrl);
      if (++s_started <= 40)
        Logf("bots: NPC %d %s (%s) was spawned before the match started -- AI started", i, ClassNameOf(pawn),
             ClassNameOf(ctrl));
    }
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    Logf("bots: EXCEPTION 0x%08X in the NPC AI start tick", GetExceptionCode());
  }
}

// ---------------------------------------------------------------------------
// Veteran / Legendary command ship guns (on; dn_host_no_ons_gun_rows.txt turns
// it off)
//
// Operator report 2026-10-08: the command ship shoots in Recruit Onslaught
// but not in Veteran or Legendary. Verified in the cooked files the same day:
//   - The tiers' command ships (GameInfo_Onslaught_BP m_bigCreepClass / Vet /
//     Leg = VH_ONS_AITargetH_Pawn_BP / _Pawn_V_BP / _Pawn_L_BP) carry
//     WP_ONS_AITargetH_weapon01 / 02 / 03.
//   - The three weapon Blueprints are identical except their projectile row,
//     proj01 / 02 / 03. Every field of the three projectile rows matches
//     except the damage: 400/200/100, 700/300/150, 1000/500/200.
//   - DN_Weapons_OTS_DT has a row for weapon01 only. The host gets no tuning
//     (it never logs in), so its weapon groups fall back to the cooked table,
//     and every Veteran / Legendary match logged "Weapon Data for
//     'WP_ONS_AITargetH_weapon02_BP' Couldn't be found": that gun has no
//     weapon row and never fires.
// So when UYWeaponGroup::LoadWeaponDataTableRow (0x518EE0; the row name is the
// FString at +0x2B8, the row is cached at +0x2B0) finds nothing for weapon02 or
// weapon03, it is asked once more under weapon01's name. The weapon row is the
// gun's mechanics. The tier's damage stays in its own projectile row.
// GUESS: weapon02/03's own rows were copies of weapon01's. The three Blueprints
// agree field for field, but the rows themselves are lost.
#define RVA_WEAPON_GROUP_LOAD_ROW 0x518EE0
#define OFF_WEAPON_GROUP_ROW_NAME 0x2B8
#define OFF_WEAPON_GROUP_ROW 0x2B0
typedef void *(__fastcall *tWeaponGroupLoadRow)(void *group);
static tWeaponGroupLoadRow g_origWeaponGroupLoadRow = nullptr;

static void *__fastcall HookWeaponGroupLoadRow(void *group) {
  void *row = g_origWeaponGroupLoadRow(group);
  if (row)
    return row;
  uint8_t *g = (uint8_t *)group;
  // The group's own row name, kept so it is put back on EVERY path: left
  // pointing at the static buffer below, the engine would one day free it.
  wchar_t *name = nullptr;
  int32_t num = 0, max = 0;
  bool swapped = false;
  __try {
    name = *(wchar_t **)(g + OFF_WEAPON_GROUP_ROW_NAME);
    if (!name || (lstrcmpW(name, L"WP_ONS_AITargetH_weapon02_BP") != 0 &&
                  lstrcmpW(name, L"WP_ONS_AITargetH_weapon03_BP") != 0))
      return row;
    static wchar_t s_weapon01[] = L"WP_ONS_AITargetH_weapon01_BP";
    num = *(int32_t *)(g + OFF_WEAPON_GROUP_ROW_NAME + 8);
    max = *(int32_t *)(g + OFF_WEAPON_GROUP_ROW_NAME + 12);
    swapped = true;
    *(wchar_t **)(g + OFF_WEAPON_GROUP_ROW_NAME) = s_weapon01;
    *(int32_t *)(g + OFF_WEAPON_GROUP_ROW_NAME + 8) = (int32_t)(sizeof(s_weapon01) / sizeof(wchar_t));
    *(int32_t *)(g + OFF_WEAPON_GROUP_ROW_NAME + 12) = (int32_t)(sizeof(s_weapon01) / sizeof(wchar_t));
    *(void **)(g + OFF_WEAPON_GROUP_ROW) = nullptr;
    row = g_origWeaponGroupLoadRow(group);
    static volatile LONG s_logged = 0;
    if (InterlockedIncrement(&s_logged) <= 4)
      Logf("command ship gun: no weapon row for %S; using WP_ONS_AITargetH_weapon01_BP's -> %p", name, row);
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    Logf("command ship gun: EXCEPTION 0x%08X", GetExceptionCode());
  }
  if (swapped) {
    *(wchar_t **)(g + OFF_WEAPON_GROUP_ROW_NAME) = name;
    *(int32_t *)(g + OFF_WEAPON_GROUP_ROW_NAME + 8) = num;
    *(int32_t *)(g + OFF_WEAPON_GROUP_ROW_NAME + 12) = max;
  }
  return row;
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
// Turbo TDM modifiers (on, TurboTDM hosts only; dn_host_no_game_modifiers.txt
// turns it off). From PR #80, addresses re-checked 2026-10-16.
//
// Turbo TDM's whole effect is three feats on every ship. DN_GameModifiers_DT
// row YGMT_TURBO_TDM: m_feats "ModifierSpeed;ModifierDamage;ModifierFireRate",
// m_affectedTeam YHMAT_ALL (the only row with feats; YGMT_TDM's is empty).
// DN_Feats_Custom_Modifiers_OTS_DT: ModifierSpeed +600% forward/reverse, +300%
// vertical/steer; ModifierDamage +50%; ModifierFireRate -50% cooldowns/reloads.
// Nothing on the host applied them (the code that handed the row's feats to
// ships was in the original server build), so Turbo TDM played as plain TDM.
//
// 0x34BB30 (.pdata 0x34BB30-0x34BCFC): the feats component's setup -- reads
// its owner (+0xA8), binds "&UYFeatsComponent::OnPawnPossessed". After it, the
// row's feats are added with the game's own AddFeat body 0x337850(comp, FName)
// (.pdata 0x337850-0x337953; exec thunk 0x73EDF0 is registered as "AddFeat"
// and reads one FName). On the authority it adds locally; the AddFeatServer
// RPC (0x5DFC40) is only taken when the caller lacks authority.
// VERIFIED live 2026-10-16: feats added on every ship (player + bots), and the
// client moved faster and smoothly (operator: "faster and smooth no stutter").
#define RVA_FEATS_COMPONENT_BEGIN 0x34BB30
#define RVA_FEATS_ADD_FEAT 0x337850
typedef void(__fastcall *tFeatsBegin)(void *featsComp);
typedef bool(__fastcall *tAddFeat)(void *featsComp, uint64_t featName);
static tFeatsBegin g_origFeatsBegin = nullptr;

static const wchar_t *const kTurboTdmFeats[] = {L"ModifierSpeed", L"ModifierDamage",
                                                L"ModifierFireRate"};

static bool HostIsTurboTDM() {
  const wchar_t *cmd = GetCommandLineW();
  return cmd && wcsstr(cmd, L"-GameMode=TurboTDM ") != nullptr;
}

// Havoc records every ship's feats component here, for the boosts its
// players buy between waves (HavocApplyBoosts).
static void RecordFeatsComp(void *pawn, void *comp);
static bool g_featsTurbo = false, g_featsHavoc = false;

static void __fastcall HookFeatsBegin(void *featsComp) {
  g_origFeatsBegin(featsComp);
  uint8_t *fc = (uint8_t *)featsComp;
  void *owner = IsReadable(fc + OFF_COMPONENT_OWNER, 8) ? *(void **)(fc + OFF_COMPONENT_OWNER) : nullptr;
  if (!owner || !ClassChainContains((UObjectMin *)owner, "Pawn"))
    return;
  if (g_featsHavoc)
    RecordFeatsComp(owner, featsComp);
  if (!g_featsTurbo)
    return;
  static volatile LONG s_logged = 0;
  bool logIt = InterlockedIncrement(&s_logged) <= 16;
  for (const wchar_t *feat : kTurboTdmFeats) {
    FNameMin name{};
    ((tFNameCtor)(g_base + RVA_FNAME_CTOR_WIDE))(&name, feat, 1 /* FNAME_Add */);
    bool ok = false;
    __try {
      ok = ((tAddFeat)(g_base + RVA_FEATS_ADD_FEAT))(featsComp, *(uint64_t *)&name);
    } __except (EXCEPTION_EXECUTE_HANDLER) {
      Logf("turbo modifiers: AddFeat(%ls) faulted on %p", feat, featsComp);
      return;
    }
    if (logIt || !ok)
      Logf("turbo modifiers: AddFeat(%ls) on %s -> %s", feat, ClassNameOf(owner),
           ok ? "added" : "FAILED");
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

// The guard above is not enough on its own: on 2026-09-28 three hosts
// (14:07, 14:19, 20:25) still overflowed at match end, each right after the
// guard dropped ONE nested call -- clean match ends show it firing twice. So
// the loop also runs through a path the _Implementation guard does not see.
// This cuts it at its source instead: the ClientSetPlayerRestrictions RPC stub
// (0x5E5B80, 18 args like the body -- this, 3 register bools, 14 stack slots)
// does nothing for a controller WITHOUT a network connection (PC+0x5A8 null),
// i.e. the host's own local player, whose "client RPC" would execute
// in-process and re-enter SetPlayerRestrictions. Remote players' RPCs go out
// unchanged. The local player is the host's spectator; its restrictions are
// never shown to anyone.
// ---------------------------------------------------------------------------
// ClientApplyRespawnFilter re-entrancy guard (not switchable)
//
// THE end-of-match stack overflow (~40% of matches, ~3 ms after EndMatch).
// Found 2026-09-29 by the stack probe below, which caught the cycle live:
//
//   0x28D6FB -> 0x5E5107 -> 0x41723D -> 0x744B7C -> 0xD1878B -> 0xD5B430 ->
//   0xD5B2A4 -> 0x28D6FB -> ...        (862 repetitions in 512 KB of stack)
//
// 0x5E50C0 is the RPC stub of ClientApplyRespawnFilter (its FName global
// 0x3E10168 is initialised at 0xCD48D). CORRECTION 2026-10-15: that global is
// initialised from L"ClientAddAchievement" (0xCD486); ClientApplyRespawnFilter
// is the next one (0xCD4A6). The stub is ClientAddAchievement on the scoring
// component; the loop and the guard are unchanged (see the live ribbons). It calls ProcessEvent (0xD5B180), which
// runs the exec thunk (0x744AF0) and the body 0x4171E0: that adds the filter's
// value to this+0x1F0 and then, when the object's net check (0x141839880)
// returns 1 -- which it does on this host, which reports itself DEDICATED --
// forwards the same filter to the client by calling the stub again. For a
// REMOTE player that is a network RPC and ends there; for the host's own
// LOCAL player it executes in-process and calls the stub again, forever. The
// same shape as the ClientSetPlayerRestrictions loop above.
//
// The guard drops only a NESTED call on the same thread: the first call
// always goes through, so remote players get their RPC unchanged, and the
// local player's in-process copy runs once.
#define RVA_CLIENT_APPLY_RESPAWN_FILTER_RPC 0x5E50C0
typedef void(__fastcall *tClientApplyRespawnFilter)(void *self, void *filter);
static tClientApplyRespawnFilter g_origClientApplyRespawnFilter = nullptr;
static __declspec(thread) int t_respawnFilterDepth = 0;

static void __fastcall HookClientApplyRespawnFilter(void *self, void *filter) {
  if (t_respawnFilterDepth > 0) {
    static volatile LONG s_logged = 0;
    if (InterlockedIncrement(&s_logged) <= 3)
      Logf("respawn filter: dropped a nested ClientApplyRespawnFilter on %p (the "
           "local player's in-process RPC loop that overflowed the stack at match end)",
           self);
    return;
  }
  ++t_respawnFilterDepth;
  g_origClientApplyRespawnFilter(self, filter);
  --t_respawnFilterDepth;
}

// ---------------------------------------------------------------------------
// End-of-match stack probe (DIAGNOSTIC; dn_host_no_stack_probe.txt /
// DN_HOST_NO_STACK_PROBE=1 turns it off)
//
// The host overflows its stack in about 40% of matches, ~3 ms after EndMatch
// starts ("Match over event" -> music reset -> EXCEPTION_STACK_OVERFLOW; 5 of
// 12 matches on 2026-09-29). Neither Wine nor the game logs a backtrace, and
// the restrictions guard above did not stop it, so the recursing function is
// unknown. This finds it: the local-controller restrictions RPC fires on the
// game thread right before every overflow, so it arms a watcher for 5 s. The
// watcher repeatedly suspends the game thread and reads its RSP; once the stack
// is PROBE_DEPTH deeper than at arm time, it scans the stack above RSP for
// values inside the exe's code section, counts them, and writes the most
// frequent ones -- the return addresses of the recursion -- as RVAs.
//
// While the game thread is suspended nothing here touches the C runtime or the
// heap (the thread may hold their locks): wsprintfA into static buffers and a
// raw WriteFile to dn_host_stackprobe.log beside the exe.
#define PROBE_DEPTH (768 * 1024)
#define PROBE_SCAN (512 * 1024)
#define PROBE_SLOTS 4096
static HANDLE g_probeTarget = nullptr;
static volatile LONG g_probeArmed = 0;
static volatile DWORD g_probeUntil = 0;
static uintptr_t g_probeBaseline = 0;
static uintptr_t g_textLo = 0, g_textHi = 0;
static uint64_t g_probeKeys[PROBE_SLOTS];
static uint32_t g_probeCounts[PROBE_SLOTS];
static char g_probeLine[512];
static HANDLE g_probeFile = INVALID_HANDLE_VALUE;
static bool SwitchOn(const char *envName, const char *markerFile);

static void ProbeWrite(const char *text) {
  if (g_probeFile == INVALID_HANDLE_VALUE)
    return;
  DWORD n = 0;
  WriteFile(g_probeFile, text, (DWORD)lstrlenA(text), &n, nullptr);
}

static void ProbeScanAndReport(uintptr_t rsp, uintptr_t rip) {
  memset(g_probeKeys, 0, sizeof(g_probeKeys));
  memset(g_probeCounts, 0, sizeof(g_probeCounts));
  uint32_t candidates = 0;
  // Every page is checked before it is read, the FIRST one included: the probe
  // can fire just as the thread reaches the bottom of its stack, where RSP sits
  // on the guard page -- reading it faulted this thread and took the host down
  // (21:45:42 on 2026-09-29, "Unhandled page fault on read access to
  // 00007FFFFE100000", thread = the probe). Guard / no-access pages end the scan.
  uintptr_t readableTo = 0;
  for (uintptr_t p = rsp; p < rsp + PROBE_SCAN; p += 8) {
    if (p >= readableTo) {
      MEMORY_BASIC_INFORMATION mbi;
      if (!VirtualQuery((void *)p, &mbi, sizeof(mbi)) || mbi.State != MEM_COMMIT ||
          (mbi.Protect & (PAGE_GUARD | PAGE_NOACCESS)) || !(mbi.Protect & (PAGE_READWRITE | PAGE_READONLY | PAGE_EXECUTE_READ | PAGE_EXECUTE_READWRITE)))
        break;
      readableTo = (uintptr_t)mbi.BaseAddress + mbi.RegionSize;
    }
    uint64_t v;
    __try {
      v = *(uint64_t *)p;
    } __except (EXCEPTION_EXECUTE_HANDLER) {
      break;
    }
    if (v < g_textLo || v >= g_textHi)
      continue;
    candidates++;
    uint32_t h = (uint32_t)((v * 0x9E3779B97F4A7C15ull) >> 52) & (PROBE_SLOTS - 1);
    for (int i = 0; i < PROBE_SLOTS; ++i, h = (h + 1) & (PROBE_SLOTS - 1)) {
      if (g_probeKeys[h] == v || g_probeKeys[h] == 0) {
        g_probeKeys[h] = v;
        g_probeCounts[h]++;
        break;
      }
    }
  }
  wsprintfA(g_probeLine,
            "stack probe: game thread is %u KB deeper than at match end; rip RVA 0x%X; "
            "%u code addresses in the top %u KB of stack. Most frequent (RVA x count):\r\n",
            (unsigned)((g_probeBaseline - rsp) / 1024),
            (unsigned)(rip >= g_base ? rip - g_base : 0), candidates, PROBE_SCAN / 1024);
  ProbeWrite(g_probeLine);
  for (int rank = 0; rank < 24; ++rank) {
    int best = -1;
    for (int i = 0; i < PROBE_SLOTS; ++i)
      if (g_probeCounts[i] && (best < 0 || g_probeCounts[i] > g_probeCounts[best]))
        best = i;
    if (best < 0)
      break;
    wsprintfA(g_probeLine, "  0x%X x %u\r\n", (unsigned)(g_probeKeys[best] - g_base),
              g_probeCounts[best]);
    ProbeWrite(g_probeLine);
    g_probeCounts[best] = 0;
  }
}

static DWORD WINAPI StackProbeThread(LPVOID) {
  for (;;) {
    if (!g_probeArmed) {
      Sleep(20);
      continue;
    }
    if ((LONG)(GetTickCount() - g_probeUntil) >= 0) {
      g_probeArmed = 0;
      continue;
    }
    if (SuspendThread(g_probeTarget) == (DWORD)-1) {
      g_probeArmed = 0;
      continue;
    }
    CONTEXT ctx;
    memset(&ctx, 0, sizeof(ctx));
    ctx.ContextFlags = CONTEXT_CONTROL;
    if (GetThreadContext(g_probeTarget, &ctx) && g_probeBaseline > ctx.Rsp &&
        g_probeBaseline - ctx.Rsp > PROBE_DEPTH) {
      ProbeScanAndReport((uintptr_t)ctx.Rsp, (uintptr_t)ctx.Rip);
      g_probeArmed = 0;
    }
    ResumeThread(g_probeTarget);
    SwitchToThread();
  }
}

// The exe's code range and the raw output file, shared by the stack probe and
// the crash handler below. Safe to call more than once.
static void InitProbeOutput() {
  if (g_textLo)
    return;
  IMAGE_DOS_HEADER *dos = (IMAGE_DOS_HEADER *)g_base;
  IMAGE_NT_HEADERS64 *nt = (IMAGE_NT_HEADERS64 *)(g_base + dos->e_lfanew);
  g_textLo = g_base + nt->OptionalHeader.BaseOfCode;
  g_textHi = g_textLo + nt->OptionalHeader.SizeOfCode;
  char path[MAX_PATH];
  DWORD len = GetModuleFileNameA(nullptr, path, MAX_PATH);
  while (len > 0 && path[len - 1] != '\\' && path[len - 1] != '/')
    --len;
  lstrcpyA(path + len, "dn_host_stackprobe.log");
  g_probeFile = CreateFileA(path, FILE_APPEND_DATA, FILE_SHARE_READ | FILE_SHARE_WRITE,
                            nullptr, OPEN_ALWAYS, FILE_ATTRIBUTE_NORMAL, nullptr);
}

// ---------------------------------------------------------------------------
// Host crash handler (DIAGNOSTIC; same switch as the stack probe)
//
// The second host crash: "Unhandled Exception: EXCEPTION_ACCESS_VIOLATION
// reading address 0xfffffff8" MID-match (3-5 min in), then the engine's crash
// handler exits with status 3 -- 7 matches on 2026-09-28/29, 5 of them
// identical. The engine's report lists frames without addresses and Wine logs
// none, so the faulting code is unknown. A vectored handler sees the exception
// first: for an access violation or stack overflow whose RIP is inside the
// exe's code, it writes the fault RVA, the address read, and the exe code
// addresses found on the stack above RSP in order (a rough call chain), to
// dn_host_stackprobe.log. Faults in wer.dll itself (the IsReadable probes)
// are not in the exe and are skipped. It only observes: always
// EXCEPTION_CONTINUE_SEARCH. At most 4 reports per process.
static volatile LONG g_crashReports = 0;
static char g_crashLine[256];

static LONG CALLBACK HostCrashHandler(EXCEPTION_POINTERS *ep) {
  DWORD code = ep->ExceptionRecord->ExceptionCode;
  if (code != EXCEPTION_ACCESS_VIOLATION && code != EXCEPTION_STACK_OVERFLOW)
    return EXCEPTION_CONTINUE_SEARCH;
  uintptr_t rip = (uintptr_t)ep->ContextRecord->Rip;
  if (rip < g_textLo || rip >= g_textHi)
    return EXCEPTION_CONTINUE_SEARCH;
  if (InterlockedIncrement(&g_crashReports) > 4)
    return EXCEPTION_CONTINUE_SEARCH;
  uintptr_t addr = ep->ExceptionRecord->NumberParameters >= 2
                       ? (uintptr_t)ep->ExceptionRecord->ExceptionInformation[1]
                       : 0;
  uintptr_t rsp = (uintptr_t)ep->ContextRecord->Rsp;
  wsprintfA(g_crashLine, "crash: %s at RVA 0x%X, address 0x%I64X, thread %u. Stack chain (RVA):\r\n",
            code == EXCEPTION_STACK_OVERFLOW ? "STACK OVERFLOW" : "ACCESS VIOLATION",
            (unsigned)(rip - g_base), (unsigned long long)addr, (unsigned)GetCurrentThreadId());
  ProbeWrite(g_crashLine);
  // Registers often hold the object that was null; worth a line.
  CONTEXT *c = ep->ContextRecord;
  wsprintfA(g_crashLine, "  rax=%I64X rbx=%I64X rcx=%I64X rdx=%I64X rsi=%I64X rdi=%I64X\r\n",
            c->Rax, c->Rbx, c->Rcx, c->Rdx, c->Rsi, c->Rdi);
  ProbeWrite(g_crashLine);
  // Separate line: both in one would be up to 298 chars in this 256-byte
  // buffer (wsprintfA does not bound it). r8-r15 from PR #80.
  wsprintfA(g_crashLine, "  r8=%I64X r9=%I64X r10=%I64X r11=%I64X r12=%I64X r13=%I64X r14=%I64X r15=%I64X\r\n",
            c->R8, c->R9, c->R10, c->R11, c->R12, c->R13, c->R14, c->R15);
  ProbeWrite(g_crashLine);
  int shown = 0;
  uintptr_t limit = rsp + (code == EXCEPTION_STACK_OVERFLOW ? 4096 : 65536);
  uintptr_t readableTo = 0;
  for (uintptr_t p = rsp; p < limit && shown < 40; p += 8) {
    if (p >= readableTo) {
      MEMORY_BASIC_INFORMATION mbi;
      if (!VirtualQuery((void *)p, &mbi, sizeof(mbi)) || mbi.State != MEM_COMMIT ||
          (mbi.Protect & (PAGE_GUARD | PAGE_NOACCESS)))
        break;
      readableTo = (uintptr_t)mbi.BaseAddress + mbi.RegionSize;
    }
    uint64_t v = *(uint64_t *)p;
    if (v >= g_textLo && v < g_textHi) {
      wsprintfA(g_crashLine, "  0x%X\r\n", (unsigned)(v - g_base));
      ProbeWrite(g_crashLine);
      shown++;
    }
  }
  return EXCEPTION_CONTINUE_SEARCH;
}

static void InstallHostCrashHandler() {
  if (SwitchOn("DN_HOST_NO_STACK_PROBE", "dn_host_no_stack_probe.txt"))
    return;
  InitProbeOutput();
  if (AddVectoredExceptionHandler(1, HostCrashHandler))
    Logf("crash handler: installed; access violations / stack overflows in the exe "
         "are written to dn_host_stackprobe.log");
}

// Called on the game thread at match end (the restrictions RPC hook).
static void ArmStackProbe() {
  static int s_state = -1; // -1 unknown, 0 off, 1 on
  if (s_state < 0) {
    s_state = SwitchOn("DN_HOST_NO_STACK_PROBE", "dn_host_no_stack_probe.txt") ? 0 : 1;
    if (s_state) {
      InitProbeOutput();
      if (!DuplicateHandle(GetCurrentProcess(), GetCurrentThread(), GetCurrentProcess(),
                           &g_probeTarget, THREAD_SUSPEND_RESUME | THREAD_GET_CONTEXT, FALSE, 0) ||
          !CreateThread(nullptr, 0, StackProbeThread, nullptr, 0, nullptr)) {
        s_state = 0;
        Logf("stack probe: could not start (thread handle or watcher thread)");
      } else {
        Logf("stack probe: armed on the game thread; a deep stack in the next 5 s is "
             "written to dn_host_stackprobe.log");
      }
    }
  }
  if (!s_state)
    return;
  volatile char here = 0;
  g_probeBaseline = (uintptr_t)&here;
  g_probeUntil = GetTickCount() + 5000;
  g_probeArmed = 1;
}

#define RVA_CLIENT_SET_PLAYER_RESTRICTIONS_RPC 0x5E5B80
static tClientSetPlayerRestrictions g_origClientSetPlayerRestrictionsRPC = nullptr;

static void __fastcall HookClientSetPlayerRestrictionsRPC(
    void *pc, uint64_t a2, uint64_t a3, uint64_t a4, uint64_t a5, uint64_t a6,
    uint64_t a7, uint64_t a8, uint64_t a9, uint64_t a10, uint64_t a11,
    uint64_t a12, uint64_t a13, uint64_t a14, uint64_t a15, uint64_t a16,
    uint64_t a17, uint64_t a18) {
  bool local = false;
  __try {
    local = pc && *(void **)((uint8_t *)pc + OFF_PC_NETCONNECTION) == nullptr;
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    local = false;
  }
  if (local) {
    ArmStackProbe(); // the overflow follows within milliseconds when it happens
    static volatile LONG s_logged = 0;
    if (InterlockedIncrement(&s_logged) <= 3)
      Logf("restrictions: skipped the ClientSetPlayerRestrictions RPC to the host's "
           "local controller %p (it would execute in-process and loop)", pc);
    return;
  }
  g_origClientSetPlayerRestrictionsRPC(pc, a2, a3, a4, a5, a6, a7, a8, a9, a10,
                                       a11, a12, a13, a14, a15, a16, a17, a18);
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

// ---------------------------------------------------------------------------
// Match result -> mmogbrain (on unless dn_host_no_match_result.txt /
// DN_HOST_NO_MATCH_RESULT=1)
//
// Added 2026-09-26. The original backend paid XP and credits from a result the
// server build reported; nothing in this exe does, so no match ever paid
// anything. At the same moment the eom stats go out -- once per connected
// player, as that player's end-of-match flow starts -- this reads the player's
// numbers from the host and sends them to mmogbrain's loopback
// GET /battle/result, which is idempotent per (match, player).
//
// Field offsets and types from the AYPlayerReplicationInfo property
// registration (0x140647C00: m_kills/m_deaths/m_assists are IntProperty,
// damage is the FloatProperty constructor shared with m_rawDamageReceived*,
// m_team a ByteProperty), AYGameState m_finalMatchResult +0x56B (EYMatchResult:
// 1 team 1, 2 team 2, 3 draw). The game state is reached PRI -> Outer (level)
// -> Outer (world) -> UWorld::GameState +0x58, and checked by class name.
// NOT verified live: whether m_finalMatchResult is already set when the
// transition starts. A 0 is reported as-is (mmogbrain pays it as a loss and
// logs outcome=unknown), which is the line to look for.
#define OFF_PRI_KILLS 0x848
#define OFF_PRI_DEATHS 0x850
#define OFF_PRI_ASSISTS 0x858
#define OFF_PRI_DAMAGE_WEAPONS 0x908
#define OFF_PRI_DAMAGE_ABILITIES 0x90C
#define OFF_PRI_TEAM 0x940
// Statistics screen figures (2026-10-15), from the SDK dump like the rest:
// m_score IntProperty, m_totalHealingDone FloatProperty. NOT verified live.
#define OFF_PRI_SCORE 0x7FC
#define OFF_PRI_HEALING 0x924
// AYPlayerController::m_team (SDK: Net, 0x0C30). FIXED 2026-09-27: the first
// reported match read PRI m_team = 0 (YT_NONE; EYTeam is NONE 0, TEAM1 1,
// TEAM2 2 per its registration 0x140697ED0) on a proving-ground host, so a won
// match was paid as a loss. The controller's copy is the fallback. Offset from
// the SDK dump, which matched every PRI/GameState offset verified live
// (0x654, 0x848, 0x56B); this one is NOT yet verified live.
#define OFF_PC_TEAM 0xC30
#define OFF_WORLD_GAMESTATE 0x58
#define OFF_GS_FINAL_MATCH_RESULT 0x56B
#define OFF_USTRUCT_SUPER 0x30

static bool ClassChainContains(UObjectMin *obj, const char *needle) {
  UObjectMin *cls = IsReadable(obj, sizeof(UObjectMin)) ? obj->Class : nullptr;
  for (int depth = 0; cls && depth < 8; ++depth) {
    if (!IsReadable(cls, OFF_USTRUCT_SUPER + 8))
      return false;
    const char *n = NameText(cls->Name);
    if (n && strstr(n, needle))
      return true;
    cls = *(UObjectMin **)((uint8_t *)cls + OFF_USTRUCT_SUPER);
  }
  return false;
}

static uint8_t *GameStateForPRI(uint8_t *pri) {
  UObjectMin *level = ((UObjectMin *)pri)->Outer;
  UObjectMin *world = IsReadable(level, sizeof(UObjectMin)) ? level->Outer : nullptr;
  if (!IsReadable(world, OFF_WORLD_GAMESTATE + 8))
    return nullptr;
  uint8_t *gs = *(uint8_t **)((uint8_t *)world + OFF_WORLD_GAMESTATE);
  if (!IsReadable(gs, OFF_GS_FINAL_MATCH_RESULT + 1) ||
      !ClassChainContains((UObjectMin *)gs, "GameState"))
    return nullptr;
  return gs;
}

// -MatchID=<id> from the host's command line (dn-dedicated passes it).
static bool MatchIDFromCommandLine(char *out, size_t outLen) {
  out[0] = 0;
  const wchar_t *cmd = GetCommandLineW();
  const wchar_t *p = cmd ? wcsstr(cmd, L"MatchID=") : nullptr;
  if (!p)
    return false;
  p += 8;
  size_t n = 0;
  while (p[n] && p[n] != L' ' && p[n] != L'"' && p[n] != L'?' && p[n] != L'&' &&
         n + 1 < outLen) {
    wchar_t c = p[n];
    out[n] = (c < 128 && (isalnum((int)c) || c == L'-' || c == L'_' || c == L'.'))
                 ? (char)c : '_';
    ++n;
  }
  out[n] = 0;
  return n > 0;
}

static int s_round = 0;

static void WriteEomRewards(uint8_t *pri, const char *body, const char *match);
static bool RewardsScreenEnabled();

// ---------------------------------------------------------------------------
// Original match XP (on; dn_host_no_event_xp.txt turns it off)
//
// mmogbrain now serves the ORIGINAL scoring table (the operator's datamine of
// the 1.11.1 / 1.12.0 tables, data/scoring/ScoringTable.json), XP and credits
// per event included. The host awards them itself, verified 2026-10-07:
// AYPlayerReplicationInfo::AssignScoringReward 0x5AD140 (one caller,
// 0x5B3170: rcx = the PRI, rdx = the event's reward record) runs on the
// server (role +0x148 == 3) and adds
//   score   rec[3] + rec[4]   to the PRI's score
//   XP      rec[5] + rec[6]   to the XP manager (AddMatchShipXP 0x3EB680 /
//                             AddMatchShipXPToAllShips 0x3EBA00)
//   credits rec[7] + rec[8]   (0x5ABDC0)
// with rec[0] the EYScoringEventID. Summing the XP from the XP manager would
// count "to all ships" events once per ship, so the record is summed here,
// per player, and reported with the match result (event_xp / event_credits);
// mmogbrain pays its XP from it instead of the placeholder formula.
#define RVA_ASSIGN_SCORING_REWARD 0x5AD140
typedef void(__fastcall *tAssignScoringReward)(void *pri, int32_t *reward);
static tAssignScoringReward g_origAssignScoringReward = nullptr;

// EYScoringEventID ordinals (Invalid = 75); see mmogbrain scoringEventIDs.
#define SCORING_EVENT_COUNT 76
struct EventTotals {
  void *pri;
  int64_t xp, credits;
  int events;
  // How often each scoring event fired for this player, rewarded or not, for
  // mmogbrain's ribbons (one per EventsForRibbon occurrences). The host
  // counts events too (0x431660) but never awards a ribbon itself: its reward
  // path builds only event achievements (0x5A9D30 type 1).
  uint16_t counts[SCORING_EVENT_COUNT];
};
static EventTotals g_eventTotals[128] = {};
static SRWLOCK g_eventTotalsLock = SRWLOCK_INIT;

// ---------------------------------------------------------------------------
// Ribbons announced in the match (on; dn_host_no_live_ribbons.txt /
// DN_HOST_NO_LIVE_RIBBONS=1 turns it off). Added 2026-10-15.
//
// A ribbon never showed during a match (operator: "ribbons dont get rewarded
// in matches"), although mmogbrain awards them from ev= after it. The host's
// reward path (0x426DC0) builds only a type-1 (event) achievement (0x5A9D30
// is called with 1), adds it to the scoring component's event list
// (0x4135A0) and, when the component's net check 0x141839880 says 1, sends it
// to the player with ClientAddAchievement -- the RPC stub 0x5E50C0 (its FName
// global 0x3E10168 is initialised from L"ClientAddAchievement" at 0xCD486;
// the respawn-filter guard further down hooks the same stub). Type 2 is the
// ribbon: 0x5A9D30 fills it from the RIBBON half of the record, and 0x413740
// is the component's ribbon list. Nothing in this exe ever builds one; the
// original server build did.
//
// So, the original rule: when a player's count of an event reaches a
// multiple of the row's EventsForRibbon (the same floor(count/n) mmogbrain
// pays), the mod builds the type-2 achievement and sends it exactly as the
// host sends type 1. The ribbon values per mode come from mmogbrain
// (GET /battle/ribbons?mode=, loaded when the match table is built).
// Achievement layout (0x1C, from 0x5A9D30 and the RPC's 0x1C-byte copy):
//   +0x00 u8 type (1 event, 2 ribbon)   +0x04 i32 EYScoringEventID
//   +0x08 u8 1, +0x09..0x0B 0           +0x0C score  +0x10 XP  +0x14 credits
//   +0x18 i32 the ship's precast loadout id (record [2] = PRI +0x654)
// The scoring component is AYPlayerReplicationInfo::m_scoringEventComponent
// (+0x928, SDK). NOT verified live.
#define RVA_CLIENT_ADD_ACHIEVEMENT_RPC 0x5E50C0
#define RVA_COMPONENT_ADD_RIBBON 0x413740
#define RVA_COMPONENT_NET_CHECK 0x1839880
#define OFF_PRI_SCORING_COMPONENT 0x928
#define OFF_PRI_PRECAST_LOADOUT 0x654
#define SCORING_EVENT_MATCH_END 40
struct RibbonRule {
  int n, score, xp, credits;
};
static RibbonRule g_ribbonRules[SCORING_EVENT_COUNT] = {};
static bool SwitchOn(const char *envName, const char *markerFile);

static bool LiveRibbonsEnabled() {
  static int s_on = -1;
  if (s_on < 0)
    s_on = SwitchOn("DN_HOST_NO_LIVE_RIBBONS", "dn_host_no_live_ribbons.txt") ? 0 : 1;
  return s_on == 1;
}

// The ribbon rows for the match's scoring mode, from mmogbrain.
static void LoadRibbonRules(const wchar_t *mode) {
  if (!LiveRibbonsEnabled())
    return;
  char m[24] = {0}, path[96], body[4096];
  for (int i = 0; i < 20 && mode && mode[i]; ++i)
    m[i] = (mode[i] < 128 && (isalnum((int)mode[i]) || mode[i] == L'_')) ? (char)mode[i] : '_';
  _snprintf_s(path, sizeof(path), _TRUNCATE, "/battle/ribbons?mode=%s", m);
  if (!HttpGetLoopback(path, body, sizeof(body))) {
    Logf("ribbons: GET %s failed; no ribbons announced in this match", path);
    return;
  }
  RibbonRule rules[SCORING_EVENT_COUNT] = {};
  int loaded = 0;
  for (char *line = body; line && *line;) {
    char *next = strchr(line, '\n');
    if (next)
      *next++ = 0;
    int ord, n, sc, xp, cr;
    if (sscanf_s(line, "%d %d %d %d %d", &ord, &n, &sc, &xp, &cr) == 5 && ord >= 0 &&
        ord < SCORING_EVENT_COUNT && n > 0) {
      rules[ord] = RibbonRule{n, sc, xp, cr};
      ++loaded;
    }
    line = next;
  }
  memcpy(g_ribbonRules, rules, sizeof(rules));
  Logf("ribbons: %d ribbon rules loaded for mode \"%s\"", loaded, m);
}

// Sends one ribbon (type-2 achievement) for event to the player owning pri.
static void SendRibbonAchievement(void *pri, uint32_t event) {
  if (!LiveRibbonsEnabled() || event >= SCORING_EVENT_COUNT || g_ribbonRules[event].n <= 0)
    return;
  __try {
    uint8_t *p = (uint8_t *)pri;
    if (!IsReadable(p + OFF_PRI_SCORING_COMPONENT, 8) || !IsReadable(p + OFF_PRI_PRECAST_LOADOUT, 4))
      return;
    void *comp = *(void **)(p + OFF_PRI_SCORING_COMPONENT);
    if (!comp || !IsReadable(comp, 0x200))
      return;
    alignas(8) uint8_t ach[0x20] = {};
    ach[0] = 2;
    *(int32_t *)(ach + 0x04) = (int32_t)event;
    ach[0x08] = 1;
    *(int32_t *)(ach + 0x0C) = g_ribbonRules[event].score;
    *(int32_t *)(ach + 0x10) = g_ribbonRules[event].xp;
    *(int32_t *)(ach + 0x14) = g_ribbonRules[event].credits;
    *(int32_t *)(ach + 0x18) = *(int32_t *)(p + OFF_PRI_PRECAST_LOADOUT);
    ((void(__fastcall *)(void *, void *))(g_base + RVA_COMPONENT_ADD_RIBBON))(comp, ach);
    int net = ((int(__fastcall *)(void *))(g_base + RVA_COMPONENT_NET_CHECK))(comp);
    if (net == 1)
      ((void(__fastcall *)(void *, void *))(g_base + RVA_CLIENT_ADD_ACHIEVEMENT_RPC))(comp, ach);
    Logf("ribbons: event %u -> ribbon for player state %p (net %d%s)", event, pri, net,
         net == 1 ? ", sent" : ", not sent: not a remote owner");
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    Logf("ribbons: EXCEPTION announcing event %u for %p", event, pri);
  }
}

// After a count went up: a ribbon when it reaches a multiple of the row's n.
static void MaybeAnnounceRibbon(void *pri, uint32_t event, unsigned count) {
  if (event < SCORING_EVENT_COUNT && count > 0 && g_ribbonRules[event].n > 0 &&
      count % (unsigned)g_ribbonRules[event].n == 0)
    SendRibbonAchievement(pri, event);
}

// The in-match ribbon popup (on; dn_host_no_ribbon_popup.txt /
// DN_HOST_NO_RIBBON_POPUP=1 turns it off). Added 2026-10-16.
//
// ClientAddAchievement (above) only files the ribbon into the client's
// scoring component list (the client's +0x350 is 0x4171E0 itself: type 2 ->
// 0x413740, no UI). The popup is a different RPC:
// AYPlayerReplicationInfo::ClientEventResult(FYEventResponse) -- sender stub
// 0x5E5280 (FName 0x3E101C8), client body 0x5AFEA0 (PRI vtable +0x698). The
// client takes the row's RIBBON name when m_ribbonAchieved (+0x04) is set,
// broadcasts it (0x5C59C0, isRibbon = that byte, points = eventScore +
// ribbonScore) to AYHUD::OnPlayerRepScoringEvent -> 0x494950 ->
// UYHUDWidget_ScoringEvents::BP_EventAddScoringEvent(text, level,
// isRibbonEvent, ribbonTexture), and plays "#Scoring_<name>".
//
// The host sends it already: PRI::ScoreEvent 0x5B30D0 fills the
// FYEventResponse (0x426DC0), calls AssignScoringReward (this hook) on it,
// then sends ClientEventResult if m_requiresHUDUpdate (+0x28) and the row's
// HUD flag (+0xB0) are set. It never sets the ribbon half. So, BEFORE the
// original runs, when this event completes a ribbon:
//   +0x04 m_ribbonAchieved = 1, +0x10 m_ribbonScore = the row's RibbonScore
//   (AssignScoringReward adds eventScore + ribbonScore to the score, as the
//   original server's did), +0x28 m_requiresHUDUpdate = 1 (0x426DC0 sets it
//   only for rows with an event score; GUESS: a ribbon also shows).
// Ribbon XP (+0x18) and credits (+0x20) stay 0: mmogbrain pays them from ev=.
// FYEventResponse (SDK): +0 m_id, +4 m_ribbonAchieved, +8 m_shipId,
// +0xC m_eventScore, +0x10 m_ribbonScore, +0x14 m_eventReward,
// +0x18 m_ribbonReward, +0x1C m_eventCredits, +0x20 m_ribbonCredits,
// +0x24 m_visibility, +0x28 m_requiresHUDUpdate.
static bool RibbonPopupEnabled() {
  static int s_on = -1;
  if (s_on < 0)
    s_on = SwitchOn("DN_HOST_NO_RIBBON_POPUP", "dn_host_no_ribbon_popup.txt") ? 0 : 1;
  return s_on == 1;
}

static void __fastcall HookAssignScoringReward(void *pri, int32_t *reward) {
  bool tracked = false, ribbon = false;
  uint32_t event = SCORING_EVENT_COUNT;
  __try {
    if (pri && IsReadable(reward, 0x2C) && IsReadable((uint8_t *)pri + 0x148, 1) &&
        ((uint8_t *)pri)[0x148] == 3) {
      tracked = true;
      event = (uint32_t)reward[0] & 0xff; // m_id: the row's Id byte
      unsigned count = 0;
      AcquireSRWLockExclusive(&g_eventTotalsLock);
      EventTotals *slot = nullptr;
      for (auto &t : g_eventTotals) {
        if (t.pri == pri) { slot = &t; break; }
        if (!slot && !t.pri) slot = &t;
      }
      if (slot) {
        slot->pri = pri;
        if (event < SCORING_EVENT_COUNT && slot->counts[event] < 0xFFFF)
          count = ++slot->counts[event];
      }
      ReleaseSRWLockExclusive(&g_eventTotalsLock);
      ribbon = LiveRibbonsEnabled() && event < SCORING_EVENT_COUNT && count > 0 &&
               g_ribbonRules[event].n > 0 && count % (unsigned)g_ribbonRules[event].n == 0;
      if (ribbon && RibbonPopupEnabled()) {
        ((uint8_t *)reward)[0x04] = 1;
        reward[4] = g_ribbonRules[event].score;
        ((uint8_t *)reward)[0x28] = 1;
      }
    }
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    tracked = ribbon = false;
  }
  g_origAssignScoringReward(pri, reward);
  if (!tracked)
    return;
  __try {
    int32_t xp = reward[5] + reward[6], cr = reward[7] + reward[8];
    if (xp > 0 || cr > 0) {
      AcquireSRWLockExclusive(&g_eventTotalsLock);
      for (auto &t : g_eventTotals)
        if (t.pri == pri) {
          t.xp += xp > 0 ? xp : 0;
          t.credits += cr > 0 ? cr : 0;
          t.events++;
          break;
        }
      ReleaseSRWLockExclusive(&g_eventTotalsLock);
    }
    if (ribbon) {
      SendRibbonAchievement(pri, event);
      if (RibbonPopupEnabled())
        Logf("ribbons: event %u -> ribbon popup (ClientEventResult m_ribbonAchieved, ribbon score %d)",
             event, g_ribbonRules[event].score);
    }
  } __except (EXCEPTION_EXECUTE_HANDLER) {
  }
}

// One more occurrence of a scoring event for this player, for an event the
// host itself never fires here (see MVP / Man Of The Day below).
static void AddScoringEvent(void *pri, uint32_t event) {
  if (!pri || event >= SCORING_EVENT_COUNT)
    return;
  AcquireSRWLockExclusive(&g_eventTotalsLock);
  EventTotals *slot = nullptr;
  for (auto &t : g_eventTotals) {
    if (t.pri == pri) { slot = &t; break; }
    if (!slot && !t.pri) slot = &t;
  }
  unsigned count = 0;
  if (slot) {
    slot->pri = pri;
    if (slot->counts[event] < 0xFFFF)
      count = ++slot->counts[event];
  }
  ReleaseSRWLockExclusive(&g_eventTotalsLock);
  MaybeAnnounceRibbon(pri, event, count);
}

// The player's summed event XP and credits; false when the host awarded none
// (hook off, or an older mmogbrain without XP in its table).
static bool TakeEventTotals(void *pri, int64_t *xp, int64_t *credits, int *events,
                            char *ev = nullptr, size_t evSize = 0) {
  bool found = false;
  if (ev && evSize)
    ev[0] = 0;
  AcquireSRWLockExclusive(&g_eventTotalsLock);
  for (auto &t : g_eventTotals) {
    if (t.pri == pri) {
      *xp = t.xp; *credits = t.credits; *events = t.events;
      // "ordinal.count,..." for mmogbrain's ribbons (the "ev" parameter).
      size_t o = 0;
      for (int i = 0; ev && i < SCORING_EVENT_COUNT && o + 16 < evSize; ++i)
        if (t.counts[i])
          o += _snprintf_s(ev + o, evSize - o, _TRUNCATE, "%s%d.%u", o ? "," : "", i, (unsigned)t.counts[i]);
      t = EventTotals{};
      found = true;
      break;
    }
  }
  ReleaseSRWLockExclusive(&g_eventTotalsLock);
  return found;
}

static void ReportMatchResult(void *orbitComp) {
  char pid[80], match[96], path[3584], body[2048];
  uint8_t *priOut = nullptr;
  int kills = 0, deaths = 0, assists = 0, team = 0, result = 0, score = 0;
  const char *teamSource = "none";
  float damage = 0, healing = 0;
  __try {
    uint8_t *pc = *(uint8_t **)((uint8_t *)orbitComp + OFF_COMPONENT_OWNER);
    if (!PlayerPIDForController(pc, pid, sizeof(pid))) {
      if (!RememberedControllerPID(pc, pid, sizeof(pid))) {
        Logf("match result: controller %p has no DNPID (connection URL unreadable "
             "and no loadout was served to it); not reported", pc);
        return;
      }
      Logf("match result: controller %p -- DNPID not readable from the connection "
           "at match end; using %s from its loadout", pc, pid);
    }
    uint8_t *pri = *(uint8_t **)(pc + OFF_PC_PLAYER_STATE);
    if (!IsReadable(pri, OFF_PRI_TEAM + 1)) {
      Logf("match result: %s has no player state; not reported", pid);
      return;
    }
    priOut = pri;
    kills = *(int32_t *)(pri + OFF_PRI_KILLS);
    deaths = *(int32_t *)(pri + OFF_PRI_DEATHS);
    assists = *(int32_t *)(pri + OFF_PRI_ASSISTS);
    damage = *(float *)(pri + OFF_PRI_DAMAGE_WEAPONS) +
             *(float *)(pri + OFF_PRI_DAMAGE_ABILITIES);
    score = *(int32_t *)(pri + OFF_PRI_SCORE);
    healing = *(float *)(pri + OFF_PRI_HEALING);
    team = pri[OFF_PRI_TEAM];
    teamSource = "PRI";
    if ((team < 1 || team > 2) && IsReadable(pc + OFF_PC_TEAM, 1)) {
      team = pc[OFF_PC_TEAM];
      teamSource = "controller";
    }
    uint8_t *gs = GameStateForPRI(pri);
    // One -MatchID per host process, but the process could play a second
    // round after a map travel; a new game state object is a new round, so
    // mmogbrain's (match, player) key does not swallow it.
    static uint8_t *s_lastGS = nullptr;
    if (gs && gs != s_lastGS) {
      s_lastGS = gs;
      ++s_round;
    }
    if (gs)
      result = gs[OFF_GS_FINAL_MATCH_RESULT];
    else
      Logf("match result: game state not found from PRI %p; reporting final=0", pri);
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    Logf("match result: EXCEPTION reading the result for %p", orbitComp);
    return;
  }
  if (!MatchIDFromCommandLine(match, sizeof(match))) {
    Logf("match result: no -MatchID= on the command line; not reported");
    return;
  }
  if (s_round > 1) {
    size_t ml = strlen(match);
    _snprintf_s(match + ml, sizeof(match) - ml, _TRUNCATE, "-r%d", s_round);
  }
  if (!(damage >= 0 && damage < 1e9f))
    damage = 0;
  if (!(healing >= 0 && healing < 1e9f))
    healing = 0;
  if (score < 0 || score > 10000000)
    score = 0;
  // Loadout ids are FName text; escape anything a query string cannot carry.
  char ships[1024];
  size_t o = 0;
  for (const char *c = FlownShipsFor(pid); *c && o + 4 < sizeof(ships); ++c) {
    if (isalnum((unsigned char)*c) || strchr("-_.,", *c))
      ships[o++] = *c;
    else
      o += _snprintf_s(ships + o, sizeof(ships) - o, _TRUNCATE, "%%%02X", (unsigned char)*c);
  }
  ships[o] = 0;
  _snprintf_s(path, sizeof(path), _TRUNCATE,
              "/battle/result?match=%s&pid=%s&team=%d&final=%d&kills=%d&deaths=%d"
              "&assists=%d&damage=%d&ships=%s",
              match, pid, team, result, kills, deaths, assists, (int)damage, ships);
  // The original table's XP and credits the host awarded this player (see
  // HookAssignScoringReward). Absent when none were awarded: mmogbrain then
  // pays its formula, as before.
  // Match Finish: every player who finishes earns it (mmogbrain counts it per
  // result); announced here, at the player's end of match.
  if (priOut)
    SendRibbonAchievement(priOut, SCORING_EVENT_MATCH_END);
  // MVP: the highest match score among the players (see AddScoringEvent).
  if (priOut && EomRibbonsEnabled()) {
    __try {
      uint8_t *gs = GameStateForPRI(priOut);
      if (gs && IsReadable(gs + 0x470, 16)) { // AGameState::PlayerArray (SDK)
        uint8_t **arr = *(uint8_t ***)(gs + 0x470);
        int count = *(int32_t *)(gs + 0x470 + 8);
        int best = 0;
        if (count > 0 && count <= 256 && IsReadable(arr, (size_t)count * 8))
          for (int i = 0; i < count; ++i)
            if (IsReadable(arr[i], OFF_PRI_SCORE + 4) &&
                ClassChainContains((UObjectMin *)arr[i], "YPlayerReplicationInfo") &&
                *(int32_t *)(arr[i] + OFF_PRI_SCORE) > best)
              best = *(int32_t *)(arr[i] + OFF_PRI_SCORE);
        if (best > 0 && score == best) {
          AddScoringEvent(priOut, SCORING_EVENT_MVP);
          Logf("ribbons: MVP -> %s (score %d, the match's highest)", pid, score);
        }
      }
    } __except (EXCEPTION_EXECUTE_HANDLER) {
    }
  }
  int64_t evXp = 0, evCr = 0;
  int evN = 0;
  char evCounts[SCORING_EVENT_COUNT * 10 + 8];
  if (priOut && TakeEventTotals(priOut, &evXp, &evCr, &evN, evCounts, sizeof(evCounts))) {
    size_t pl = strlen(path);
    // event_xp only when something paid XP or credits: its presence switches
    // mmogbrain from its formula to the table's figures.
    if (evN > 0)
      _snprintf_s(path + pl, sizeof(path) - pl, _TRUNCATE, "&event_xp=%lld&event_credits=%lld&events=%d",
                  (long long)evXp, (long long)evCr, evN);
    pl = strlen(path);
    if (evCounts[0])
      _snprintf_s(path + pl, sizeof(path) - pl, _TRUNCATE, "&ev=%s", evCounts);
  }
  // The kills by class, for mmogbrain's daily contracts (see RecordKill).
  char kl[KILL_LOG_MAX * 8 + 8];
  if (priOut && TakeKillLog(priOut, kl, sizeof(kl))) {
    size_t pl = strlen(path);
    _snprintf_s(path + pl, sizeof(path) - pl, _TRUNCATE, "&kl=%s", kl);
  }
  // Statistics screen: score, healing, seconds per ship class (ct= always
  // sent, empty when nothing was flown, so mmogbrain knows not to estimate).
  {
    char ct[CLASS_TIME_MAX * 16 + 8];
    ct[0] = 0;
    if (priOut)
      TakeClassTime(priOut, ct, sizeof(ct));
    size_t pl = strlen(path);
    _snprintf_s(path + pl, sizeof(path) - pl, _TRUNCATE, "&score=%d&heal=%d&ct=%s", score, (int)healing, ct);
  }
  bool ok = HttpGetLoopback(path, body, sizeof(body));
  if (ok && priOut && RewardsScreenEnabled())
    WriteEomRewards(priOut, body, match);
  for (char *c = body; *c; ++c)
    if (*c == '\n') *c = ' ';
  Logf("match result: %s team %d (%s) final %d kills %d deaths %d assists %d damage %d "
       "score %d healing %d event xp %lld credits %lld (%d events) ships [%s] -> %s%s",
       pid, team, teamSource, result, kills, deaths, assists, (int)damage, score, (int)healing,
       (long long)evXp, (long long)evCr, evN, FlownShipsFor(pid),
       ok ? "mmogbrain: " : "FAILED (mmogbrain unreachable or refused)", ok ? body : "");
  if (ok)
    ClearFlownShips(pid);
}

// ---------------------------------------------------------------------------
// End-of-match rewards screen (on; dn_host_no_eom_rewards.txt turns it off)
//
// The screen was empty although mmogbrain pays: the client reads its rewards
// from two structures that only the missing server build ever filled and
// finalized. Its own log says "IsInEndOfMatchDataFinalized - Credits info /
// Match XP is NOT finalized therefore the server closed before EOM was
// finalized" (AYPlayerControllerBase 0x5BBD20), and the host's says the same.
// Verified from the exe's reflection data (2026-09-28):
//
//   PRI +0x800  m_creditsInfo  FYCreditsInfo (0x28):  +0x00 m_credits
//               TArray<int32> per EYXPPoolType, +0x10 m_finalized, +0x18
//               m_battleID FString. Checked at PRI+0x810.
//   PRI +0x930  -> the XP manager; +0xF8 m_matchXPInfo FYMatchXPInfo (0x38):
//               +0x00 m_freeXP TArray<int32> per pool, +0x10 m_shipsXP
//               TArray<YMatchShipXP>, +0x20 m_finalized (checked by 0x401DE0
//               at +0x118), +0x28 m_battleID.
//   YMatchShipXP (0x28): +0x00 ship id (int, what 0x3FB0D0 matches),
//               +0x08 m_shipXp TArray<int32>, +0x18 m_freeXp TArray<int32>.
//
// mmogbrain answers /battle/result with the payout split into those pools
// (credit_pools / xp_pools / ship_xp_pools, 13 each) and the fleet's ship ids,
// and this writes them in with the engine's own array/string assignment, then
// sets both finalized flags -- so the screen shows what was actually paid.
// Replication to the client is the engine's (both are replicated properties).
// Not verified live yet.
// ---------------------------------------------------------------------------

#define OFF_PRI_CREDITS_INFO 0x800
#define OFF_PRI_XP_MANAGER 0x930
#define OFF_XPM_MATCH_XP 0xF8
#define REWARD_POOLS 13
#define MAX_FLEET_SHIPS 16
#define RVA_OPERATOR_NEW 0xE02DE0 // operator new(size) -> FMemory::Malloc

typedef void *(__fastcall *tOperatorNew)(size_t size);

static bool SwitchOn(const char *envName, const char *markerFile);

static bool RewardsScreenEnabled() {
  return !SwitchOn("DN_HOST_NO_EOM_REWARDS", "dn_host_no_eom_rewards.txt");
}

#define RVA_ACTOR_FLUSH_NET_DORMANCY 0x171C000
#define VT_ACTOR_FORCE_NET_UPDATE 0x580
typedef void(__fastcall *tActorVoid)(void *actor);

// PushActorNow asks the engine to replicate an actor on its next net tick:
// FlushNetDormancy, then ForceNetUpdate. 1 if both calls were made.
static int PushActorNow(void *actor) {
  if (!actor)
    return 0;
  __try {
    ((tActorVoid)(g_base + RVA_ACTOR_FLUSH_NET_DORMANCY))(actor);
    void **vt = *(void ***)actor;
    ((tActorVoid)vt[VT_ACTOR_FORCE_NET_UPDATE / 8])(actor);
    return 1;
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    Logf("eom rewards: EXCEPTION 0x%08X pushing actor %p", GetExceptionCode(), actor);
    return 0;
  }
}

static void WriteEomRewards(uint8_t *pri, const char *body, const char *match) {
  char v[512];
  int32_t credits[REWARD_POOLS] = {}, xp[REWARD_POOLS] = {}, shipXp[REWARD_POOLS] = {};
  int32_t zero[REWARD_POOLS] = {}, unplayed[REWARD_POOLS] = {};
  int32_t fleet[MAX_FLEET_SHIPS] = {}, flown[MAX_FLEET_SHIPS] = {};
  if (ParseInts(FieldValue(body, "credit_pools", v, sizeof(v)), credits, REWARD_POOLS) != REWARD_POOLS ||
      ParseInts(FieldValue(body, "xp_pools", v, sizeof(v)), xp, REWARD_POOLS) != REWARD_POOLS ||
      ParseInts(FieldValue(body, "ship_xp_pools", v, sizeof(v)), shipXp, REWARD_POOLS) != REWARD_POOLS) {
    Logf("eom rewards: mmogbrain sent no pools (older mmogbrain?); screen stays empty");
    return;
  }
  // What every fleet ship NOT flown earned (mmogbrain unplayedShipPools, 10% of
  // the XP without performance by default). Optional: an older mmogbrain does
  // not send it, and those ships then show 0, as before.
  bool haveUnplayed =
      ParseInts(FieldValue(body, "unplayed_ship_xp_pools", v, sizeof(v)), unplayed, REWARD_POOLS) == REWARD_POOLS;
  int nFleet = ParseInts(FieldValue(body, "fleet_ships", v, sizeof(v)), fleet, MAX_FLEET_SHIPS);
  int nFlown = ParseInts(FieldValue(body, "flown_ships", v, sizeof(v)), flown, MAX_FLEET_SHIPS);

  wchar_t wmatch[96];
  int ml = MultiByteToWideChar(CP_UTF8, 0, match, -1, wmatch, 96);
  FStringMin battleID = {wmatch, ml > 0 ? ml : 1, ml > 0 ? ml : 1};
  TArrayIntMin creditsArr = {credits, REWARD_POOLS, REWARD_POOLS};
  TArrayIntMin xpArr = {xp, REWARD_POOLS, REWARD_POOLS};
  TArrayIntMin shipArr = {shipXp, REWARD_POOLS, REWARD_POOLS};
  TArrayIntMin zeroArr = {zero, REWARD_POOLS, REWARD_POOLS};
  TArrayIntMin unplayedArr = {haveUnplayed ? unplayed : zero, REWARD_POOLS, REWARD_POOLS};
  tAssign fstr = (tAssign)(g_base + RVA_FSTRING_ASSIGN);
  tAssign tarr = (tAssign)(g_base + RVA_TARRAY_INT_ASSIGN);

  uint8_t *xpmOut = nullptr;
  __try {
    uint8_t *ci = pri + OFF_PRI_CREDITS_INFO;
    tarr(ci + 0x00, &creditsArr);
    ci[0x10] = 1;
    fstr(ci + 0x18, &battleID);

    uint8_t *xpm = *(uint8_t **)(pri + OFF_PRI_XP_MANAGER);
    xpmOut = xpm;
    if (!xpm || !IsReadable(xpm + OFF_XPM_MATCH_XP, 0x38)) {
      Logf("eom rewards: credits written; PRI %p has no XP manager, match XP not written", pri);
      return;
    }
    uint8_t *mx = xpm + OFF_XPM_MATCH_XP;
    tarr(mx + 0x00, &xpArr);
    uint8_t *ships = nullptr;
    if (nFleet > 0) {
      ships = (uint8_t *)((tOperatorNew)(g_base + RVA_OPERATOR_NEW))((size_t)nFleet * 0x28);
      if (!ships) {
        Logf("eom rewards: allocation failed; match XP not written");
        return;
      }
      memset(ships, 0, (size_t)nFleet * 0x28);
      for (int i = 0; i < nFleet; ++i) {
        uint8_t *e = ships + i * 0x28;
        *(int32_t *)e = fleet[i];
        bool wasFlown = false;
        for (int j = 0; j < nFlown; ++j)
          wasFlown |= flown[j] == fleet[i];
        tarr(e + 0x08, wasFlown ? &shipArr : &unplayedArr); // m_shipXp
        tarr(e + 0x18, &zeroArr);                        // m_freeXp
      }
    }
    // The previous (empty) array is not freed: it has nothing to free.
    TArrayIntMin *shipsXP = (TArrayIntMin *)(mx + 0x10);
    shipsXP->data = (int32_t *)ships;
    shipsXP->num = nFleet;
    shipsXP->max = nFleet;
    mx[0x20] = 1;
    fstr(mx + 0x28, &battleID);
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    Logf("eom rewards: EXCEPTION 0x%08X writing PRI %p", GetExceptionCode(), pri);
    return;
  }
  // Push both actors to the owning client NOW rather than whenever the net
  // driver next considers them. The rewards are written straight into memory;
  // replication picks the change up only on the actor's next net update, and
  // with only the delay to rely on the client's end-of-match init stage still
  // found no pools in 4 of 5 matches (ShipXpError at 22:29, 23:59, 02:03,
  // 08:18 on 2026-09-29/30, each with the rewards written 4.5-5 s before the
  // transition, for exactly the reported ship ids). FlushNetDormancy first,
  // in case an actor is dormant (a dormant actor does not replicate at all).
  // Both are the engine's own AActor functions, from their native
  // registrations (0x1D3810A / 0x1D3812C): ForceNetUpdate's exec thunk
  // 0x1E70010 ends in "jmp [vtable+0x580]" (a virtual); FlushNetDormancy's
  // thunk 0x1E6FF00 jumps to its body 0x171C000 (this).
  //
  // ONLY the PRI. The XP manager (PRI+0x930) is a UYXPManager : UActorComponent,
  // NOT an actor (SDK: DreadGame.YXPManager), so FlushNetDormancy and vtable
  // +0x580 do not exist on it -- slot +0x580 of a component's vtable is some
  // other virtual. The first build of this pushed it too, and the client's
  // end-of-match screen lost its Rewards tab (MVP/Stats/Scoreboard only,
  // operator 2026-09-30). A component replicates through its owner's actor
  // channel, so forcing the PRI covers m_matchXPInfo as well.
  // dn_host_no_eom_push.txt / DN_HOST_NO_EOM_PUSH=1 turns the push off, to
  // bisect it against the end-of-match screen without a rebuild.
  (void)xpmOut;
  int pushed = SwitchOn("DN_HOST_NO_EOM_PUSH", "dn_host_no_eom_push.txt") ? 0 : PushActorNow(pri);
  Logf("eom rewards: PRI %p written -- scoring pool credits %d xp %d, %d fleet ships "
       "(%d flown, unplayed share %d), both finalized; PRI pushed %d (FlushNetDormancy + ForceNetUpdate)",
       pri, credits[0], xp[0], nFleet, nFlown, haveUnplayed ? unplayed[0] : 0, pushed);
}

// ---------------------------------------------------------------------------
// Teams, names and bot balance, from the game-mode timer
// (on unless dn_host_no_team_sync.txt / DN_HOST_NO_TEAM_SYNC=1)
//
// Three gaps the host has because the original server build fed it match data:
//
//   Teams. The host never reads ?TEAM= (the only "TEAM=" in the exe is the
//   CLIENT's "TRAVEL %s?TEAM=%s"), and each human's PlayerState team
//   (AYPlayerReplicationInfo::m_team +0x940) stayed YT_NONE 0 -- so players
//   were missing from the scoreboard, results scored them as no team, and two
//   TDM players shared a side.
//
//   Names. Every join arrives with an empty ?Name= (the client's Browse
//   replaces it with its empty nickname), so AGameMode::InitNewPlayer named
//   players 257, 258. The name comes from mmogbrain by DNPID
//   (GET /battle/player), else the join URL's PlayerName=.
//
//   Bots. In the proving ground the bot targets are the mode's fixed team size
//   (gm+0x97C, copied to gm+0x980 / +0x984 by 0x3678F0); nothing subtracts the
//   humans, so three players still got 8 bots on their side. The game's own
//   SetTeamSizeAI (body 0x381550: sets the target, then 0x381FA0 trims surplus
//   bots) is called with teamSize - humans.
//
// CHANGED 2026-09-28: the first version did the team and name at PostLogin
// through the ProcessEvent hook, calling AYPlayerController::SetTeam. With it,
// only the LAST player to join got his own loadout (the others' ship picks
// never reached FindLoadoutByID -- host logs 01:07 and 01:09); with it off
// (DN_HOST_NO_TEAM_SYNC=1, 01:24) both players' fits registered. So this runs
// from the once-a-second game-mode timer instead, writes only the team bytes
// that are wrong (the controller already had the right team in the proving
// ground), and calls no SetTeam. NOT verified live yet.
#define OFF_GS_PLAYER_ARRAY 0x470 // AGameState::PlayerArray (SDK)
#define OFF_ACTOR_OWNER 0xC8      // AActor::Owner (SDK)
#define OFF_GM_TEAM_SIZE 0x97C
#ifndef OFF_GM_AI_TARGET_T1
#define OFF_GM_AI_TARGET_T1 0x980
#endif
#define RVA_SET_TEAM_SIZE_AI 0x381550

typedef void(__fastcall *tSetTeamSizeAI)(void *gameMode, int team, int size);
static bool SwitchOn(const char *envName, const char *markerFile);

struct PlayerSeen {
  void *pc;
  bool named;
  int lastTeamFix;
};
static PlayerSeen g_seen[64];

static PlayerSeen *SeenFor(void *pc) {
  PlayerSeen *freeSlot = nullptr;
  for (auto &p : g_seen) {
    if (p.pc == pc)
      return &p;
    if (!freeSlot && !p.pc)
      freeSlot = &p;
  }
  if (freeSlot) {
    freeSlot->pc = pc;
    freeSlot->named = false;
    freeSlot->lastTeamFix = -1;
  }
  return freeSlot;
}

// Captain appearance. AYPlayerReplicationInfo::m_captainDisplayInformation is
// a replicated FString with RepNotify at +0x758 (SDK DreadGame_Classes.h: Net,
// Transient, RepNotify). On a client OnRep_CaptainData builds the captain from
// it ("GENDER_x;#iiS=..#iiH=.."). The login-less host never fills it, so every
// client drew the other players with the default captain. The string is the
// player's saved one from mmogbrain (/battle/player captain=), data the
// original server had. From PR #80 (they resolved the offset by reflection).
// NOT verified live: that clients re-render the captain from the OnRep alone.
// Switch: dn_host_no_captain.txt / DN_HOST_NO_CAPTAIN.
#define OFF_PRI_CAPTAIN_DISPLAY 0x758
static void SetCaptainDisplayInfo(uint8_t *pc, const char *body) {
  if (SwitchOn("DN_HOST_NO_CAPTAIN", "dn_host_no_captain.txt"))
    return;
  char v[512];
  if (!FieldValue(body, "captain", v, sizeof(v)) || !v[0] || strlen(v) >= sizeof(v) - 1)
    return;
  uint8_t *pri = IsReadable(pc + OFF_PC_PLAYER_STATE, 8) ? *(uint8_t **)(pc + OFF_PC_PLAYER_STATE) : nullptr;
  if (!pri || !IsReadable(pri + OFF_PRI_CAPTAIN_DISPLAY, sizeof(FStringMin))) {
    Logf("captain: no player state on controller %p", pc);
    return;
  }
  static wchar_t w[512];
  int wl = MultiByteToWideChar(CP_UTF8, 0, v, -1, w, 512);
  if (wl <= 1)
    return;
  FStringMin tmp = {w, wl, wl};
  ((tAssign)(g_base + RVA_FSTRING_ASSIGN))(pri + OFF_PRI_CAPTAIN_DISPLAY, &tmp);
  Logf("captain: player state %p +0x%x = \"%s\"", pri, OFF_PRI_CAPTAIN_DISPLAY, v);
}

static void NamePlayer(uint8_t *gm, uint8_t *pc, PlayerSeen *seen) {
  seen->named = true; // one attempt per player, logged either way
  static wchar_t name[64];
  name[0] = 0;
  const char *source = "mmogbrain";
  char pid[80], path[160], body[1024], v[128];
  if (PlayerPIDForController(pc, pid, sizeof(pid))) {
    _snprintf_s(path, sizeof(path), _TRUNCATE, "/battle/player?pid=%s", pid);
    if (HttpGetLoopback(path, body, sizeof(body))) {
      if (FieldValue(body, "name", v, sizeof(v)) && v[0])
        MultiByteToWideChar(CP_UTF8, 0, v, -1, name, 32);
      SetCaptainDisplayInfo(pc, body);
    }
  }
  if (!name[0]) {
    source = "join URL";
    if (!UrlOptionWideForController(pc, L"PlayerName=", name, 32)) {
      Logf("names: no name for controller %p (pid %s): mmogbrain had none and "
           "the join URL has no PlayerName", pc, pid[0] ? pid : "<none>");
      return;
    }
  }
  static uint64_t s_changeName = 0;
  if (!s_changeName) {
    FNameMin n = {};
    ((tFNameCtor)(g_base + RVA_FNAME_CTOR_WIDE))(&n, L"ChangeName", 1 /* FNAME_Add */);
    s_changeName = *(uint64_t *)&n;
  }
  void *fn = ((tFindFunctionChecked)(g_base + RVA_FIND_FUNCTION_CHECKED))(gm, s_changeName);
  if (!fn) {
    Logf("names: ChangeName not found on game mode %p", gm);
    return;
  }
  // AGameMode_ChangeName_Params (SDK): Controller +0x00, FString +0x08,
  // bool bNameChange +0x18.
  struct {
    void *controller;
    FStringMin newName;
    uint8_t bNameChange;
    uint8_t pad[7];
  } parms = {};
  parms.controller = pc;
  int len = (int)wcslen(name);
  parms.newName = {name, len + 1, len + 1};
  void **vt = *(void ***)gm;
  ((tProcessEventVirt)vt[VT_PROCESS_EVENT / 8])(gm, fn, &parms);
  char narrow[64];
  WideCharToMultiByte(CP_UTF8, 0, name, -1, narrow, sizeof(narrow), nullptr, nullptr);
  Logf("names: controller %p is now \"%s\" (from %s)", pc, narrow, source);
}

static void FixTeam(uint8_t *pc, uint8_t *pri, PlayerSeen *seen) {
  char v[8];
  if (!UrlOptionForController(pc, L"TEAM=", v, sizeof(v)))
    return;
  int want = atoi(v);
  if (want < 1 || want > 2)
    return;
  int priTeam = pri[OFF_PRI_TEAM];
  int pcTeam = IsReadable(pc + OFF_PC_TEAM, 1) ? pc[OFF_PC_TEAM] : want;
  if (priTeam == want && pcTeam == want)
    return;
  pri[OFF_PRI_TEAM] = (uint8_t)want;
  if (IsReadable(pc + OFF_PC_TEAM, 1))
    pc[OFF_PC_TEAM] = (uint8_t)want;
  if (seen->lastTeamFix != want) // do not repeat the line every second
    Logf("team sync: controller %p TEAM=%d -- player state %d -> %d, controller %d -> %d",
         pc, want, priTeam, want, pcTeam, want);
  seen->lastTeamFix = want;
}

// Runs once a second on the game thread (HookGameModeTimer).
static void PlayersTick(uint8_t *gm) {
  if (!g_teamSyncArmed)
    return;
  __try {
    if (!IsReadable(gm + OFF_GM_GAMESTATE, 8))
      return;
    uint8_t *gs = *(uint8_t **)(gm + OFF_GM_GAMESTATE);
    if (!IsReadable(gs + OFF_GS_PLAYER_ARRAY, 16))
      return;
    uint8_t **arr = *(uint8_t ***)(gs + OFF_GS_PLAYER_ARRAY);
    int count = *(int32_t *)(gs + OFF_GS_PLAYER_ARRAY + 8);
    if (count <= 0 || count > 256 || !IsReadable(arr, (size_t)count * 8))
      return;
    int humans = 0;
    int humansOnTeam[3] = {0, 0, 0};
    for (int i = 0; i < count; ++i) {
      uint8_t *pri = arr[i];
      if (!IsReadable(pri, OFF_PRI_TEAM + 1))
        continue;
      uint8_t *pc = *(uint8_t **)(pri + OFF_ACTOR_OWNER);
      // Humans only: a player controller with a network connection -- not the
      // host's own local player, not a bot's AI controller.
      if (!IsReadable(pc, OFF_PC_NETCONNECTION + 8) ||
          !ClassChainContains((UObjectMin *)pc, "PlayerController") ||
          !*(void **)(pc + OFF_PC_NETCONNECTION))
        continue;
      ++humans;
      PlayerSeen *seen = SeenFor(pc);
      if (!seen)
        continue;
      FixTeam(pc, pri, seen);
      int t = pri[OFF_PRI_TEAM];
      if (t == 1 || t == 2)
        ++humansOnTeam[t];
      if (!seen->named)
        NamePlayer(gm, pc, seen);
    }

    // Humans take bot slots on their own team, in any mode with bots
    // (m_enableSpawnAI set): proving ground (all humans on team 1) and PvP.
    static int s_botBalance = -1;
    if (s_botBalance < 0)
      s_botBalance = SwitchOn("DN_HOST_NO_BOT_BALANCE", "dn_host_no_bot_balance.txt") ? 0 : 1;
    if (s_botBalance && IsReadable(gm + OFF_GM_ENABLE_SPAWN_AI, 1) && gm[OFF_GM_ENABLE_SPAWN_AI]) {
      int size = *(int32_t *)(gm + OFF_GM_TEAM_SIZE);
      for (int team = 1; team <= 2; ++team) {
        int target = *(int32_t *)(gm + OFF_GM_AI_TARGET_T1 + (team - 1) * 4); // +0x980, +0x984
        int want = size - humansOnTeam[team];
        if (want < 0)
          want = 0;
        // target > 0: the bots have been set up (0x3678F0 ran); before that a
        // call would fill early.
        if (size > 0 && target > 0 && humansOnTeam[team] > 0 && target != want) {
          ((tSetTeamSizeAI)(g_base + RVA_SET_TEAM_SIZE_AI))(gm, team, want);
          Logf("bot balance: %d human(s) on team %d; team size %d -> team %d bots %d -> %d",
               humansOnTeam[team], team, size, team, target, want);
        }
      }
    }
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    Logf("players tick: EXCEPTION on game mode %p", gm);
  }
}

// Seconds per ship class (see AddClassTime). Runs from the same once-a-second
// game-mode timer, timed by the tick count rather than assumed to be 1 s.
static void ClassTimeTick(uint8_t *gm) {
  static DWORD s_last = 0;
  static void *s_lastGM = nullptr;
  DWORD now = GetTickCount();
  DWORD elapsed = (s_lastGM == gm && s_last) ? now - s_last : 0;
  s_last = now;
  s_lastGM = gm;
  if (!elapsed)
    return;
  __try {
    if (!IsReadable(gm + OFF_GM_GAMESTATE, 8))
      return;
    uint8_t *gs = *(uint8_t **)(gm + OFF_GM_GAMESTATE);
    if (!IsReadable(gs + OFF_GS_PLAYER_ARRAY, 16))
      return;
    uint8_t **arr = *(uint8_t ***)(gs + OFF_GS_PLAYER_ARRAY);
    int count = *(int32_t *)(gs + OFF_GS_PLAYER_ARRAY + 8);
    if (count <= 0 || count > 256 || !IsReadable(arr, (size_t)count * 8))
      return;
    for (int i = 0; i < count; ++i) {
      uint8_t *pri = arr[i];
      if (!IsReadable(pri, OFF_PRI_TEAM + 1))
        continue;
      uint8_t *pc = *(uint8_t **)(pri + OFF_ACTOR_OWNER);
      if (!IsReadable(pc, OFF_PC_NETCONNECTION + 8) ||
          !ClassChainContains((UObjectMin *)pc, "PlayerController") ||
          !*(void **)(pc + OFF_PC_NETCONNECTION) || !IsReadable(pc + OFF_CONTROLLER_PAWN, 8))
        continue;
      uint8_t *pawn = *(uint8_t **)(pc + OFF_CONTROLLER_PAWN);
      if (pawn && IsReadable(pawn + OFF_PAWN_SHIP_CLASS, 1))
        AddClassTime(pri, pawn[OFF_PAWN_SHIP_CLASS], elapsed);
    }
  } __except (EXCEPTION_EXECUTE_HANDLER) {
  }
}

static bool SwitchOn(const char *envName, const char *markerFile);

// The end-of-match TRANSITION itself is deferred, and the rewards go first.
//
// The client gathers its rewards in UYEndOfMatchInitStage (0x32D4B0 ->
// 0x34A1F0 -> UYEndOfMatchDataManager::GatherEomReward 0x340C40), the FIRST
// stage of the end-of-match flow that ClientStartEndOfMatchTransition starts.
// For every fleet ship it looks the ship up in m_shipsXP (0x3FB0D0) and needs
// exactly 13 pools (the EYXPPoolType::MAX array built by 0x332690), else it
// files "ShipXpError: Ship XP pools were not gathered correctly" per ship.
// The rewards reach the client by property replication, so they must be
// written BEFORE the transition RPC leaves, with time to replicate.
//
// History, both verified from client_reports against this log:
//   2026-09-28: stats RPC, then rewards -> a ShipXpError batch per player,
//     0.5 s after "eom rewards: written".
//   2026-09-29 morning: rewards, then the stats RPC 3 s later -> the SAME
//     batches, now 3 s BEFORE "sent ... (deferred)": the gather had already
//     run at the transition, which went out untouched before the write.
// Now: rewards at once, then after EOM_TRANSITION_DELAY_MS the original
// transition, and the stats RPC right after it (the order that fixed the
// black screen). Sent from the game mode's once-a-second timer, which keeps
// running after the match (verified 2026-09-29: "sent ... (deferred)" lines).
// dn_host_eom_no_defer.txt / DN_HOST_EOM_NO_DEFER=1 restores the old flow
// (transition, stats, then rewards).
#define EOM_TRANSITION_DELAY_MS 3000
struct PendingEomTransition {
  void *orbitComp;
  uint8_t *pc;
  uint8_t *pri;
  DWORD due;
};
static PendingEomTransition g_pendingEom[32];

// Personal stats page (the end-of-match "Player Stats" rows).
//
// The client fills UYWidget_EndOfMatchPlayerStatsPage from the array this RPC
// delivers (PRI+0x7D0, set by ClientSetTopPlayerMatchStats; 0x4654D0 makes one
// row per entry, NO filter by player) and each row (0x469280) reads +0x38 the
// comparison, +0x3C the value (float, shown as a number), +0x18 the label
// (FText) and +0x40 the difference to average (%.1f). The code that computed
// these -- UYPlayerMatchStatisticsManager, RetrieveTopStatsForPlayers -- is not
// in this exe (none of its log strings is referenced): it was server-build
// code. So the mod builds the rows from the player's own PRI.
//
// FYPlayerMatchStat, 0x48 bytes (reflection 0x721FF0):
//   +0x00 m_pid (8 bytes, unused by the page)
//   +0x08 m_category FYPlayerMatchStatisticsCategory (reflection 0x7227A0):
//         +0x08 vptr (FTableRowBase), +0x10 m_ID (byte), +0x18 m_name (FText,
//         0x18), +0x30 m_priority (int)
//   +0x38 m_comparison (byte)  +0x3C m_stat (float)  +0x40 m_statDiffToAvg
//   (float)  +0x44 m_medal (byte)
// Enums by registration order (0x6911EE): comparison NONE 0 .. PureStat 5;
// category NONE 0, Assists 1, Kills 2, DoubleKills 3, DamageCausedDestroyer 4
// .. DamageCausedByAbilities 9, PowerUsage 10, Healing 11, ...
//
// Rows: the three categories the PRI holds -- Kills (+0x848), Assists
// (+0x858), "Damage with Modules" (DamageCausedByAbilities, +0x90C). Weapon
// damage is one total on the PRI while the game splits it per target class, so
// it is left out rather than split by guesswork. Labels are the English names
// in DN_PlayerMatchStatistics_DT. Comparison PureStat: the value alone (no
// average to compare against). The label FText is made by the engine's own
// Conv_StringToText body (thunk 0x1E63460 -> 0x19D21F0, FText* (FText* out,
// const FString* in)), so it is a real, ref-counted FText. The rows are
// engine-allocated and never freed (a few hundred bytes per match); the local
// copy in the PRI is the engine's own.
#define RVA_TEXT_FROM_STRING 0x19D21F0
typedef void *(__fastcall *tTextFromString)(void *outText, FStringMin *in);
#define STAT_COMPARISON_PURE 5

static int BuildPlayerMatchStats(uint8_t *pri, TArrayIntMin *out) {
  struct Row {
    uint8_t id;
    const wchar_t *label;
    float value;
  } rows[3];
  int n = 0;
  __try {
    rows[n++] = {2, L"Kills", (float)*(int32_t *)(pri + OFF_PRI_KILLS)};
    rows[n++] = {1, L"Assists", (float)*(int32_t *)(pri + OFF_PRI_ASSISTS)};
    rows[n++] = {9, L"Damage with Modules", *(float *)(pri + OFF_PRI_DAMAGE_ABILITIES)};
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    return 0;
  }
  tOperatorNew alloc = (tOperatorNew)(g_base + RVA_OPERATOR_NEW);
  uint8_t *arr = (uint8_t *)alloc((size_t)n * 0x48);
  if (!arr)
    return 0;
  memset(arr, 0, (size_t)n * 0x48);
  tTextFromString mkText = (tTextFromString)(g_base + RVA_TEXT_FROM_STRING);
  for (int i = 0; i < n; ++i) {
    uint8_t *e = arr + i * 0x48;
    e[0x10] = rows[i].id;
    int len = lstrlenW(rows[i].label) + 1;
    wchar_t *buf = (wchar_t *)alloc((size_t)len * sizeof(wchar_t));
    if (!buf)
      return 0;
    memcpy(buf, rows[i].label, (size_t)len * sizeof(wchar_t));
    FStringMin str = {buf, len, len};
    mkText(e + 0x18, &str);
    e[0x38] = STAT_COMPARISON_PURE;
    *(float *)(e + 0x3C) = rows[i].value;
  }
  out->data = (int32_t *)arr;
  out->num = n;
  out->max = n;
  return n;
}

static void SendTopPlayerMatchStats(uint8_t *pc, uint8_t *pri, const char *how) {
  __try {
    if (!IsReadable(pc, OFF_PC_NETCONNECTION + 8) || *(uint8_t **)(pc + OFF_PC_PLAYER_STATE) != pri ||
        !IsReadable(pri, 0x800)) {
      Logf("eom stats: controller %p / PRI %p is gone; not sent (%s)", pc, pri, how);
      return;
    }
    uint64_t name = *(uint64_t *)(g_base + RVA_FNAME_CLIENT_SET_TOP_PLAYER_MATCH_STATS);
    void *fn = ((tFindFunctionChecked)(g_base + RVA_FIND_FUNCTION_CHECKED))(pri, name);
    if (!fn) {
      Logf("eom stats: ClientSetTopPlayerMatchStats not found on %p; not sent", pri);
      return;
    }
    int rows = 0;
    TArrayIntMin parms = {nullptr, 0, 0}; // TArray<FYPlayerMatchStat>
    if (!SwitchOn("DN_HOST_EMPTY_EOM_STATS", "dn_host_empty_eom_stats.txt"))
      rows = BuildPlayerMatchStats(pri, &parms);
    void **vt = *(void ***)pri;
    ((tProcessEventVirt)vt[VT_PROCESS_EVENT / 8])(pri, fn, &parms);
    Logf("eom stats: sent ClientSetTopPlayerMatchStats (%d personal stat rows, %s) to "
         "controller %p PRI %p -- the client's SetupUIWidgets stage waits for it",
         rows, how, pc, pri);
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    Logf("eom stats: EXCEPTION sending ClientSetTopPlayerMatchStats for %p", pc);
  }
}

// The deferred half: the game's own transition, then the stats RPC.
static void SendEomTransition(void *orbitComp, uint8_t *pc, uint8_t *pri, const char *how) {
  bool ok = false;
  __try {
    ok = IsReadable(pc, OFF_PC_ORBIT_COMPONENT + 8) &&
         *(void **)(pc + OFF_PC_ORBIT_COMPONENT) == orbitComp;
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    ok = false;
  }
  if (!ok) {
    Logf("eom transition: controller %p left before its end-of-match transition (%s)", pc, how);
    return;
  }
  g_origClientStartEomTransition(orbitComp);
  Logf("eom transition: ClientStartEndOfMatchTransition sent to controller %p (%s)", pc, how);
  SendTopPlayerMatchStats(pc, pri, how);
}

// Game thread, once a second (HookGameModeTimer).
static void FlushPendingEomStats() {
  DWORD now = GetTickCount();
  for (auto &p : g_pendingEom) {
    if (p.pc && (int32_t)(now - p.due) >= 0) {
      PendingEomTransition q = p;
      p.pc = nullptr;
      SendEomTransition(q.orbitComp, q.pc, q.pri, "deferred");
    }
  }
}

static void __fastcall HookClientStartEomTransition(void *orbitComp) {
  uint8_t *pc = nullptr, *pri = nullptr;
  __try {
    pc = *(uint8_t **)((uint8_t *)orbitComp + OFF_COMPONENT_OWNER);
    if (!IsReadable(pc, OFF_PC_ORBIT_COMPONENT + 8) ||
        *(void **)(pc + OFF_PC_ORBIT_COMPONENT) != orbitComp) {
      Logf("eom stats: orbit component %p has no matching controller; not sent",
           orbitComp);
      pc = nullptr;
    } else if (!*(void **)(pc + OFF_PC_NETCONNECTION)) {
      pc = nullptr; // the host's own local player; its flow is not what anyone sees
    } else {
      pri = *(uint8_t **)(pc + OFF_PC_PLAYER_STATE);
      if (!IsReadable(pri, 0x800)) {
        Logf("eom stats: controller %p has no player state; not sent", pc);
        pc = nullptr;
      }
    }
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    Logf("eom stats: EXCEPTION reading the controller of %p", orbitComp);
    pc = nullptr;
  }
  bool noDefer = SwitchOn("DN_HOST_EOM_NO_DEFER", "dn_host_eom_no_defer.txt");
  if (!pc || noDefer) {
    // Local player, unknown controller, or the old flow: transition now.
    g_origClientStartEomTransition(orbitComp);
    if (pc)
      SendTopPlayerMatchStats(pc, pri, "immediate");
  }
  // Rewards (ReportMatchResult writes them) -- before the deferred transition.
  if (!SwitchOn("DN_HOST_NO_MATCH_RESULT", "dn_host_no_match_result.txt"))
    ReportMatchResult(orbitComp);
  else
    Logf("match result: OFF (dn_host_no_match_result.txt / DN_HOST_NO_MATCH_RESULT=1)");
  if (!pc || noDefer)
    return;
  for (auto &p : g_pendingEom) {
    if (!p.pc) {
      p = {orbitComp, pc, pri, GetTickCount() + EOM_TRANSITION_DELAY_MS};
      Logf("eom transition: controller %p held for %d ms so its rewards replicate "
           "before the client's end-of-match init stage gathers them",
           pc, EOM_TRANSITION_DELAY_MS);
      return;
    }
  }
  SendEomTransition(orbitComp, pc, pri, "queue full, immediate");
}

// Both of the switches below are opt-in separately from the loadout fix,
// because both change what players see.
// ---------------------------------------------------------------------------
// Scoring table (issue #67): every score was 0.
//
// The host scores every event (kill, assist, ...) from a table it copies out of
// the YMmogbrain subsystem: UYScoringEventManager::InitializeData (0x423610) ->
// 0x423590 -> 0x423450 copies subsystem +0x43F8/+0x4408/+0x4460, but only when
// the "present" flag at +0x4470 is set. The one writer of that block is the
// reply parser 0x2A75740, which a logged-in game runs when mmogbrain answers
// its scoring request (dispatcher 0x2A2574D, slot +0x3680). The host never logs
// in, so the flag stayed 0 and every event was worth 0 -- kills, deaths and
// damage were counted, the SCORE was not. (Verified from the exe 2026-09-28.)
//
// So, before InitializeData runs, this fetches the scoring document from
// mmogbrain (GET /battle/scoring, loopback) and feeds it through the client's
// OWN decoder and parser -- supplying the data a logged-in game would have had,
// not writing a score:
//
//   1. A one-slot response holder, laid out like the client's (slot array at
//      +0x27F0, 0x88 bytes each): key +0x00, state +0x14, chunk list +0x18
//      (each chunk = next pointer + 0x7FF8 data bytes), length +0x7C. The
//      reader's Read (0x2A7BE30 -> 0x2A7BFB0) only walks that chunk list.
//   2. A reader initialised by 0x2A57080 and pointed at it the way 0x2A65850
//      does after matching a request id: +0x38 holder, +0x40 slot, +0x08 = 0.
//   3. 0x2A3E450(reader, root) decodes the document into the node tree, as
//      the dispatcher does for every reply (0x2A23539).
//   4. 0x2A75740(subsystem + 0x43F8, root) parses ScoringTable and
//      ScoringParamsTable and sets the flag (+0x78 of that block = +0x4470).
//
// The subsystem is found exactly as 0x423450 finds it: FName("YMmogbrain")
// (0xC9CF20) -> FindModule (0xCA9060) -> vtable +0x48.
// ---------------------------------------------------------------------------

#define RVA_SCORING_INIT_DATA 0x423610 // UYScoringEventManager::InitializeData
#define RVA_SCORING_COPY 0x423450      // copies the subsystem block; arg 2 = mode name
#define RVA_FNAME_FROM_ANSI 0xC9CF20
#define RVA_FIND_MODULE 0xCA9060
#define RVA_MMOG_READER_INIT 0x2A57080
#define RVA_MMOG_DECODE 0x2A3E450
#define RVA_SCORING_PARSE 0x2A75740
#define OFF_MMOG_SCORING 0x43F8
#define OFF_MMOG_SCORING_PRESENT 0x4470
#define OFF_MMOG_SLOTS 0x27F0
#define MMOG_SLOT_SIZE 0x88
#define MMOG_CHUNK_DATA 0x7FF8

typedef void *(__fastcall *tFNameFromAnsi)(void *out, const char *name, int findType);
typedef void *(__fastcall *tFindModule)(uint64_t name);
typedef void *(__fastcall *tModuleGetClient)(void *module);
typedef void *(__fastcall *tMmogReaderInit)(void *reader);
typedef void(__fastcall *tMmogDecode)(void *reader, void *root);
typedef void(__fastcall *tScoringParse)(void *dest, void *root);
typedef void(__fastcall *tScoringInitData)(void *mgr);
typedef void(__fastcall *tScoringCopy)(void *table, void *modeName);

static tScoringInitData g_origScoringInitData = nullptr;
static tScoringCopy g_origScoringCopy = nullptr;

static uint8_t *MmogClient() {
  uint64_t name[2] = {};
  void *fn = ((tFNameFromAnsi)(g_base + RVA_FNAME_FROM_ANSI))(name, "YMmogbrain", 1);
  if (!fn)
    return nullptr;
  void *module = ((tFindModule)(g_base + RVA_FIND_MODULE))(*(uint64_t *)fn);
  if (!module || !IsReadable(module, 8))
    return nullptr;
  void **vt = *(void ***)module;
  if (!IsReadable(vt, 0x50))
    return nullptr;
  return (uint8_t *)((tModuleGetClient)vt[0x48 / 8])(module);
}

// Runs the document through the client's decoder and scoring parser. Returns
// the number of rows the parser stored, or -1.
static int ParseScoringDocument(uint8_t *client, const uint8_t *doc, int len) {
  // Response holder with one slot. Deliberately never freed: the parser copies
  // what it needs, and this runs once per host process.
  uint8_t *holder = (uint8_t *)calloc(1, OFF_MMOG_SLOTS + MMOG_SLOT_SIZE);
  if (!holder)
    return -1;
  uint8_t *slot = holder + OFF_MMOG_SLOTS;
  *(int32_t *)(slot + 0x14) = 6; // complete
  void **link = (void **)(slot + 0x18);
  for (int off = 0; off < len; off += MMOG_CHUNK_DATA) {
    int n = len - off < MMOG_CHUNK_DATA ? len - off : MMOG_CHUNK_DATA;
    uint8_t *chunk = (uint8_t *)calloc(1, 8 + MMOG_CHUNK_DATA);
    if (!chunk)
      return -1;
    memcpy(chunk + 8, doc + off, n);
    *link = chunk;
    link = (void **)chunk;
  }
  *(int32_t *)(slot + 0x7C) = len;

  uint8_t *reader = (uint8_t *)calloc(1, 0x100);
  uint8_t *root = (uint8_t *)calloc(1, 0x100);
  if (!reader || !root)
    return -1;
  ((tMmogReaderInit)(g_base + RVA_MMOG_READER_INIT))(reader);
  *(void **)(reader + 0x38) = holder;
  *(int32_t *)(reader + 0x40) = 0;
  *(int32_t *)(reader + 0x08) = 0;

  __try {
    ((tMmogDecode)(g_base + RVA_MMOG_DECODE))(reader, root);
    Logf("scoring: decoded %d bytes -> root has %d fields", len, *(int32_t *)(root + 0x20));
    ((tScoringParse)(g_base + RVA_SCORING_PARSE))(client + OFF_MMOG_SCORING, root);
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    Logf("scoring: EXCEPTION 0x%08X while decoding/parsing the table", GetExceptionCode());
    return -1;
  }
  return *(int32_t *)(client + OFF_MMOG_SCORING + 8);
}

static void FeedScoringTable() {
  uint8_t *client = nullptr;
  __try {
    client = MmogClient();
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    client = nullptr;
  }
  if (!client || !IsReadable(client + OFF_MMOG_SCORING, 0x80)) {
    Logf("scoring: YMmogbrain subsystem not found; scores stay 0");
    return;
  }
  if (client[OFF_MMOG_SCORING_PRESENT]) {
    Logf("scoring: table already present (%d rows)", *(int32_t *)(client + OFF_MMOG_SCORING + 8));
    return;
  }
  static char buf[256 * 1024];
  const char *body;
  int n;
  if (!HttpGetLoopbackRaw("/battle/scoring", buf, (int)sizeof(buf), &body, &n) || n <= 6) {
    Logf("scoring: GET /battle/scoring failed; scores stay 0");
    return;
  }
  int rows = ParseScoringDocument(client, (const uint8_t *)body, n);
  Logf("scoring: table %s -- %d rows, present flag %d",
       rows > 0 && client[OFF_MMOG_SCORING_PRESENT] ? "LOADED" : "NOT loaded", rows,
       client[OFF_MMOG_SCORING_PRESENT]);
}

static void __fastcall HookScoringInitData(void *mgr) {
  FeedScoringTable();
  g_origScoringInitData(mgr);
}

// Logs the mode name the table is filtered by: a row counts only when its
// GameModes lists it (0x424190). mmogbrain lists every alias; this line shows
// which one the host actually uses.
static void __fastcall HookScoringCopy(void *table, void *modeName) {
  const wchar_t *mode = L"";
  __try {
    if (modeName && *(int32_t *)((uint8_t *)modeName + 8) > 0)
      mode = *(const wchar_t **)modeName;
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    mode = L"?";
  }
  // Team Elimination has NO mode name, and with the table loaded both TE
  // matches crashed the host a few minutes in (11:40 and 16:02 on 2026-09-28:
  // EXCEPTION_ACCESS_VIOLATION reading 0xfffffff8, the same in both); the two
  // TE matches before the table existed ended cleanly. An empty mode name
  // makes the filter (0x424190) take EVERY row, so TE ran the table with no
  // mode context. GUESS: some reward/ribbon path looks the mode up and reads
  // through a missing entry (null - 8). The crashing function is not
  // identified (no minidump: this DLL stubs WER). So an empty mode builds no
  // table -- TE scores 0, as before, and does not crash.
  //
  // CHANGED 2026-09-29: hosts now report TE's mode as "TE" (the ?game=TE
  // match URL), so the empty-name guard stopped firing and the table was built
  // for TE again -- and ALL 7 TE matches since 2026-09-28 crashed the same way
  // (7 of 7 AVs reading 0xfffffff8 were ?game=TE; no TDM match had one), 3-5
  // min in: about when TE's first ROUND ends. GUESS: a round end scores an
  // event the table leaves out on purpose (Winner/MatchEnd, excluded because
  // they overflowed the host at EndMatch) and reads through the missing row.
  // Until the crash handler below names the function, TE gets no table.
  //
  // RE-ENABLED 2026-10-26 (dn_host_no_te_scoring.txt / DN_HOST_NO_TE_SCORING=1
  // restores the skip). TE was the only mode with no scoring at all (4 results,
  // 0 events, against 108 of 111 in TDM). The likeliest cause of those crashes
  // is the ClientAddAchievement loop (the re-entrancy guard on 0x5E50C0
  // below, wrongly first named ClientApplyRespawnFilter): every ribbon for the
  // host's own local player called the RPC stub again, forever. TDM met it at
  // EndMatch, where its ribbons go; TE with a table awarded ribbons mid-match,
  // "3-5 min in" -- there were no TE rounds then for a round end to explain it
  // (the game mode is stripped, see "Team Elimination rounds"). The guard is
  // installed on every host now and fires (45 times in one host log); it was
  // written the day after the TE skip (2026-09-29), so every TE match since has
  // run without the table and the guard was never tested against TE. NOT
  // verified in a TE match.
  //
  // WRONG (2026-10-26): with the table back, both TE matches crashed the same
  // way at the player's first capital-ship kill (CaptainKill_SameTier and
  // FinalBlow scored, the fault within 1 ms). The cause is the TE-only
  // Predator row: its scorer 0x3EFF60 reads the last m_rounds entry with no
  // empty check (see TeAppendRound). The TE section now keeps that entry open.
  bool isTE = mode[0] == L'T' && mode[1] == L'E' && mode[2] == 0;
  static int s_teScoringOff = -1;
  if (s_teScoringOff < 0)
    s_teScoringOff = SwitchOn("DN_HOST_NO_TE_SCORING", "dn_host_no_te_scoring.txt") ? 1 : 0;
  if (!mode[0] || (isTE && s_teScoringOff)) {
    Logf("scoring: mode \"%ls\" -- table not built, scores stay 0 in this mode%s", mode,
         isTE ? " (dn_host_no_te_scoring.txt)" : " (no mode name)");
    return;
  }
  Logf("scoring: building the match table for mode \"%ls\"", mode);
  g_origScoringCopy(table, modeName);
  LoadRibbonRules(mode);
}

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
// ===========================================================================
// Havoc (from PR #80, starveil-bunny). Wave director, wave and kill XP,
// between-wave upgrades, respawn ship switch. Installed only on hosts started
// with -GameMode=Havoc; dn_host_no_havoc_director.txt /
// DN_HOST_NO_HAVOC_DIRECTOR=1 turns all of it off.
// ===========================================================================
#define RVA_SCENE_START_COMBAT 0x2681A0    // (sceneMgr, bool notifyLevelScript); no-op unless +0x444
#define OFF_SCENE_COMBAT_NOT_STARTED 0x444 // byte; tick spawns nothing while set
typedef void(__fastcall *tSceneStartCombat)(void *scene, uint8_t notifyLevelScript);
typedef void(__fastcall *tExecThunk)(void *ctx, uint8_t *stack, void *result);
static inline int HvcMax(int a, int b) { return a > b ? a : b; }

// ---------------------------------------------------------------------------
// Havoc respawn ship (UYLoadoutManager::ActivateLoadout, 0x336C90)
//
// Picking another ship on the Havoc respawn screen shows its icon but
// respawns the old one; the host logs "ActivateLoadout | Loadout
// [Default__VH_HVC_UTI_AssaultM_Loadout_T2_BP_C] is not valid" per attempt.
// ActivateLoadout(mgr, loadout, bool r8b) (.pdata ENTRY 0x336C90-0x336D9A)
// bails unless 0x33C680(mgr, loadout) is true; on success it stores the
// loadout at mgr+0x208 (0x336DBB).
//
// 0x33C680:
//   1. scan every manager group (+0x108 array, count +0x110, 0x30-byte
//      entries: TArray of loadout ptrs, type byte +0x28) for the loadout;
//   2. no filter (mgr+0x200 null): found in a type-4 group (0x33C840
//      collects type 4, 0x33C8C0 hit -> 0x33C825) -> FALSE; not found ->
//      0x33CA66 -> TRUE;
//   3. with a filter: found in a type-2 group and filter+0x28 == 0 -> FALSE;
//      otherwise switch on loadout+0xD8 (ship class, 1..15, table 0x33CA90):
//      each case needs two filter bytes in +0x29..+0x3C (family + class
//      allow flags) -> true only if both set; out of range -> TRUE.
// Havoc clears the filter at match start ("ClearLoadoutFilter | In Havoc we
// do not want to reactivate the previous loadout ID here!"), and the five
// Havoc ships sit in a type-4 group (LoadLoadoutFilterLoadouts, "Loading
// loadout filter VH_LoadoutFilter_Havoc_DA"), so every respawn pick fails
// step 2. With the filter still set, the first pick passes. Whatever the
// original server did on respawn (restore the filter or bypass the gate) is
// not in this exe.
//
// The loadout object is a native YShipLoadout named Default__VH_HVC_*_C, so
// a class or name-prefix test does not identify it. The hook keys on the
// failing arm instead: gate FALSE with no filter. While the original
// ActivateLoadout runs, the gate is hooked to answer TRUE for exactly that
// loadout. Installed only on Havoc hosts. Logs every call (first 40).
// ---------------------------------------------------------------------------
#define RVA_ACTIVATE_LOADOUT 0x336C90
#define RVA_LOADOUT_IS_REGISTERED 0x33C680
#define OFF_LOADOUT_MGR_FILTER 0x200
#define OFF_LOADOUT_MGR_ACTIVE 0x208
typedef void(__fastcall *tActivateLoadout)(void *mgr, void *loadout, uint8_t flag);
static tActivateLoadout g_origActivateLoadout = nullptr;

typedef bool(__fastcall *tLoadoutGate)(void *mgr, void *loadout);
static tLoadoutGate g_origLoadoutGate = nullptr;
static void *volatile g_forceValidLoadout = nullptr;

static bool __fastcall HookLoadoutGate(void *mgr, void *loadout) {
  if (loadout && loadout == g_forceValidLoadout)
    return true;
  return g_origLoadoutGate(mgr, loadout);
}

static void __fastcall HookActivateLoadout(void *mgr, void *loadout, uint8_t flag) {
  static int s_logged = 0;
  uint8_t *m = (uint8_t *)mgr;
  if (!m || !loadout || !IsReadable(m + OFF_LOADOUT_MGR_ACTIVE, 8) || !IsReadable(loadout, sizeof(UObjectMin))) {
    g_origActivateLoadout(mgr, loadout, flag);
    return;
  }
  bool valid = g_origLoadoutGate(mgr, loadout);
  void *filter = *(void **)(m + OFF_LOADOUT_MGR_FILTER);
  const char *name = NameText(((UObjectMin *)loadout)->Name);
  const char *fname = filter && IsReadable(filter, sizeof(UObjectMin)) ? NameText(((UObjectMin *)filter)->Name) : nullptr;
  // The respawn screen offers only the five VH_HVC_* ships the filter's
  // m_specialLoadouts lists.
  // GUESS: no non-Havoc-ship ActivateLoadout reaches here with filter null.
  bool force = !valid && !filter;
  if (force)
    g_forceValidLoadout = loadout;
  g_origActivateLoadout(mgr, loadout, flag);
  if (force)
    g_forceValidLoadout = nullptr;
  if (s_logged < 40) {
    ++s_logged;
    void *active = *(void **)(m + OFF_LOADOUT_MGR_ACTIVE);
    const char *an = active && IsReadable(active, sizeof(UObjectMin)) ? NameText(((UObjectMin *)active)->Name) : nullptr;
    Logf("loadout: ActivateLoadout(%p, %s, %u) gate %d, filter %s, class byte %u%s -> active %s", mgr,
         name ? name : "?", flag, valid, fname ? fname : (filter ? "?" : "none"), ((uint8_t *)loadout)[0xD8],
         force ? ", gate forced for Havoc ship" : "", an ? an : "?");
  }
}

// Offset_Internal (+0x50) of the reflected property `name` on obj's class
// chain, or -1.
static int PropOffset(UObjectMin *obj, const char *name) {
  if (!IsReadable(obj, sizeof(UObjectMin)))
    return -1;
  for (UObjectMin *cls = obj->Class; cls && IsReadable(cls, 0x40); cls = *(UObjectMin **)((uint8_t *)cls + OFF_USTRUCT_SUPER))
    for (UObjectMin *f = *(UObjectMin **)((uint8_t *)cls + 0x38); f && IsReadable(f, 0x58); f = *(UObjectMin **)((uint8_t *)f + 0x28)) {
      const char *fn = NameText(f->Name);
      if (fn && !strcmp(fn, name) && strcmp(ClassNameOf(f), "Function"))
        return (int)*(uint32_t *)((uint8_t *)f + 0x50);
    }
  return -1;
}

// ---------------------------------------------------------------------------
// Havoc wave director (on by default on Havoc hosts; dn_host_no_havoc_director.txt
// / DN_HOST_NO_HAVOC_DIRECTOR=1 turns it off).
//
// Without this, Havoc loads and fighter swarms fight, but no wave ever starts
// (no big ships, no wave timer). The server half of the wave
// loop is compiled out of this build: AYGameMode_Horde StartFirstWave /
// EndCurrentWave / CheckWaveOver and AYGameMode_Havoc OnWaveStartDelayTimerOver
// / OnWaveCompleteTimerOver all share the empty exec thunk 0x74C640 (UFunction
// Func +0xB0, runtime reflection dump), and OnSwitchToState's body
// is the bare `ret` at 0x1AA9450. What survives is the game-state side:
//   AYGameState_Havoc::EnterNewState(gs, state) 0x36BC00 -- fires the BP
//     OnEnter*State events of GameState_Havoc_BP and ClientHavocStateChanged
//     (0x5E5440); 0 UpgradeSelection, 1 WaveStarting, 2 WaveInProgress,
//     3 WaveComplete, 4 GameOver.
//   AYGameState_Horde OnRep_WaveStateChanged body 0x39E010(gs) -- reads
//     m_currentWaveState and fires the level script OnHordeWaveStarting /
//     Started / Ended events, then BP HordeWaveStateChanged.
// Property offsets (UProperty Offset_Internal +0x50, reflection dump, the
// Horde ones match the disassembly of 0x39E010):
#define OFF_HVC_GM_WAVE_COMPLETE_DUR 0xA70  // float m_waveCompleteDuration
#define OFF_HVC_GM_UPGRADE_DUR 0xA74        // int   m_upgradeSelectionDuration
#define OFF_HVC_GM_WAVE_START_DELAY 0xA78   // int   m_waveStartDelayDuration
#define OFF_HVC_GM_PVE_EVENT_MANAGER 0xA10  // m_pveEventManager
#define OFF_HVC_GS_IS_MATCH_STARTED 0x569   // AYGameState::m_isMatchStarted
#define OFF_HVC_GS_NUM_ALIVE 0x1E58         // AYGameState_PVE::m_numAliveEnemies
#define OFF_HVC_GS_KILLED 0x1F40            // m_numEnemiesKilledThisWave
#define OFF_HVC_GS_MAX_WAVE_ENEMIES 0x1F44  // m_maxWaveEnemies
#define OFF_HVC_GS_MAX_WAVES 0x1F48         // m_maxNumberOfWaves
#define OFF_HVC_GS_WAVE_INDEX 0x1F4C        // m_currentWaveIndex
#define OFF_HVC_GS_WAVE_STATE 0x1F50        // m_currentWaveState (EYHordeWaveState)
#define OFF_HVC_GS_WAVE_DURATION 0x1F58     // m_currentWaveDuration
#define OFF_HVC_GS_HAVOC_STATE 0x2018       // m_currentHavocState
#define OFF_HVC_GS_WAVES_REACHED 0x2024     // m_wavesReached
#define OFF_HVC_GS_EVENT_STARTED 0x2120     // GameState_Havoc_BP EventStarted
#define OFF_HVC_GS_HORDE_MANAGER 0x2128     // GameState_Havoc_BP HordeManager
#define OFF_HVC_GM_ON_PVE_MODE_STARTED 0xA00 // AYGameMode_PVE::OnPVEModeStarted (multicast)
// AYGameMode_PVE::OnAnyNPCKilled (multicast, +0x9E0). GameState_Havoc_BP binds
// OnAnyNPCKilled_Event(KilledYPawn obj @0, KilledNPCID int @8; parms 12 B) to
// it; when EnableEnemiesDropXP has opened the gate, the event spawns
// AlliedBuffPod_HavocXP_Pickup_Standard_BP (boss: _Boss_B) at the pawn's
// transform, and picking that up calls GameMode_Havoc.AddXPForGroup(HavocXP)
// -- the per-kill XP. Nothing on the host broadcast it (the server-side
// death path went with the server build), so kills gave no XP and +0x1F40
// stayed 0.
#define OFF_HVC_GM_ON_ANY_NPC_KILLED 0x9E0
#define OFF_HVC_GS_PVE_EVENT_MANAGER 0x1E68 // AYGameState_PVE::m_pveEventManager
#define OFF_HVC_GS_ON_WAVE_STATE_CHANGED 0x1F20 // AYGameState_Horde::OnHordeWaveStateChanged
#define OFF_HVC_HM_GAME_MODE 0x4B8          // PVE_HordeManager_BP GameMode
// YPVEEventManager::m_combatSceneManager (reflection: ObjectProperty +0x3C0).
// PVE_HordeManager_BP.SpawnWaveInitialEnemies hands it to
// PVE_EnemyGroupSpawn_Component.SpawnPVEEnemyGroupEvent, whose only spawn is
// CombatSceneManager.AddNPCSpawnByDataset (bytecode via tools/bpdump +
// bpcompact.py) -- a null context would skip the call silently.
// Not the no-spawn cause: the field is already set to the world's
// YAICombatSceneManager before the director runs; the fill below is a guard.
#define OFF_PVE_EM_COMBAT_SCENE_MANAGER 0x3C0
#define OFF_HVC_HM_WAVES 0x4A8              // PVE_HordeManager_BP Waves (TArray)
#define RVA_MULTICAST_BROADCAST_NOPARMS 0x2322A0 // (delegate*, parms*): EnterNewState's own broadcasts
#define RVA_HVC_ENTER_NEW_STATE 0x36BC00
#define RVA_HORDE_WAVE_STATE_CHANGED 0x39E010

// Calls a parameterless UFunction (BP event) by name through ProcessEvent.
static bool CallNoParms(void *obj, const wchar_t *name) {
  if (!IsReadable(obj, sizeof(UObjectMin)))
    return false;
  FNameMin n = {};
  ((tFNameCtor)(g_base + RVA_FNAME_CTOR_WIDE))(&n, name, 1 /* FNAME_Add */);
  void *fn = ((tFindFunctionChecked)(g_base + RVA_FIND_FUNCTION_CHECKED))(obj, *(uint64_t *)&n);
  if (!fn)
    return false;
  alignas(16) uint8_t parms[256] = {};
  void **vt = *(void ***)obj;
  ((tProcessEventVirt)vt[VT_PROCESS_EVENT / 8])(obj, fn, parms);
  return true;
}

// Calls a UFunction whose parameters are a wave state (byte/enum) and a wave
// index (int), filled by type from the function's own parameter list.
static bool CallWaveEvent(void *obj, const wchar_t *name, int state, int wave) {
  if (!IsReadable(obj, sizeof(UObjectMin)))
    return false;
  FNameMin n = {};
  ((tFNameCtor)(g_base + RVA_FNAME_CTOR_WIDE))(&n, name, 1 /* FNAME_Add */);
  void *fn = ((tFindFunctionChecked)(g_base + RVA_FIND_FUNCTION_CHECKED))(obj, *(uint64_t *)&n);
  if (!fn || !IsReadable((uint8_t *)fn + 0x92, 2))
    return false;
  alignas(16) uint8_t parms[256] = {};
  char desc[160] = "";
  int o = 0;
  UObjectMin *pp = *(UObjectMin **)((uint8_t *)fn + 0x38);
  for (int j = 0; pp && j < 8 && IsReadable(pp, 0x58); ++j) {
    uint32_t off = *(uint32_t *)((uint8_t *)pp + 0x50);
    const char *pc = ClassNameOf(pp);
    if (off < sizeof parms - 4) {
      if (!strcmp(pc, "ByteProperty") || !strcmp(pc, "EnumProperty"))
        parms[off] = (uint8_t)state;
      else if (!strcmp(pc, "IntProperty"))
        *(int32_t *)(parms + off) = wave;
    }
    const char *pn = NameText(pp->Name);
    o += snprintf(desc + o, sizeof desc - o > 0 ? sizeof desc - o : 0, " %s:%s@%x", pn ? pn : "?", pc, off);
    pp = *(UObjectMin **)((uint8_t *)pp + 0x28);
  }
  void **vt = *(void ***)obj;
  ((tProcessEventVirt)vt[VT_PROCESS_EVENT / 8])(obj, fn, parms);
  static int s_logged = 0;
  if (s_logged++ < 8)
    Logf("havoc: called %ls(%d, %d) on %s; parms%s", name, state, wave, ClassNameOf(obj), desc);
  return true;
}

static int DelegateBound(const uint8_t *p) { return IsReadable(p + 8, 4) ? *(const int32_t *)(p + 8) : -1; }

// The compiled-out natives the BPs still call. Their exec thunk 0x74C640
// (P_FINISH, then the bare `ret` 0x1AA9450) is shared by every parameterless
// stub; CalledNativeName names the UFunction being run. Each call is
// logged once per name and recorded for the director.
#define RVA_EMPTY_EXEC_THUNK 0x74C640
static volatile LONG g_hvcBpEnd = 0; // 1 PlayersWin, 2 PlayersLose: the BP asked the run to end
static tExecThunk g_origEmptyExecThunk = nullptr;
static volatile LONG g_hvcEndWaveCalls = 0, g_hvcPlayersWinCalls = 0, g_hvcPlayersLoseCalls = 0;

// Which native is running. A script call hands the native the CALLER's
// FFrame, so FFrame::Node (+0x10) is the calling function, not the stub. The
// callee is the EX_FinalFunction / EX_CallMath
// operand, the UFunction* just before the parameters: Code (+0x20) - 8 on
// entry. Falls back to Node when that is not a readable *Function object
// (an EX_VirtualFunction carries an FName there instead).
static const char *CalledNativeName(uint8_t *stack) {
  uint8_t *code = IsReadable(stack + 0x20, 8) ? *(uint8_t **)(stack + 0x20) : nullptr;
  if (code && IsReadable(code - 8, 8)) {
    UObjectMin *fn = *(UObjectMin **)(code - 8);
    if (IsReadable(fn, sizeof(UObjectMin)) && IsReadable(fn->Class, sizeof(UObjectMin))) {
      const char *cn = NameText(fn->Class->Name);
      if (cn && strstr(cn, "Function"))
        return NameText(fn->Name);
    }
  }
  UObjectMin *node = IsReadable(stack + 0x10, 8) ? *(UObjectMin **)(stack + 0x10) : nullptr;
  return IsReadable(node, sizeof(UObjectMin)) ? NameText(node->Name) : nullptr;
}

static void __fastcall HookEmptyExecThunk(void *ctx, uint8_t *stack, void *result) {
  const char *n = CalledNativeName(stack);
  g_origEmptyExecThunk(ctx, stack, result);
  if (!n)
    return;
  if (!strcmp(n, "EndCurrentWave"))
    InterlockedIncrement(&g_hvcEndWaveCalls);
  else if (!strcmp(n, "PlayersWin") || !strcmp(n, "PlayersLose")) {
    // The run's end. Compiled out like the wave loop, so nothing ends the
    // match yet: logged on every call to learn when the BP decides it.
    LONG c = InterlockedIncrement(!strcmp(n, "PlayersWin") ? &g_hvcPlayersWinCalls : &g_hvcPlayersLoseCalls);
    InterlockedCompareExchange(&g_hvcBpEnd, !strcmp(n, "PlayersWin") ? 1 : 2, 0);
    uint8_t *gs = IsReadable((uint8_t *)ctx + OFF_GM_GAMESTATE, 8) ? *(uint8_t **)((uint8_t *)ctx + OFF_GM_GAMESTATE) : nullptr;
    bool havocGs = gs && IsReadable(gs + OFF_HVC_GS_WAVES_REACHED, 4) && ClassChainContains((UObjectMin *)gs, "YGameState_Havoc");
    Logf("havoc: BP called %s on %s (call %ld; wave %d of %d, waves reached %d) -- the director ends the run", n,
         ClassNameOf(ctx), c, havocGs ? *(int32_t *)(gs + OFF_HVC_GS_WAVE_INDEX) : -1,
         havocGs ? *(int32_t *)(gs + OFF_HVC_GS_MAX_WAVES) : -1, havocGs ? *(int32_t *)(gs + OFF_HVC_GS_WAVES_REACHED) : -1);
  }
  static char s_seen[16][48];
  static int s_nseen = 0;
  for (int i = 0; i < s_nseen; ++i)
    if (!strcmp(s_seen[i], n))
      return;
  if (s_nseen < 16)
    snprintf(s_seen[s_nseen++], sizeof s_seen[0], "%s", n);
  Logf("havoc: BP called compiled-out native %s on %s (first call)", n, ClassNameOf(ctx));
}

// AddNPCToEnemyList(int npcId), which PVE_EnemyGroupSpawn_Component calls on
// YGameMode_PVE right after AddNPCSpawnByDataset for every enemy that
// CountForWave, is compiled out too: its exec thunk 0x742E90 reads the int and
// calls the bare `ret` 0x1AA9450. That thunk is shared by 34 one-int natives
// (LEA r8 refs in the StaticRegisterNatives blocks), so the hook does the
// thunk's own work -- FFrame::Step (0xD5D250: stack, stack->Object +0x18,
// &result) when Code (+0x20) is set, else StepExplicitProperty (0xD5D280) on
// PropertyChainForCompiledIn (+0x80, advanced by Next +0x28); P_FINISH
// (Code += Code != 0) -- and then records the id when the called native
// (CalledNativeName) is AddNPCToEnemyList. Nothing else in the exe writes AYGameState_PVE
// m_numAliveEnemies (+0x1E58; the two writers are constructors), so the
// director derives it from these ids. GUESS: the server build's list
// semantics (removal on death, what else it fed) are unknown.
#define RVA_INT_PARM_EXEC_THUNK 0x742E90
#define RVA_FFRAME_STEP 0xD5D250
#define RVA_FFRAME_STEP_EXPLICIT_PROPERTY 0xD5D280
static tExecThunk g_origIntParmExecThunk = nullptr;
static volatile LONG g_hvcEnemyIds[256];
static volatile LONG g_hvcEnemyCount = 0;

// Havoc XP. AYGameMode_Havoc AddXPForGroup(int) (int thunk above) and
// AddXPWithContributerPlayer/Pawn(int, object) (thunk 0x740AF0, below) are
// compiled out like the wave loop, and nothing in the exe writes AYGameState
// _Havoc m_playerWaveXP (+0x201C) or m_totalXP (+0x2020) -- the only writer is
// the constructor 0x363680. The getters are plain loads: GetTotalXP 0x372730
// = +0x2020, GetPlayerWaveXP 0x371AC0 = +0x201C, GetWaveXP 0x372F60 = +0x2020
// - +0x2038 (+0x2038 has no writer at all; reads 0). Both fields are
// replicated (CPF_Net). Without this, GameMode_Havoc_BP's AddXPForGroup(750)
// at a wave clear left the client at 0 wave XP and 0 upgrade currency. GUESS: the original split between the two fields is
// unknown; every grant goes to both, and m_playerWaveXP restarts each wave.
#define OFF_HVC_GS_PLAYER_WAVE_XP 0x201C
#define OFF_HVC_GS_TOTAL_XP 0x2020
// Per-player spendable XP. The upgrade panel's XP counter and the purchase
// gate both read AYPlayerReplicationInfo_Havoc m_currentXP: 0x3FC7D0 (PC
// +0x3E0 -> PRI cast -> +0xA28), called by the activate path 0x448240 which
// compares it with the upgrade's cost before SendSelectedUpgrade. Field
// offsets from the property registration 0x958150..0x958963: m_currentXP
// +0xA28, m_currentContributedXP +0xA2C, m_lastWaveContributedXP +0xA30.
// DeductXP 0x3F34F0 (pri, xp) is real code (`sub [pri+0xA28], xp`); AddXP is
// compiled out (only its log format string "AddXP | Player %s | XP %i |
// Contributer %i" survives, no LEA to it), so grants write the field here.
// GUESS: Havoc is co-op and the GS XP is team XP, so every connected human
// gets the full grant; the contributor (if any) also gets it in
// m_currentContributedXP. The original split is unknown.
#define OFF_HVC_PRI_CURRENT_XP 0xA28
#define OFF_HVC_PRI_CONTRIBUTED_XP 0xA2C
#define RVA_HVC_PRI_DEDUCT_XP 0x3F34F0
static int HavocHumanPCs(uint8_t *gs, uint8_t **out, int max) {
  if (!IsReadable(gs + OFF_GS_PLAYER_ARRAY, 16))
    return 0;
  uint8_t **arr = *(uint8_t ***)(gs + OFF_GS_PLAYER_ARRAY);
  int count = *(int32_t *)(gs + OFF_GS_PLAYER_ARRAY + 8);
  if (count < 0 || count > 256 || (count && !IsReadable(arr, (size_t)count * 8)))
    return 0;
  int n = 0;
  for (int i = 0; i < count && n < max; ++i) {
    uint8_t *pri = arr[i];
    if (!IsReadable(pri, OFF_HVC_PRI_CONTRIBUTED_XP + 4))
      continue;
    uint8_t *pc = *(uint8_t **)(pri + OFF_ACTOR_OWNER);
    if (!IsReadable(pc, OFF_PC_NETCONNECTION + 8) || !ClassChainContains((UObjectMin *)pc, "PlayerController") ||
        !*(void **)(pc + OFF_PC_NETCONNECTION) || *(uint8_t **)(pc + OFF_PC_PLAYER_STATE) != pri ||
        !ClassChainContains((UObjectMin *)pri, "YPlayerReplicationInfo_Havoc"))
      continue;
    out[n++] = pc;
  }
  return n;
}

// The PRI behind a contributor object (a pawn, a controller or a PRI).
static uint8_t *HavocContributorPri(void *obj) {
  uint8_t *o = (uint8_t *)obj;
  if (!IsReadable(o, sizeof(UObjectMin)))
    return nullptr;
  if (ClassChainContains((UObjectMin *)o, "Pawn"))
    o = IsReadable(o + OFF_PAWN_CONTROLLER, 8) ? *(uint8_t **)(o + OFF_PAWN_CONTROLLER) : nullptr;
  if (o && ClassChainContains((UObjectMin *)o, "Controller"))
    o = IsReadable(o + OFF_PC_PLAYER_STATE, 8) ? *(uint8_t **)(o + OFF_PC_PLAYER_STATE) : nullptr;
  return o && IsReadable(o, OFF_HVC_PRI_CONTRIBUTED_XP + 4) &&
                 ClassChainContains((UObjectMin *)o, "YPlayerReplicationInfo_Havoc") ? o : nullptr;
}

static void HavocAddXP(uint8_t *gm, int xp, const char *why, void *contributor = nullptr) {
  if (!IsReadable(gm + OFF_GM_GAMESTATE, 8))
    return;
  uint8_t *gs = *(uint8_t **)(gm + OFF_GM_GAMESTATE);
  if (!IsReadable(gs + OFF_HVC_GS_TOTAL_XP, 4) || !ClassChainContains((UObjectMin *)gs, "YGameState_Havoc"))
    return;
  if (xp <= 0 || xp > 1000000)
    return;
  *(int32_t *)(gs + OFF_HVC_GS_PLAYER_WAVE_XP) += xp;
  *(int32_t *)(gs + OFF_HVC_GS_TOTAL_XP) += xp;
  uint8_t *pcs[16];
  int npc = HavocHumanPCs(gs, pcs, 16);
  for (int i = 0; i < npc; ++i)
    *(int32_t *)(*(uint8_t **)(pcs[i] + OFF_PC_PLAYER_STATE) + OFF_HVC_PRI_CURRENT_XP) += xp;
  if (uint8_t *cpri = HavocContributorPri(contributor))
    *(int32_t *)(cpri + OFF_HVC_PRI_CONTRIBUTED_XP) += xp;
  static int s_logged = 0;
  if (s_logged++ < 40)
    Logf("havoc: %s(%d): wave xp %d, total xp %d, %d player(s) credited%s", why, xp,
         *(int32_t *)(gs + OFF_HVC_GS_PLAYER_WAVE_XP), *(int32_t *)(gs + OFF_HVC_GS_TOTAL_XP), npc,
         contributor ? " (+contributor)" : "");
}

// FFrame::Step for one parameter, as the generated exec thunks do it.
static void StepParm(uint8_t *stack, void *out) {
  if (*(void **)(stack + 0x20))
    ((void(__fastcall *)(void *, void *, void *))(g_base + RVA_FFRAME_STEP))(stack, *(void **)(stack + 0x18), out);
  else {
    uint8_t *prop = *(uint8_t **)(stack + 0x80);
    *(void **)(stack + 0x80) = *(void **)(prop + 0x28);
    ((void(__fastcall *)(void *, void *, void *))(g_base + RVA_FFRAME_STEP_EXPLICIT_PROPERTY))(stack, out, prop);
  }
}

// Exec thunk 0x740AF0: (int, object) -> bare `ret` 0x1AA9450; read both
// parameters the way it does (StepParm x2, P_FINISH), then act by callee.
#define RVA_INT_OBJ_PARM_EXEC_THUNK 0x740AF0
static tExecThunk g_origIntObjParmExecThunk = nullptr;
static void __fastcall HookIntObjParmExecThunk(void *ctx, uint8_t *stack, void *result) {
  const char *n = CalledNativeName(stack);
  int32_t v = 0;
  void *obj = nullptr;
  StepParm(stack, &v);
  StepParm(stack, &obj);
  if (*(uint8_t **)(stack + 0x20))
    ++*(uint8_t **)(stack + 0x20);
  if (!n)
    return;
  if (!strcmp(n, "AddXPWithContributerPlayer") || !strcmp(n, "AddXPWithContributerPawn")) {
    HavocAddXP((uint8_t *)ctx, v, n, obj);
    return;
  }
  static char s_seen[16][48];
  static int s_nseen = 0;
  for (int i = 0; i < s_nseen; ++i)
    if (!strcmp(s_seen[i], n))
      return;
  if (s_nseen < 16)
    snprintf(s_seen[s_nseen++], sizeof s_seen[0], "%s", n);
  Logf("havoc: BP called compiled-out native %s(%d, %s) on %s (first call)", n, v,
       obj ? ClassNameOf((UObjectMin *)obj) : "null", ClassNameOf(ctx));
}

// Ready-up. AYPlayerController_Havoc::ServerReadyUpPlayerForNextWave's exec
// thunk 0x772650 (no parameters; _Validate at vtable +0x1068, body at
// +0x1070) is unique to the RPC. The _Implementation is compiled out: PC
// vtable 0x2EF6678 +0x1070 (and ServerUnReadyPlayerForNextWave's, +0x1060) is
// the stub 0x15FBF00 `mov al, 1; ret`. The hook records the controller; the
// director ends upgrade selection early once every connected human has
// readied. GUESS: the original rule (all players? a vote?) is unknown.
//
// The player's own ready state is AYPlayerReplicationInfo_Havoc
// m_readyForNextWave (IsReadyForNextWave 0x401E50 = `return pri[0xA41]`),
// replicated with OnRep_ReadyForNextWaveChanged -> AYHUD::UpdateHavocUpgrade
// Panel (0x562E50), which greys the upgrades out (state 3) once the player is
// ready. Nothing wrote it, so the panel never showed a ready player. The game
// state's ClientUpdatePlayerReadyForNextWave(TArray<int32>) is still not sent:
// its client half only stores the list (+0x2028) and fires a BP event that no
// blueprint implements.
#define RVA_HVC_READY_UP_THUNK 0x772650
#define RVA_HVC_UNREADY_THUNK 0x774B40 // ServerUnReadyPlayerForNextWave exec thunk (78 B, unique)
#define OFF_HVC_PRI_READY 0xA41
static tExecThunk g_origHavocReadyUpThunk = nullptr;
static tExecThunk g_origHavocUnReadyThunk = nullptr;
static void *volatile g_hvcReady[16];
static volatile LONG g_hvcReadyCount = 0;

// Writes m_readyForNextWave on pc's Havoc PRI (offset by reflection, 0xA41
// from the getter as the fallback).
static void HavocSetReadyFlag(void *pc, bool ready) {
  uint8_t *p = (uint8_t *)pc;
  uint8_t *pri = IsReadable(p + OFF_PC_PLAYER_STATE, 8) ? *(uint8_t **)(p + OFF_PC_PLAYER_STATE) : nullptr;
  if (!pri || !IsReadable(pri, OFF_HVC_PRI_READY + 1) ||
      !ClassChainContains((UObjectMin *)pri, "YPlayerReplicationInfo_Havoc"))
    return;
  static int s_off = 0;
  if (!s_off) {
    int off = PropOffset((UObjectMin *)pri, "m_readyForNextWave");
    s_off = off > 0 ? off : OFF_HVC_PRI_READY;
    Logf("havoc: PRI m_readyForNextWave at +0x%X (reflection %d, getter 0x%X)", s_off, off, OFF_HVC_PRI_READY);
  }
  pri[s_off] = ready ? 1 : 0;
}

static void __fastcall HookHavocReadyUpThunk(void *ctx, uint8_t *stack, void *result) {
  g_origHavocReadyUpThunk(ctx, stack, result);
  HavocSetReadyFlag(ctx, true);
  LONG n = g_hvcReadyCount;
  for (LONG i = 0; i < n && i < 16; ++i)
    if (g_hvcReady[i] == ctx)
      return;
  if (n < 16) {
    g_hvcReady[n] = ctx;
    InterlockedIncrement(&g_hvcReadyCount);
  }
  Logf("havoc: ServerReadyUpPlayerForNextWave from %p %s (%ld ready)", ctx, ClassNameOf(ctx), g_hvcReadyCount);
}

static void __fastcall HookHavocUnReadyThunk(void *ctx, uint8_t *stack, void *result) {
  g_origHavocUnReadyThunk(ctx, stack, result);
  HavocSetReadyFlag(ctx, false);
  LONG n = g_hvcReadyCount;
  for (LONG i = 0; i < n && i < 16; ++i)
    if (g_hvcReady[i] == ctx) {
      g_hvcReady[i] = g_hvcReady[n - 1];
      InterlockedDecrement(&g_hvcReadyCount);
      break;
    }
  Logf("havoc: ServerUnReadyPlayerForNextWave from %p %s (%ld ready)", ctx, ClassNameOf(ctx), g_hvcReadyCount);
}

// Match ready-up (pre-match lineup toggle) is not handled, on purpose. In
// Havoc each client's card shows its own tick and the other players' X, and
// the lineup ends on its timer. AYPlayerController::ServerPlayerReadyUpForMatch
// (exec thunk 0x772380, body 0x592210) stores the bool at PC+0xC44 and calls gm
// vt+0x960(PC) only if gs vt+0x7D8 (0x39E3D0, `movzx eax, byte [gs+0x1C3C]`)
// is true. gs+0x1C3C is the reflected m_useWaveSpawning, copied from the game
// mode's m_useWaveSpawning (gm+0x899) by 0x379D60; both are 0 in
// GameMode_Havoc_BP. Stock game-mode data, not missing server state; forcing
// the flag would also switch the spawn rules it is named for.

// Connected humans (player controllers with a net connection), as PlayersTick
// counts them; -1 when the player array is unreadable.
static int HavocHumanCount(uint8_t *gs, int *readyOut) {
  *readyOut = 0;
  if (!IsReadable(gs + OFF_GS_PLAYER_ARRAY, 16))
    return -1;
  uint8_t **arr = *(uint8_t ***)(gs + OFF_GS_PLAYER_ARRAY);
  int count = *(int32_t *)(gs + OFF_GS_PLAYER_ARRAY + 8);
  if (count < 0 || count > 256 || (count && !IsReadable(arr, (size_t)count * 8)))
    return -1;
  int humans = 0;
  for (int i = 0; i < count; ++i) {
    uint8_t *pri = arr[i];
    if (!IsReadable(pri, OFF_ACTOR_OWNER + 8))
      continue;
    uint8_t *pc = *(uint8_t **)(pri + OFF_ACTOR_OWNER);
    if (!IsReadable(pc, OFF_PC_NETCONNECTION + 8) || !ClassChainContains((UObjectMin *)pc, "PlayerController") ||
        !*(void **)(pc + OFF_PC_NETCONNECTION))
      continue;
    ++humans;
    for (LONG j = 0; j < g_hvcReadyCount && j < 16; ++j)
      if (g_hvcReady[j] == pc) {
        ++*readyOut;
        break;
      }
  }
  return humans;
}

// ---------------------------------------------------------------------------
// Havoc upgrade offers. Server side of the between-wave upgrade panel, all of
// it compiled out of this build:
//  - offer: AYPlayerController_Havoc::ClientReceiveRandSelectedUpgrades
//    (TArray<FName>) -- its RPC wrapper 0x7463E0 has no callers. The client
//    side is intact: _Implementation 0x3F25F0 hands the names to
//    UYHavocProgressionComponent::StoreSelectedUpgradesReceivedFromServer
//    0x3AAAE0 (PC +0x1148, slots at comp +0x30) and the HUD's
//    PrepareUpgradesForUI 0x37B2D0 loads them for UI_HUD_HavocProgressionMenu.
//  - pick: the panel's ActivateSelectedUpgrade (0x448240) checks m_currentXP
//    >= the upgrade's cost, then SendSelectedUpgrade 0x405900 turns the FName
//    back into its slot (0x39B800) and calls ServerReceiveSelectedUpgradeIndex
//    (int), whose _Implementation 0x406040 only logs "Selected slot index is
//    %d" -- hooked below.
//  - apply: UYHavocProgressionComponent::AddNewBoost and
//    OnApplyBoostAndModifiersData (0x3A25C0) only log; the boost's m_feats go
//    onto the ship here with the game's own UYFeatsComponent::AddFeat (as the
//    Turbo TDM modifiers do), again on every respawned ship.
// Boost rows: data/datatables/Progression/Havoc/DN_HavocBoosts_DT.json (row
// name, m_feats, m_cost, m_weight, m_category), verbatim. Five rows' m_feats
// differ from their row name (typos in the table, kept). Feat definitions:
// DN_Feats_Havoc_Boosts_Shared_OTS_DT.
// GUESS: the selection rule is unknown. One boost per category (offensive /
// defensive / utility), weighted by m_weight (weight-0 rows never offered);
// each slot can be bought once per selection phase. Loadout (ship tier)
// upgrades from DN_HavocLoadouts_DT are NOT offered yet.
// NOT verified against a live client: the panel showing the offer, the
// purchase result, the feats replicating to the client's ship.
struct HavocBoost {
  const wchar_t *id, *feat;
  int cost, weight;
  char cat; // 'O' offensive, 'D' defensive, 'U' utility
};
static const HavocBoost kHavocBoosts[] = {
    {L"HVC_Boost_Def_HpInc_Large", L"HVC_Boost_Def_HpInc_Large", 1000, 2, 'D'},
    {L"HVC_Boost_Def_HpInc_Small", L"HVC_Boost_Def_HpInc_Small", 300, 2, 'D'},
    {L"HVC_Boost_Def_HpInc_Medium", L"HVC_Boost_Def_HpInc_Medium", 600, 5, 'D'},
    {L"HCV_Boost_Def_RegtimeDec_Medium", L"HVC_Boost_Def_HPRegtimeDec_Medium", 600, 5, 'D'},
    {L"HVC_Boost_Def_HPRegInc_Small", L"HVC_Boost_Def_HPRegInc_Small", 350, 2, 'D'},
    {L"HVC_Boost_Def_HPRegInc_Medium", L"HVC_Boost_Def_HPRegInc_Medium", 600, 5, 'D'},
    {L"HVC_Boost_Def_HPRegInc_Large", L"HVC_Boost_Def_HPRegInc_Large", 1000, 2, 'D'},
    {L"HVC_Boost_Def_ArInc_Small", L"HVC_Boost_Def_ArInc_Small", 350, 2, 'D'},
    {L"HVC_Boost_Def_ArInc_Medium", L"HVC_Boost_Def_ArInc_Medium", 600, 5, 'D'},
    {L"HVC_Boost_Def_ArInc_Large", L"HVC_Boost_Def_ArInc_Large", 1000, 2, 'D'},
    {L"HVC_Boost_DefArIncFr_Medium", L"HVC_Boost_Def_ArIncFr_Medium", 600, 0, 'D'},
    {L"HVC_Boost_Def_ArIncSid_Medium", L"HVC_Boost_Def_ArIncSid_Medium", 600, 0, 'D'},
    {L"HVC_Boost_Def_ArincBk_Medium", L"HVC_Boost_Def_ArIncBk_Medium", 600, 0, 'D'},
    {L"HVC_Boost_Def_ShieldEnDec_Medium", L"HVC_Boost_Def_ShieldEnDec_Medium", 600, 5, 'D'},
    {L"HVC_Boost_Off_DmgInc_Small", L"HVC_Boost_Off_DmgInc_Small", 350, 2, 'O'},
    {L"HVC_Boost_Off_DmgInc_Medium", L"HVC_Boost_Off_DmgInc_Medium", 600, 5, 'O'},
    {L"HVC_Boost_Off_DmgInc_Large", L"HVC_Boost_Off_DmgInc_Large", 1000, 2, 'O'},
    {L"HVC_Boost_Off_AbiInc_Large", L"HVC_Boost_Off_AbiInc_Large", 1000, 2, 'O'},
    {L"HVC_Boost_Off_AbiInc_Small", L"HVC_Boost_Off_AbiInc_Small", 350, 2, 'O'},
    {L"HVC_Boost_Off_AbiInc_Medium", L"HVC_Boost_Off_AbiInc_Medium", 600, 5, 'O'},
    {L"HVC_Boost_Off_RldTDec_Medium", L"HVC_Boost_Off_RldTDec_Medium", 600, 5, 'O'},
    {L"HVC_Boost_Off_WpnEnDec", L"HVC_Boost_Off_WpnEnDec", 600, 5, 'O'},
    {L"HVC_Boost_Off_WpnEnRelDec_Medium", L"HVC_Boost_Off_WpnEnRelDec_Medium", 600, 2, 'D'},
    {L"HVC_Boost_Def_ThrustEnArmInc_Medium", L"HVC_Boost_Def_ThrustEnArmInc_Medium", 600, 2, 'D'},
    {L"HVC_Boost_Util_EnInc_Large", L"HVC_Boost_Util_EnInc_Large", 1000, 2, 'U'},
    {L"HVC_Boost_Util_EnInc_Medium", L"HVC_Boost_Util_EnInc_Medium", 600, 5, 'U'},
    {L"HVC_Boost_Util_EnInc_Small", L"HVC_Boost_Util_EnInc_Small", 350, 2, 'U'},
    {L"HVC_Boost_Util_SpdInc_Small", L"HVC_Boost_Util_SpdInc_Small", 350, 2, 'U'},
    {L"HVC_Boost_Util_SpdInc_Medium", L"HVC_Boost_Util_SpdInc_Medium", 600, 5, 'U'},
    {L"HVC_Boost_Util_SpdInc_Large", L"HVC_Boost_Util_SpdInc_Large", 1000, 2, 'U'},
    {L"HVC_Boost_Util_SpdIncFwd_Large", L"HVC_Boost_Util_SpdIncFwd_Large", 1000, 2, 'U'},
    {L"HVC_Boost_Util_SpdIncStr_Large", L"HVC_Boost_Util_SpdIncStr_Large", 1000, 2, 'U'},
    {L"HVC_Boost_Util_SpdIncVert_Large", L"HVC_Boost_Util_SpdIncVert_Large", 1000, 2, 'U'},
    {L"HVC_Boost_Util_SpdIncBk_Large", L"HVC_Boost_Util_SpdIncBk_Large", 1000, 2, 'U'},
    {L"HVC_Boost_Util_EnRegInc_Medium", L"HVC_Boost_Util_EnReginc_Medium", 600, 5, 'U'},
    {L"HCV_Boost_Util_EnThrustSpdInc_Medium", L"HVC_Boost_Util_EnThrustSpdInc_Medium", 600, 2, 'D'},
    {L"HVC_Boost_Util_AbiTDec_Medium", L"HVC_Boost_Util_AbiTDec_Medium", 600, 5, 'U'},
    {L"HVC_Boost_Util_ThrustEnDec", L"HVC_Boost_Util_ThrustEnDec", 600, 5, 'U'},
};
static const int kHavocBoostCount = (int)(sizeof kHavocBoosts / sizeof kHavocBoosts[0]);

struct HavocPlayer {
  void *pc;
  int offer[3];     // kHavocBoosts index, -1 = none / already bought
  int offerShip[3]; // kHavocShips index on a ship-upgrade wave, else -1
  FNameMin offerNames[3];
  int owned[128], nOwned;
  void *appliedPawn;
  int nApplied;
};
static HavocPlayer g_hvcPlayers[16];

static HavocPlayer *HavocPlayerFor(void *pc, bool create) {
  for (HavocPlayer &p : g_hvcPlayers)
    if (p.pc == pc)
      return &p;
  if (!create)
    return nullptr;
  for (HavocPlayer &p : g_hvcPlayers)
    if (!p.pc) {
      memset(&p, 0, sizeof p);
      p.pc = pc;
      p.offer[0] = p.offer[1] = p.offer[2] = -1;
      p.offerShip[0] = p.offerShip[1] = p.offerShip[2] = -1;
      return &p;
    }
  return nullptr;
}

// Calls a UFunction by name, letting `fill` write each parameter by its
// property class and offset; logs the parameter list the first few times.
typedef void (*tParmFill)(uint8_t *parms, const char *propClass, uint32_t off, void *ctx);
static bool CallFilled(void *obj, const wchar_t *name, tParmFill fill, void *ctx) {
  if (!IsReadable(obj, sizeof(UObjectMin)))
    return false;
  FNameMin n = {};
  ((tFNameCtor)(g_base + RVA_FNAME_CTOR_WIDE))(&n, name, 1 /* FNAME_Add */);
  void *fn = ((tFindFunctionChecked)(g_base + RVA_FIND_FUNCTION_CHECKED))(obj, *(uint64_t *)&n);
  if (!fn || !IsReadable((uint8_t *)fn + 0x40, 8))
    return false;
  alignas(16) uint8_t parms[256] = {};
  char desc[160] = "";
  int o = 0;
  UObjectMin *pp = *(UObjectMin **)((uint8_t *)fn + 0x38);
  for (int j = 0; pp && j < 8 && IsReadable(pp, 0x58); ++j) {
    uint32_t off = *(uint32_t *)((uint8_t *)pp + 0x50);
    const char *pc = ClassNameOf(pp);
    if (fill && off < sizeof parms - 16)
      fill(parms, pc, off, ctx);
    const char *pn = NameText(pp->Name);
    o += snprintf(desc + o, sizeof desc - o > 0 ? sizeof desc - o : 0, " %s:%s@%x", pn ? pn : "?", pc, off);
    pp = *(UObjectMin **)((uint8_t *)pp + 0x28);
  }
  void **vt = *(void ***)obj;
  ((tProcessEventVirt)vt[VT_PROCESS_EVENT / 8])(obj, fn, parms);
  static int s_logged = 0;
  if (s_logged++ < 12)
    Logf("havoc: called %ls on %s; parms%s", name, ClassNameOf(obj), desc[0] ? desc : " (none)");
  return true;
}

static void FillNameArray(uint8_t *parms, const char *pc, uint32_t off, void *ctx) {
  if (!strcmp(pc, "ArrayProperty")) {
    HavocPlayer *p = (HavocPlayer *)ctx;
    *(void **)(parms + off) = p->offerNames;
    *(int32_t *)(parms + off + 8) = 3;
    *(int32_t *)(parms + off + 12) = 3;
  }
}
// ClientReceiveBoostPurchaseResult(FName purchasedBoostID @0, bool isSuccessful
// @8); parameter list read at runtime.
struct HavocPurchaseResult { FNameMin id; uint8_t ok; };
static void FillPurchaseResult(uint8_t *parms, const char *pc, uint32_t off, void *ctx) {
  HavocPurchaseResult *r = (HavocPurchaseResult *)ctx;
  if (!strcmp(pc, "NameProperty"))
    memcpy(parms + off, &r->id, sizeof r->id);
  else if (!strcmp(pc, "BoolProperty"))
    parms[off] = r->ok;
}

static int HavocPickBoost(char cat) {
  static uint32_t s_rng = 0;
  if (!s_rng)
    s_rng = GetTickCount() | 1;
  int total = 0;
  for (const HavocBoost &b : kHavocBoosts)
    if (b.cat == cat)
      total += b.weight;
  if (total <= 0)
    return -1;
  s_rng ^= s_rng << 13, s_rng ^= s_rng >> 17, s_rng ^= s_rng << 5;
  int r = (int)(s_rng % (uint32_t)total);
  for (int i = 0; i < kHavocBoostCount; ++i)
    if (kHavocBoosts[i].cat == cat && (r -= kHavocBoosts[i].weight) < 0)
      return i;
  return -1;
}

// On entering UpgradeSelection: a fresh offer for every connected human.
static uint32_t HavocRand();
// ---------------------------------------------------------------------------
// Havoc ship tier upgrades (stage 3, 2026-10-19; dn_host_no_havoc_ship_upgrades
// .txt turns them off).
//
// Offer. AYGameState_Havoc::PrepareUpgradesForUI (0x37B2D0) asks the data
// manager IsWaveWithLoadoutUpgrades(m_currentWaveIndex) (0x3A0830: the wave is
// one of the loadout rows' m_waveAppearances -- 3, 6, 12); on such a wave it
// reads every offered name as a DN_HavocLoadouts_DT ROW (GetLoadoutByID
// 0x39A1B0) instead of a boost, and shows the ship. The cost the panel checks
// (upgrade data +0x1A8, ActivateSelectedUpgrade 0x448240) is filled from
// m_cost for a boost and left 0 for a ship: a ship upgrade costs no XP.
// GUESS: one ship per category (offensive / defensive / utility) from the rows
// that appear on this wave, the slot of the player's own line holding its next
// tier (m_loadoutItemIDNextTier of the row whose item id is the active
// loadout's m_precastLoadoutID).
//
// Swap. AYPlayerController::SpawnPlayer (0x5998A0) has a Havoc path: with the
// last pawn weak pointer (PC +0xBB0) valid and PC +0xCA0 clear, it spawns the
// ACTIVE loadout (loadout manager *(*(PC+0x958)+0x28), +0x208) at that pawn's
// transform and destroys it ("GameState is Havoc. Respawn location should be
// the last known pawn position before it get's destroyed!"). Its own caller
// (ServerSpawnNearActorPostLoadPawnWork 0x592C40) sets the pending flag PC
// +0xB88 first. So the pick: register the ship's loadout with the player's
// manager (its class from ItemIDRegister, as the precasts are), activate it
// (ActivateLoadout 0x336C90), point +0xBB0 at the current ship, set +0xB88,
// call SpawnPlayer. Boosts and modifiers follow onto the new ship (their
// per-pawn bookkeeping sees a new pawn).
struct HavocShip {
  const wchar_t *row;
  int itemId, nextTierId, wave;
  char cat; // 'O' offensive, 'D' defensive, 'U' utility
  const char *asset;
  const wchar_t *path;
};
// DN_HavocLoadouts_DT, verbatim (generated 2026-10-19), with each m_loadoutItemID's
// asset from ItemIDRegister: row, item id, next-tier item id, m_waveAppearances
// (0 = "none", the starting T2 ships), category, asset name, class path.
static const HavocShip kHavocShips[] = {
    {L"HVCDEFDN_M(T2)", 33489354, 33489355, 0, 'D', "VH_HVC_DEF_DreadM_Loadout_T2_BP", L"/Game/Generic/Loadouts/Precast/Havoc/VH_HVC_DEF_DreadM_Loadout_T2_BP.VH_HVC_DEF_DreadM_Loadout_T2_BP_C"},
    {L"HVCDEFSU_M(T2)", 33489363, 33489364, 0, 'D', "VH_HVC_DEF_SupportM_Loadout_T2_BP", L"/Game/Generic/Loadouts/Precast/Havoc/VH_HVC_DEF_SupportM_Loadout_T2_BP.VH_HVC_DEF_SupportM_Loadout_T2_BP_C"},
    {L"HVCUTIAS_M(T2)", 33489370, 33489367, 0, 'U', "VH_HVC_UTI_AssaultM_Loadout_T2_BP", L"/Game/Generic/Loadouts/Precast/Havoc/VH_HVC_UTI_AssaultM_Loadout_T2_BP.VH_HVC_UTI_AssaultM_Loadout_T2_BP_C"},
    {L"HVCOFFSC_L(T2)", 33489386, 33489387, 0, 'O', "VH_HVC_OFF_ScoutL_Loadout_T2_BP", L"/Game/Generic/Loadouts/Precast/Havoc/VH_HVC_OFF_ScoutL_Loadout_T2_BP.VH_HVC_OFF_ScoutL_Loadout_T2_BP_C"},
    {L"HVCOFFSN_M(T2)", 33489394, 33489395, 0, 'O', "VH_HVC_OFF_SniperM_Loadout_T2_BP", L"/Game/Generic/Loadouts/Precast/Havoc/VH_HVC_OFF_SniperM_Loadout_T2_BP.VH_HVC_OFF_SniperM_Loadout_T2_BP_C"},
    {L"HVCDEFAS_H(T3)", 33489432, 33489349, 3, 'D', "VH_HVC_DEF_AssaultH_Loadout_T3_BP", L"/Game/Generic/Loadouts/Precast/Havoc/VH_HVC_DEF_AssaultH_Loadout_T3_BP.VH_HVC_DEF_AssaultH_Loadout_T3_BP_C"},
    {L"HVCDEFDN_M(T3)", 33489355, 33489356, 3, 'D', "VH_HVC_DEF_DreadM_Loadout_T3_BP", L"/Game/Generic/Loadouts/Precast/Havoc/VH_HVC_DEF_DreadM_Loadout_T3_BP.VH_HVC_DEF_DreadM_Loadout_T3_BP_C"},
    {L"HVCDEFSU_M(T3)", 33489364, 33489365, 3, 'D', "VH_HVC_DEF_SupportM_Loadout_T3_BP", L"/Game/Generic/Loadouts/Precast/Havoc/VH_HVC_DEF_SupportM_Loadout_T3_BP.VH_HVC_DEF_SupportM_Loadout_T3_BP_C"},
    {L"HVCUTIAS_M(T3)", 33489367, 33489368, 3, 'U', "VH_HVC_UTI_AssaultM_Loadout_T3_BP", L"/Game/Generic/Loadouts/Precast/Havoc/VH_HVC_UTI_AssaultM_Loadout_T3_BP.VH_HVC_UTI_AssaultM_Loadout_T3_BP_C"},
    {L"HVCUTISU_L(T3)", 33489374, 33489371, 3, 'U', "VH_HVC_UTI_SupportL_Loadout_T3_BP", L"/Game/Generic/Loadouts/Precast/Havoc/VH_HVC_UTI_SupportL_Loadout_T3_BP.VH_HVC_UTI_SupportL_Loadout_T3_BP_C"},
    {L"HVCUTISN_L(T3)", 33489382, 33489381, 3, 'U', "VH_HVC_UTI_SniperL_Loadout_T3_BP", L"/Game/Generic/Loadouts/Precast/Havoc/VH_HVC_UTI_SniperL_Loadout_T3_BP.VH_HVC_UTI_SniperL_Loadout_T3_BP_C"},
    {L"HVCUTISC_H(T3)", 33489383, 33489384, 3, 'U', "VH_HVC_UTI_ScoutH_Loadout_T3_BP", L"/Game/Generic/Loadouts/Precast/Havoc/VH_HVC_UTI_ScoutH_Loadout_T3_BP.VH_HVC_UTI_ScoutH_Loadout_T3_BP_C"},
    {L"HVCOFFSC_L(T3)", 33489387, 33489388, 3, 'O', "VH_HVC_OFF_ScoutL_Loadout_T3_BP", L"/Game/Generic/Loadouts/Precast/Havoc/VH_HVC_OFF_ScoutL_Loadout_T3_BP.VH_HVC_OFF_ScoutL_Loadout_T3_BP_C"},
    {L"HVCOFFSC_M(T3)", 33489390, 33489391, 3, 'O', "VH_HVC_OFF_ScoutM_Loadout_T3_BP", L"/Game/Generic/Loadouts/Precast/Havoc/VH_HVC_OFF_ScoutM_Loadout_T3_BP.VH_HVC_OFF_ScoutM_Loadout_T3_BP_C"},
    {L"HVCOFFSN_M(T3)", 33489395, 33489396, 3, 'O', "VH_HVC_OFF_SniperM_Loadout_T3_BP", L"/Game/Generic/Loadouts/Precast/Havoc/VH_HVC_OFF_SniperM_Loadout_T3_BP.VH_HVC_OFF_SniperM_Loadout_T3_BP_C"},
    {L"HVCOFFDN_L(T3)", 33489404, 33489403, 3, 'O', "VH_HVC_OFF_DreadL_Loadout_T3_BP", L"/Game/Generic/Loadouts/Precast/Havoc/VH_HVC_OFF_DreadL_Loadout_T3_BP.VH_HVC_OFF_DreadL_Loadout_T3_BP_C"},
    {L"HVCDEFAS_H(T4)", 33489349, 33489350, 6, 'D', "VH_HVC_DEF_AssaultH_Loadout_T4_BP", L"/Game/Generic/Loadouts/Precast/Havoc/VH_HVC_DEF_AssaultH_Loadout_T4_BP.VH_HVC_DEF_AssaultH_Loadout_T4_BP_C"},
    {L"HVCDEFAS_M(T3)", 33489351, 33489352, 3, 'D', "VH_HVC_DEF_AssaultM_Loadout_T3_BP", L"/Game/Generic/Loadouts/Precast/Havoc/VH_HVC_DEF_AssaultM_Loadout_T3_BP.VH_HVC_DEF_AssaultM_Loadout_T3_BP_C"},
    {L"HVCDEFAS_M(T4)", 33489352, 33489353, 6, 'D', "VH_HVC_DEF_AssaultM_Loadout_T4_BP", L"/Game/Generic/Loadouts/Precast/Havoc/VH_HVC_DEF_AssaultM_Loadout_T4_BP.VH_HVC_DEF_AssaultM_Loadout_T4_BP_C"},
    {L"HVCDEFDN_M(T4)", 33489356, 33489357, 6, 'D', "VH_HVC_DEF_DreadM_Loadout_T4_BP", L"/Game/Generic/Loadouts/Precast/Havoc/VH_HVC_DEF_DreadM_Loadout_T4_BP.VH_HVC_DEF_DreadM_Loadout_T4_BP_C"},
    {L"HVCDEFDN_H(T4)", 33489358, 33489359, 6, 'D', "VH_HVC_DEF_DreadH_Loadout_T4_BP", L"/Game/Generic/Loadouts/Precast/Havoc/VH_HVC_DEF_DreadH_Loadout_T4_BP.VH_HVC_DEF_DreadH_Loadout_T4_BP_C"},
    {L"HVCDEFSU_H(T4)", 33489361, 33489362, 6, 'D', "VH_HVC_DEF_SupportH_Loadout_T4_BP", L"/Game/Generic/Loadouts/Precast/Havoc/VH_HVC_DEF_SupportH_Loadout_T4_BP.VH_HVC_DEF_SupportH_Loadout_T4_BP_C"},
    {L"HVCDEFSU_M(T4)", 33489365, 33489366, 6, 'D', "VH_HVC_DEF_SupportM_Loadout_T4_BP", L"/Game/Generic/Loadouts/Precast/Havoc/VH_HVC_DEF_SupportM_Loadout_T4_BP.VH_HVC_DEF_SupportM_Loadout_T4_BP_C"},
    {L"HVCUTIAS_M(T4)", 33489368, 33489369, 6, 'U', "VH_HVC_UTI_AssaultM_Loadout_T4_BP", L"/Game/Generic/Loadouts/Precast/Havoc/VH_HVC_UTI_AssaultM_Loadout_T4_BP.VH_HVC_UTI_AssaultM_Loadout_T4_BP_C"},
    {L"HVCUTISU_L(T4)", 33489371, 33489372, 6, 'U', "VH_HVC_UTI_SupportL_Loadout_T4_BP", L"/Game/Generic/Loadouts/Precast/Havoc/VH_HVC_UTI_SupportL_Loadout_T4_BP.VH_HVC_UTI_SupportL_Loadout_T4_BP_C"},
    {L"HVCUTIDN_L(T4)", 33489378, 33489379, 6, 'U', "VH_HVC_UTI_DreadL_Loadout_T4_BP", L"/Game/Generic/Loadouts/Precast/Havoc/VH_HVC_UTI_DreadL_Loadout_T4_BP.VH_HVC_UTI_DreadL_Loadout_T4_BP_C"},
    {L"HVCUTISN_L(T4)", 33489381, 33489380, 6, 'U', "VH_HVC_UTI_SniperL_Loadout_T4_BP", L"/Game/Generic/Loadouts/Precast/Havoc/VH_HVC_UTI_SniperL_Loadout_T4_BP.VH_HVC_UTI_SniperL_Loadout_T4_BP_C"},
    {L"HVCUTISC_H(T4)", 33489384, 33489385, 6, 'U', "VH_HVC_UTI_ScoutH_Loadout_T4_BP", L"/Game/Generic/Loadouts/Precast/Havoc/VH_HVC_UTI_ScoutH_Loadout_T4_BP.VH_HVC_UTI_ScoutH_Loadout_T4_BP_C"},
    {L"HVCOFFSC_L(T4)", 33489388, 33489389, 6, 'O', "VH_HVC_OFF_ScoutL_Loadout_T4_BP", L"/Game/Generic/Loadouts/Precast/Havoc/VH_HVC_OFF_ScoutL_Loadout_T4_BP.VH_HVC_OFF_ScoutL_Loadout_T4_BP_C"},
    {L"HVCOFFSC_M(T4)", 33489391, 33489392, 6, 'O', "VH_HVC_OFF_ScoutM_Loadout_T4_BP", L"/Game/Generic/Loadouts/Precast/Havoc/VH_HVC_OFF_ScoutM_Loadout_T4_BP.VH_HVC_OFF_ScoutM_Loadout_T4_BP_C"},
    {L"HVCOFFSN_M(T4)", 33489396, 33489397, 6, 'O', "VH_HVC_OFF_SniperM_Loadout_T4_BP", L"/Game/Generic/Loadouts/Precast/Havoc/VH_HVC_OFF_SniperM_Loadout_T4_BP.VH_HVC_OFF_SniperM_Loadout_T4_BP_C"},
    {L"HVCOFFSN_H(T4)", 33489398, 33489399, 6, 'O', "VH_HVC_OFF_SniperH_Loadout_T4_BP", L"/Game/Generic/Loadouts/Precast/Havoc/VH_HVC_OFF_SniperH_Loadout_T4_BP.VH_HVC_OFF_SniperH_Loadout_T4_BP_C"},
    {L"HVCOFFAS_L(T4)", 33489401, 33489402, 6, 'O', "VH_HVC_OFF_AssaultL_Loadout_T4_BP", L"/Game/Generic/Loadouts/Precast/Havoc/VH_HVC_OFF_AssaultL_Loadout_T4_BP.VH_HVC_OFF_AssaultL_Loadout_T4_BP_C"},
    {L"HVCOFFDN_L(T4)", 33489403, 33489405, 6, 'O', "VH_HVC_OFF_DreadL_Loadout_T4_BP", L"/Game/Generic/Loadouts/Precast/Havoc/VH_HVC_OFF_DreadL_Loadout_T4_BP.VH_HVC_OFF_DreadL_Loadout_T4_BP_C"},
    {L"HVCOFFAS_L(T5)", 33489402, 33489402, 12, 'O', "VH_HVC_OFF_AssaultL_Loadout_T5_BP", L"/Game/Generic/Loadouts/Precast/Havoc/VH_HVC_OFF_AssaultL_Loadout_T5_BP.VH_HVC_OFF_AssaultL_Loadout_T5_BP_C"},
    {L"HVCOFFDN_L(T5)", 33489405, 33489405, 12, 'O', "VH_HVC_OFF_DreadL_Loadout_T5_BP", L"/Game/Generic/Loadouts/Precast/Havoc/VH_HVC_OFF_DreadL_Loadout_T5_BP.VH_HVC_OFF_DreadL_Loadout_T5_BP_C"},
    {L"HVCDEFAS_H(T5)", 33489350, 33489350, 12, 'D', "VH_HVC_DEF_AssaultH_Loadout_T5_BP", L"/Game/Generic/Loadouts/Precast/Havoc/VH_HVC_DEF_AssaultH_Loadout_T5_BP.VH_HVC_DEF_AssaultH_Loadout_T5_BP_C"},
    {L"HVCDEFAS_M(T5)", 33489353, 33489353, 12, 'D', "VH_HVC_DEF_AssaultM_Loadout_T5_BP", L"/Game/Generic/Loadouts/Precast/Havoc/VH_HVC_DEF_AssaultM_Loadout_T5_BP.VH_HVC_DEF_AssaultM_Loadout_T5_BP_C"},
    {L"HVCDEFDN_M(T5)", 33489357, 33489357, 12, 'D', "VH_HVC_DEF_DreadM_Loadout_T5_BP", L"/Game/Generic/Loadouts/Precast/Havoc/VH_HVC_DEF_DreadM_Loadout_T5_BP.VH_HVC_DEF_DreadM_Loadout_T5_BP_C"},
    {L"HVCDEFDN_H(T5)", 33489359, 33489359, 12, 'D', "VH_HVC_DEF_DreadH_Loadout_T5_BP", L"/Game/Generic/Loadouts/Precast/Havoc/VH_HVC_DEF_DreadH_Loadout_T5_BP.VH_HVC_DEF_DreadH_Loadout_T5_BP_C"},
    {L"HVCOFFSN_H(T5)", 33489399, 33489399, 12, 'O', "VH_HVC_OFF_SniperH_Loadout_T5_BP", L"/Game/Generic/Loadouts/Precast/Havoc/VH_HVC_OFF_SniperH_Loadout_T5_BP.VH_HVC_OFF_SniperH_Loadout_T5_BP_C"},
    {L"HVCDEFSN_H(T5)", 33489360, 33489360, 12, 'D', "VH_HVC_DEF_SniperH_Loadout_T5_BP", L"/Game/Generic/Loadouts/Precast/Havoc/VH_HVC_DEF_SniperH_Loadout_T5_BP.VH_HVC_DEF_SniperH_Loadout_T5_BP_C"},
    {L"HVCDEFSU_H(T5)", 33489362, 33489362, 12, 'D', "VH_HVC_DEF_SupportH_Loadout_T5_BP", L"/Game/Generic/Loadouts/Precast/Havoc/VH_HVC_DEF_SupportH_Loadout_T5_BP.VH_HVC_DEF_SupportH_Loadout_T5_BP_C"},
    {L"HVCDEFSU_M(T5)", 33489366, 33489366, 12, 'D', "VH_HVC_DEF_SupportM_Loadout_T5_BP", L"/Game/Generic/Loadouts/Precast/Havoc/VH_HVC_DEF_SupportM_Loadout_T5_BP.VH_HVC_DEF_SupportM_Loadout_T5_BP_C"},
    {L"HVCUTIAS_M(T5)", 33489369, 33489369, 12, 'U', "VH_HVC_UTI_AssaultM_Loadout_T5_BP", L"/Game/Generic/Loadouts/Precast/Havoc/VH_HVC_UTI_AssaultM_Loadout_T5_BP.VH_HVC_UTI_AssaultM_Loadout_T5_BP_C"},
    {L"HVCUTISU_L(T5)", 33489372, 33489372, 12, 'U', "VH_HVC_UTI_SupportL_Loadout_T5_BP", L"/Game/Generic/Loadouts/Precast/Havoc/VH_HVC_UTI_SupportL_Loadout_T5_BP.VH_HVC_UTI_SupportL_Loadout_T5_BP_C"},
    {L"HVCUTIDN_M(T5)", 33489373, 33489373, 12, 'U', "VH_HVC_UTI_DreadM_Loadout_T5_BP", L"/Game/Generic/Loadouts/Precast/Havoc/VH_HVC_UTI_DreadM_Loadout_T5_BP.VH_HVC_UTI_DreadM_Loadout_T5_BP_C"},
    {L"HVCOFFSN_M(T5)", 33489397, 33489397, 12, 'O', "VH_HVC_OFF_SniperM_Loadout_T5_BP", L"/Game/Generic/Loadouts/Precast/Havoc/VH_HVC_OFF_SniperM_Loadout_T5_BP.VH_HVC_OFF_SniperM_Loadout_T5_BP_C"},
    {L"HVCUTIDN_L(T5)", 33489379, 33489379, 12, 'U', "VH_HVC_UTI_DreadL_Loadout_T5_BP", L"/Game/Generic/Loadouts/Precast/Havoc/VH_HVC_UTI_DreadL_Loadout_T5_BP.VH_HVC_UTI_DreadL_Loadout_T5_BP_C"},
    {L"HVCUTISN_L(T5)", 33489380, 33489380, 12, 'U', "VH_HVC_UTI_SniperL_Loadout_T5_BP", L"/Game/Generic/Loadouts/Precast/Havoc/VH_HVC_UTI_SniperL_Loadout_T5_BP.VH_HVC_UTI_SniperL_Loadout_T5_BP_C"},
    {L"HVCUTISC_H(T5)", 33489385, 33489385, 12, 'U', "VH_HVC_UTI_ScoutH_Loadout_T5_BP", L"/Game/Generic/Loadouts/Precast/Havoc/VH_HVC_UTI_ScoutH_Loadout_T5_BP.VH_HVC_UTI_ScoutH_Loadout_T5_BP_C"},
    {L"HVCOFFSC_L(T5)", 33489389, 33489389, 12, 'O', "VH_HVC_OFF_ScoutL_Loadout_T5_BP", L"/Game/Generic/Loadouts/Precast/Havoc/VH_HVC_OFF_ScoutL_Loadout_T5_BP.VH_HVC_OFF_ScoutL_Loadout_T5_BP_C"},
    {L"HVCOFFSC_M(T5)", 33489392, 33489392, 12, 'O', "VH_HVC_OFF_ScoutM_Loadout_T5_BP", L"/Game/Generic/Loadouts/Precast/Havoc/VH_HVC_OFF_ScoutM_Loadout_T5_BP.VH_HVC_OFF_ScoutM_Loadout_T5_BP_C"},
};
static const int kHavocShipCount = (int)(sizeof kHavocShips / sizeof kHavocShips[0]);
#define RVA_SPAWN_PLAYER 0x5998A0       // AYPlayerController::SpawnPlayer(pc)
#define RVA_WEAK_PTR_SET 0xD64B60       // TWeakObjectPtr = obj (index +0xC, serial)
#define OFF_PC_LOADOUT_HOLDER 0x958     // -> +0x28 the loadout manager (0x5B7A60)
#define OFF_PC_SPAWN_PENDING 0xB88      // byte; SpawnPlayer clears it
#define OFF_PC_LAST_PAWN 0xBB0          // TWeakObjectPtr<YPawn>
#define OFF_PC_NO_INPLACE_SPAWN 0xCA0   // byte; set -> no spawn at the last pawn
typedef void(__fastcall *tSpawnPlayer)(void *pc);
typedef void(__fastcall *tWeakSet)(void *weak, void *obj);

static bool HavocShipUpgradesOn() {
  static int s_on = -1;
  if (s_on < 0)
    s_on = SwitchOn("DN_HOST_NO_HAVOC_SHIP_UPGRADES", "dn_host_no_havoc_ship_upgrades.txt") ? 0 : 1;
  return s_on == 1;
}
static bool HavocIsShipWave(int wave) {
  for (const HavocShip &s : kHavocShips)
    if (s.wave > 0 && s.wave == wave)
      return true;
  return false;
}
static int HavocShipByItem(int itemId) {
  for (int i = 0; i < kHavocShipCount; ++i)
    if (kHavocShips[i].itemId == itemId)
      return i;
  return -1;
}
static void *HavocLoadoutManager(void *pc) {
  uint8_t *h = IsReadable((uint8_t *)pc + OFF_PC_LOADOUT_HOLDER, 8) ? *(uint8_t **)((uint8_t *)pc + OFF_PC_LOADOUT_HOLDER) : nullptr;
  return h && IsReadable(h + 0x28, 8) ? *(void **)(h + 0x28) : nullptr;
}
// The kHavocShips row of the active loadout, or -1. By its
// m_precastLoadoutID when set; else by its loadout id FName (+0xB0), which on
// these classes is the default object's name, "Default__<asset>_C" (smoke
// host 2026-10-19: every Havoc CDO has m_precastLoadoutID 0 and that FName);
// else by its class name.
static int HavocCurrentShipRow(void *pc) {
  uint8_t *mgr = (uint8_t *)HavocLoadoutManager(pc);
  uint8_t *lo = mgr && IsReadable(mgr + OFF_LOADOUT_MGR_ACTIVE, 8) ? *(uint8_t **)(mgr + OFF_LOADOUT_MGR_ACTIVE) : nullptr;
  if (!lo || !IsReadable(lo, 0xB8))
    return -1;
  int off = PropOffset((UObjectMin *)lo, "m_precastLoadoutID");
  int id = off > 0 && IsReadable(lo + off, 4) ? *(int32_t *)(lo + off) : 0;
  int row = id > 0 ? HavocShipByItem(id) : -1;
  if (row >= 0)
    return row;
  const char *fname = NameText(*(FNameMin *)(lo + 0xB0));
  const char *cls = ClassNameOf(lo);
  char want[160];
  for (int i = 0; i < kHavocShipCount; ++i) {
    _snprintf_s(want, sizeof want, _TRUNCATE, "Default__%s_C", kHavocShips[i].asset);
    if (fname && !strcmp(fname, want))
      return i;
    if (cls && !strcmp(cls, want + 9))
      return i;
  }
  static int s_logged = 0;
  if (s_logged++ < 4)
    Logf("havoc: active loadout %p not a Havoc ship (precast id %d, id FName %s, class %s)", lo, id, fname ? fname : "?",
         cls ? cls : "?");
  return -1;
}
static int HavocCurrentShipItem(void *pc) {
  int row = HavocCurrentShipRow(pc);
  return row >= 0 ? kHavocShips[row].itemId : -1;
}
// One ship of the category from the rows appearing on this wave; `prefer`
// wins when it is one of them.
static int HavocPickShip(char cat, int wave, int prefer) {
  if (prefer >= 0 && kHavocShips[prefer].cat == cat && kHavocShips[prefer].wave == wave)
    return prefer;
  int n = 0;
  for (const HavocShip &s : kHavocShips)
    if (s.cat == cat && s.wave == wave)
      ++n;
  if (!n)
    return -1;
  int r = (int)(HavocRand() % (uint32_t)n);
  for (int i = 0; i < kHavocShipCount; ++i)
    if (kHavocShips[i].cat == cat && kHavocShips[i].wave == wave && r-- == 0)
      return i;
  return -1;
}

// Smoke test (dn_host_havoc_smoke.txt): every ship class loads, and its
// default object's loadout id FName is "Default__<asset>_C".
static void HavocVerifyShipClasses() {
  int ok = 0, bad = 0;
  for (const HavocShip &s : kHavocShips) {
    PrecastPath pp = {s.asset, s.path};
    UObjectMin *cdo = ResolvePrecastByName(&pp);
    const char *fname = cdo && IsReadable((uint8_t *)cdo + 0xB0, 8) ? NameText(*(FNameMin *)((uint8_t *)cdo + 0xB0)) : nullptr;
    char want[160];
    _snprintf_s(want, sizeof want, _TRUNCATE, "Default__%s_C", s.asset);
    if (cdo && fname && !strcmp(fname, want))
      ++ok;
    else {
      ++bad;
      Logf("havoc: ship class %s -> cdo %p, id FName %s (want %s)", s.asset, cdo, fname ? fname : "?", want);
    }
  }
  Logf("havoc: ship classes checked: %d OK, %d wrong or missing", ok, bad);
}

static bool HavocSwapShip(void *pc, int row) {
  const HavocShip &s = kHavocShips[row];
  void *mgr = HavocLoadoutManager(pc);
  if (!mgr) {
    Logf("havoc: ship upgrade %ls for %p -- no loadout manager", s.row, pc);
    return false;
  }
  PrecastPath pp = {s.asset, s.path};
  UObjectMin *cdo = ResolvePrecastByName(&pp);
  if (!cdo || !IsReadable((uint8_t *)cdo + 0xB8, 8)) {
    Logf("havoc: ship upgrade %ls -- class %ls did not load", s.row, s.path);
    return false;
  }
  void *loadout = g_origFindLoadoutByID ? g_origFindLoadoutByID(mgr, (void **)((uint8_t *)cdo + 0xB0), 0) : nullptr;
  if (!loadout) {
    bool added = AddLoadoutGuarded(mgr, cdo);
    loadout = g_origFindLoadoutByID ? g_origFindLoadoutByID(mgr, (void **)((uint8_t *)cdo + 0xB0), 0) : nullptr;
    Logf("havoc: ship upgrade %ls -- registered %s with manager %p -> %s, found %p", s.row, s.asset, mgr,
         added ? "ok" : "EXCEPTION", loadout);
  }
  if (!loadout)
    return false;
  uint8_t *p = (uint8_t *)pc;
  void *pawn = IsReadable(p + OFF_CONTROLLER_PAWN, 8) ? *(void **)(p + OFF_CONTROLLER_PAWN) : nullptr;
  int before = HavocCurrentShipItem(pc);
  __try {
    ((tActivateLoadout)(g_base + RVA_ACTIVATE_LOADOUT))(mgr, loadout, 1);
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    Logf("havoc: ship upgrade %ls -- EXCEPTION in ActivateLoadout", s.row);
    return false;
  }
  int after = HavocCurrentShipItem(pc);
  if (!pawn) {
    Logf("havoc: ship upgrade %ls for %p -- active loadout %d -> %d; no ship now, the next spawn uses it", s.row, pc,
         before, after);
    return after == s.itemId;
  }
  uint8_t noInPlace = p[OFF_PC_NO_INPLACE_SPAWN];
  __try {
    ((tWeakSet)(g_base + RVA_WEAK_PTR_SET))(p + OFF_PC_LAST_PAWN, pawn);
    p[OFF_PC_SPAWN_PENDING] = 1;
    ((tSpawnPlayer)(g_base + RVA_SPAWN_PLAYER))(pc);
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    Logf("havoc: ship upgrade %ls -- EXCEPTION in SpawnPlayer", s.row);
    return false;
  }
  void *now = IsReadable(p + OFF_CONTROLLER_PAWN, 8) ? *(void **)(p + OFF_CONTROLLER_PAWN) : nullptr;
  Logf("havoc: ship upgrade %ls (%s) for %p -- active loadout %d -> %d, ship %p %s -> %p %s (pending %u, no-in-place %u)",
       s.row, s.asset, pc, before, after, pawn, ClassNameOf(pawn), now, now ? ClassNameOf(now) : "-",
       p[OFF_PC_SPAWN_PENDING], noInPlace);
  return after == s.itemId && now && now != pawn;
}

static void HavocOfferUpgrades(uint8_t *gs) {
  uint8_t *pcs[16];
  int npc = HavocHumanPCs(gs, pcs, 16);
  for (int i = 0; i < npc; ++i) {
    HavocPlayer *p = HavocPlayerFor(pcs[i], true);
    if (!p)
      continue;
    static const char kCats[3] = {'O', 'D', 'U'};
    int wave = *(int32_t *)(gs + OFF_HVC_GS_WAVE_INDEX);
    bool ships = HavocShipUpgradesOn() && HavocIsShipWave(wave);
    int cur = ships ? HavocShipByItem(HavocCurrentShipItem(pcs[i])) : -1;
    int next = cur >= 0 ? HavocShipByItem(kHavocShips[cur].nextTierId) : -1;
    for (int k = 0; k < 3; ++k) {
      p->offer[k] = ships ? -1 : HavocPickBoost(kCats[k]);
      p->offerShip[k] = ships ? HavocPickShip(kCats[k], wave, next) : -1;
      p->offerNames[k] = {};
      const wchar_t *nm = p->offer[k] >= 0 ? kHavocBoosts[p->offer[k]].id
                          : p->offerShip[k] >= 0 ? kHavocShips[p->offerShip[k]].row : nullptr;
      if (nm)
        ((tFNameCtor)(g_base + RVA_FNAME_CTOR_WIDE))(&p->offerNames[k], nm, 1);
    }
    bool ok = CallFilled(pcs[i], L"ClientReceiveRandSelectedUpgrades", FillNameArray, p);
    uint8_t *pri = *(uint8_t **)(pcs[i] + OFF_PC_PLAYER_STATE);
    if (ships) {
      Logf("havoc: wave %d is a ship-upgrade wave -- offered %ls / %ls / %ls to %p (now %ls, next tier %ls)%s", wave,
           p->offerShip[0] >= 0 ? kHavocShips[p->offerShip[0]].row : L"-",
           p->offerShip[1] >= 0 ? kHavocShips[p->offerShip[1]].row : L"-",
           p->offerShip[2] >= 0 ? kHavocShips[p->offerShip[2]].row : L"-", pcs[i],
           cur >= 0 ? kHavocShips[cur].row : L"unknown", next >= 0 ? kHavocShips[next].row : L"-", ok ? "" : " -- RPC NOT FOUND");
      continue;
    }
    Logf("havoc: offered %ls / %ls / %ls to %p (xp %d)%s", p->offer[0] >= 0 ? kHavocBoosts[p->offer[0]].id : L"-",
         p->offer[1] >= 0 ? kHavocBoosts[p->offer[1]].id : L"-", p->offer[2] >= 0 ? kHavocBoosts[p->offer[2]].id : L"-",
         pcs[i], *(int32_t *)(pri + OFF_HVC_PRI_CURRENT_XP), ok ? "" : " -- RPC NOT FOUND");
  }
}

#define RVA_HVC_SERVER_SELECTED_UPGRADE_IMPL 0x406040
typedef void(__fastcall *tHvcSelectedUpgrade)(void *pc, int32_t slot);
static tHvcSelectedUpgrade g_origHvcSelectedUpgrade = nullptr;
static void __fastcall HookHvcSelectedUpgrade(void *pc, int32_t slot) {
  g_origHvcSelectedUpgrade(pc, slot);
  HavocPlayer *p = HavocPlayerFor(pc, false);
  uint8_t *pri = IsReadable((uint8_t *)pc + OFF_PC_PLAYER_STATE, 8) ? *(uint8_t **)((uint8_t *)pc + OFF_PC_PLAYER_STATE) : nullptr;
  int b = p && slot >= 0 && slot < 3 ? p->offer[slot] : -1;
  int ship = p && slot >= 0 && slot < 3 ? p->offerShip[slot] : -1;
  if (ship >= 0) {
    // A ship costs no XP (see HavocSwapShip); one ship per upgrade phase.
    bool swapped = HavocSwapShip(pc, ship);
    if (swapped)
      p->offerShip[0] = p->offerShip[1] = p->offerShip[2] = -1;
    Logf("havoc: %p picked slot %d (ship %ls) -> %s", pc, slot, kHavocShips[ship].row, swapped ? "swapped" : "FAILED");
    HavocPurchaseResult r = {};
    r.ok = swapped ? 1 : 0;
    ((tFNameCtor)(g_base + RVA_FNAME_CTOR_WIDE))(&r.id, kHavocShips[ship].row, 1);
    CallFilled(pc, L"ClientReceiveBoostPurchaseResult", FillPurchaseResult, &r);
    return;
  }
  uint8_t ok = 0;
  int xp = -1;
  if (b >= 0 && pri && IsReadable(pri, OFF_HVC_PRI_CONTRIBUTED_XP + 4) &&
      ClassChainContains((UObjectMin *)pri, "YPlayerReplicationInfo_Havoc")) {
    xp = *(int32_t *)(pri + OFF_HVC_PRI_CURRENT_XP);
    if (xp >= kHavocBoosts[b].cost && p->nOwned < (int)(sizeof p->owned / sizeof p->owned[0])) {
      ((void(__fastcall *)(void *, int32_t))(g_base + RVA_HVC_PRI_DEDUCT_XP))(pri, kHavocBoosts[b].cost);
      p->owned[p->nOwned++] = b;
      p->offer[slot] = -1;
      ok = 1;
    }
  }
  Logf("havoc: %p picked slot %d (%ls, cost %d) with xp %d -> %s", pc, slot, b >= 0 ? kHavocBoosts[b].id : L"none",
       b >= 0 ? kHavocBoosts[b].cost : 0, xp, ok ? "bought" : "refused");
  // AYPlayerReplicationInfo_Havoc::ClientXPDeducted ("Update upgrades panel",
  // 0x4030F2) is NOT a reflected UFunction: FindFunctionChecked on it kills
  // the host ("Failed to find function ClientXPDeducted in
  // YPlayerReplicationInfo_Havoc"). m_currentXP replicates by itself.
  HavocPurchaseResult r = {};
  r.ok = ok;
  if (b >= 0)
    ((tFNameCtor)(g_base + RVA_FNAME_CTOR_WIDE))(&r.id, kHavocBoosts[b].id, 1);
  CallFilled(pc, L"ClientReceiveBoostPurchaseResult", FillPurchaseResult, &r);
}

// Feats components by ship, recorded at UYFeatsComponent begin-play.
// modGen/modsOn/permApplied: the Havoc wave and permanent enemy modifiers
// already on this ship (HavocApplyModifiers). 256 slots: every wave enemy
// has a feats component too.
struct FeatsByPawn {
  void *pawn, *comp;
  LONG modGen;
  int modsOn[2];
  int permApplied;
};
static FeatsByPawn g_featsByPawn[256];
static void RecordFeatsComp(void *pawn, void *comp) {
  int freeAt = -1;
  for (int i = 0; i < 256; ++i) {
    if (g_featsByPawn[i].pawn == pawn) {
      if (g_featsByPawn[i].comp != comp)
        g_featsByPawn[i] = {pawn, comp, -1, {-1, -1}, 0};
      return;
    }
    if (freeAt < 0 && !IsReadable(g_featsByPawn[i].comp, OFF_COMPONENT_OWNER + 8))
      freeAt = i;
  }
  static int s_next = 0;
  if (freeAt < 0)
    freeAt = s_next++ % 256;
  g_featsByPawn[freeAt] = {pawn, comp, -1, {-1, -1}, 0};
}
static void *FeatsCompOf(void *pawn) {
  for (FeatsByPawn &e : g_featsByPawn)
    if (e.pawn == pawn && IsReadable(e.comp, OFF_COMPONENT_OWNER + 8) &&
        *(void **)((uint8_t *)e.comp + OFF_COMPONENT_OWNER) == pawn)
      return e.comp;
  return nullptr;
}

// Every director tick: put each player's bought boosts on their current ship.
static void HavocApplyBoosts() {
  for (HavocPlayer &p : g_hvcPlayers) {
    if (!p.pc || !p.nOwned || !IsReadable((uint8_t *)p.pc + OFF_CONTROLLER_PAWN, 8))
      continue;
    void *pawn = *(void **)((uint8_t *)p.pc + OFF_CONTROLLER_PAWN);
    if (!pawn)
      continue;
    if (pawn != p.appliedPawn)
      p.appliedPawn = pawn, p.nApplied = 0;
    if (p.nApplied >= p.nOwned)
      continue;
    void *comp = FeatsCompOf(pawn);
    if (!comp)
      continue;
    for (; p.nApplied < p.nOwned; ++p.nApplied) {
      const HavocBoost &b = kHavocBoosts[p.owned[p.nApplied]];
      FNameMin name{};
      ((tFNameCtor)(g_base + RVA_FNAME_CTOR_WIDE))(&name, b.feat, 1);
      bool ok = false;
      __try {
        ok = ((tAddFeat)(g_base + RVA_FEATS_ADD_FEAT))(comp, *(uint64_t *)&name);
      } __except (EXCEPTION_EXECUTE_HANDLER) {
        Logf("havoc: AddFeat(%ls) faulted on %p", b.feat, comp);
      }
      Logf("havoc: boost feat %ls on %s for %p -> %s", b.feat, ClassNameOf(pawn), p.pc, ok ? "added" : "FAILED");
    }
  }
}

static void __fastcall HookIntParmExecThunk(void *ctx, uint8_t *stack, void *result) {
  const char *n = CalledNativeName(stack);
  int32_t v = 0;
  if (*(void **)(stack + 0x20))
    ((void(__fastcall *)(void *, void *, void *))(g_base + RVA_FFRAME_STEP))(stack, *(void **)(stack + 0x18), &v);
  else {
    uint8_t *prop = *(uint8_t **)(stack + 0x80);
    *(void **)(stack + 0x80) = *(void **)(prop + 0x28);
    ((void(__fastcall *)(void *, void *, void *))(g_base + RVA_FFRAME_STEP_EXPLICIT_PROPERTY))(stack, &v, prop);
  }
  if (*(uint8_t **)(stack + 0x20))
    ++*(uint8_t **)(stack + 0x20);
  if (!n)
    return;
  if (!strcmp(n, "AddNPCToEnemyList")) {
    LONG i = InterlockedIncrement(&g_hvcEnemyCount) - 1;
    if (i < 256)
      g_hvcEnemyIds[i] = v;
    else
      InterlockedDecrement(&g_hvcEnemyCount);
    Logf("havoc: AddNPCToEnemyList(%d) on %s (list %ld)", v, ClassNameOf(ctx), g_hvcEnemyCount);
    return;
  }
  if (!strcmp(n, "AddXPForGroup")) {
    HavocAddXP((uint8_t *)ctx, v, "AddXPForGroup");
    return;
  }
  static char s_seen[16][48];
  static int s_nseen = 0;
  for (int i = 0; i < s_nseen; ++i)
    if (!strcmp(s_seen[i], n))
      return;
  if (s_nseen < 16)
    snprintf(s_seen[s_nseen++], sizeof s_seen[0], "%s", n);
  Logf("havoc: BP called compiled-out native %s(%d) on %s (first call)", n, v, ClassNameOf(ctx));
}

// ---------------------------------------------------------------------------
// Havoc, stage 2 (2026-10-19): the rules the PR's director guessed or left
// out, from GameState_Havoc_BP / PVE_HordeManager_BP bytecode (tools/bpdump),
// the client's own code and the Havoc data tables.
//
// Wave over. GameState_Havoc_BP CheckWaveOver: when GetNumberOfRemainingWave
// Enemies <= 0 AND m_numEnemiesKilledThisWave >= m_maxWaveEnemies + the
// bridgehead spawns, it sets god mode and -- once every XP drop is picked up
// or gone (AllXPDropsPickedUp, set by the pickups' CompleteBuffPodObjective
// IfAllRetrieved) and 2 s later -- calls SetSpecialWaveConditionsFulfilled
// (true). That native is compiled out (bool exec thunk 0x777000 -> the bare
// `ret` 0x1AA9450, same shape as the int thunk 0x742E90), so the hook below
// records it and the director ends the wave on it.
#define RVA_BOOL_PARM_EXEC_THUNK 0x777000
static tExecThunk g_origBoolParmExecThunk = nullptr;
static volatile LONG g_hvcWaveConditions = 0;
static const char *CalledNativeName(uint8_t *stack);
static void StepParm(uint8_t *stack, void *out);

static void __fastcall HookBoolParmExecThunk(void *ctx, uint8_t *stack, void *result) {
  const char *n = CalledNativeName(stack);
  int32_t v = 0;
  StepParm(stack, &v);
  if (*(uint8_t **)(stack + 0x20))
    ++*(uint8_t **)(stack + 0x20);
  if (!n)
    return;
  if (!strcmp(n, "SetSpecialWaveConditionsFulfilled")) {
    LONG before = InterlockedExchange(&g_hvcWaveConditions, v ? 1 : 0);
    if (before != (v ? 1 : 0))
      Logf("havoc: BP SetSpecialWaveConditionsFulfilled(%d) on %s", v ? 1 : 0, ClassNameOf(ctx));
    return;
  }
  static char s_seen[16][48];
  static int s_nseen = 0;
  for (int i = 0; i < s_nseen; ++i)
    if (!strcmp(s_seen[i], n))
      return;
  if (s_nseen < 16)
    snprintf(s_seen[s_nseen++], sizeof s_seen[0], "%s", n);
  Logf("havoc: BP called compiled-out native %s(%d) on %s (first call)", n, v, ClassNameOf(ctx));
}

// Kill XP. GameMode_Havoc_BP m_monsterKillXP = 25 (DefaultHavoc.ini says 10;
// the BP default wins), and the scoring table's HavocXPKill row scores the
// same 25. The native that paid it is compiled out with the rest of the
// server's death path. GUESS: every listed wave enemy's death pays it to the
// group (AddXPForGroup's rule); the killer is not known here.
static int HavocMonsterKillXP(uint8_t *gm) {
  static void *s_for = nullptr;
  static int s_xp = 0;
  if (s_for != gm) {
    s_for = gm;
    int off = PropOffset((UObjectMin *)gm, "m_monsterKillXP");
    s_xp = off > 0 && IsReadable(gm + off, 4) ? *(int32_t *)(gm + off) : 0;
    if (s_xp < 0 || s_xp > 10000)
      s_xp = 0;
    Logf("havoc: kill XP %d per enemy (m_monsterKillXP)", s_xp);
  }
  return s_xp;
}
static void HavocAddXP(uint8_t *gm, int xp, const char *why, void *contributor);
static void HavocOnEnemyKilled(uint8_t *gm) {
  int xp = HavocMonsterKillXP(gm);
  if (xp > 0)
    HavocAddXP(gm, xp, "enemy kill (m_monsterKillXP)", nullptr);
}

// Enemies on the list still alive: entry not yet spawned (the scene tick will
// spawn it), or its pawn live and not in state 2/3 (OFF_PAWN_STATE, the AI's
// own dead/dying filter 0x57BD10). Dead ids drop off the list.
static int HavocCountAliveEnemies(uint8_t *gm, uint8_t *gs) {
  uint8_t *scene = (uint8_t *)((tGetSceneManager)(g_base + RVA_COMBAT_SCENE_MANAGER))();
  if (!scene || !IsReadable(scene + OFF_SCENE_NPCS, 16))
    return -1;
  uint8_t *arr = *(uint8_t **)(scene + OFF_SCENE_NPCS);
  int cnt = *(int32_t *)(scene + OFF_SCENE_NPCS_COUNT);
  int n = (int)g_hvcEnemyCount, alive = 0, keep = 0;
  for (int i = 0; i < n && i < 256; ++i) {
    int id = (int)g_hvcEnemyIds[i];
    bool live = false;
    if (arr && id >= 0 && id < cnt && IsReadable(arr + (size_t)id * NPC_SPAWN_SIZE, NPC_SPAWN_SIZE)) {
      uint8_t *e = arr + (size_t)id * NPC_SPAWN_SIZE;
      if (!e[OFF_NPCSPAWN_INSTANCED])
        live = true;
      else {
        uint8_t *pawn = (uint8_t *)((tWeakGet)(g_base + RVA_WEAK_PTR_GET))(e + OFF_NPCSPAWN_PAWN);
        live = pawn && IsReadable(pawn + OFF_PAWN_STATE, 1) && pawn[OFF_PAWN_STATE] != 2 && pawn[OFF_PAWN_STATE] != 3;
        if (!live) {
          // A listed id leaves the list exactly once, so this counts each kill
          // once. A pawn already destroyed (weak ptr null) is counted but not
          // broadcast: the BP spawns the pickup at KilledYPawn's transform and
          // a null pawn would drop it at the origin.
          *(int32_t *)(gs + OFF_HVC_GS_KILLED) += 1;
          HavocOnEnemyKilled(gm);
          if (pawn) {
            struct { void *pawn; int32_t npcId; int32_t pad; } parms = {pawn, id, 0};
            // GUESS: KilledNPCID is the combat-scene spawn index (the id the
            // enemy-list hook records); the BP only forwards it, the XP pickup
            // path does not read it.
            ((void (*)(void *, void *))(g_base + RVA_MULTICAST_BROADCAST_NOPARMS))(gm + OFF_HVC_GM_ON_ANY_NPC_KILLED,
                                                                                  &parms);
          }
          Logf("havoc: enemy %d died (pawn %p) -> killed %d%s", id, pawn, *(int32_t *)(gs + OFF_HVC_GS_KILLED),
               pawn ? ", OnAnyNPCKilled broadcast" : ", pawn gone: no XP drop");
        }
      }
    }
    if (live) {
      g_hvcEnemyIds[keep++] = id;
      ++alive;
    }
  }
  // ids appended by the hook while we pruned stay at the tail
  LONG now = g_hvcEnemyCount;
  for (LONG i = n; i < now && i < 256; ++i)
    g_hvcEnemyIds[keep++] = g_hvcEnemyIds[i];
  InterlockedExchange(&g_hvcEnemyCount, keep);
  return alive;
}

static bool HostIsHavoc() {
  const wchar_t *cmd = GetCommandLineW();
  return cmd && wcsstr(cmd, L"-GameMode=Havoc ") != nullptr;
}

// ---------------------------------------------------------------------------
// Havoc wave modifiers (stage 2). The wave modifier component's server half is
// compiled out; the client half is intact: AYGameState_Havoc::
// ClientUpdateActiveWaveModifier(activeModifierNames, activePermanentEnemy
// ModifierNames) (thunk 0x7479B0, two TArray<FName>; parameter names from the
// UFunction construction 0x8D5071) fills the HUD's modifier list.
// Rules from the data: YHavocWaveModifierComponent m_desiredModifierSlotsNum
// = 2 (DefaultHavoc.ini), "NoModifier" fills an empty slot, "BossWave" marks
// a boss wave (DN_HavocBossWaves_DT 7/12/17/22). A row is eligible when
// m_minWave <= wave <= m_maxWave (-1 = open; 999 = never), weight > 0, and no
// chosen row lists it in m_excludes (or it a chosen row). Its m_feats go on
// the ships of m_affectedTeam for the wave and come off at its end.
// Permanent enemy modifiers (DN_HavocPermanentEnemyModifiers_DT): every row
// with m_wave <= the wave applies to every enemy ship; AddFeat (0x337850)
// stacks a feat the ship already has when the feat allows it (existing +0x51,
// 0x14033F060), which is how the repeated HVC_PEM_EnemyDamageBoost5 rows add up.
// GUESS: the selection is weighted random without repeats; boss waves get only
// "BossWave". The original's selection code is not in this exe.
struct HavocModifier {
  const wchar_t *id, *feats, *excludes;
  int minWave, maxWave, weight;
  char team; // 'A' all, 'P' players, 'E' enemies, 'N' none
};
struct HavocEnemyBoost {
  const wchar_t *id, *feat;
  int wave;
};
// DN_HavocModifiers_DT, verbatim (generated 2026-10-19): row, m_feats, m_excludes,
// m_minWave, m_maxWave, m_weight, m_affectedTeam (A all / P players / E enemies /
// N none). Feats and excludes are ";"-separated, as in the table.
static const HavocModifier kHavocModifiers[] = {
    {L"NoModifier", L"", L"", 0, 0, 1, 'N'},
    {L"BossWave", L"", L"", 999, 1, 0, 'A'},
    {L"HVC_enemies_damageresistanceFrontFull_damageresistanceBackNegative", L"HVC_damageresistanceFrontFull_damageresistanceBackNegative_1;HVC_damageresistanceFrontFull_damageresistanceBackNegative_2", L"", 1, -1, 1, 'A'},
    {L"HVC_global_abilitiesDamageHigh_abilitiesCooldownHigh", L"HVC_abilitiesDamageHigh_abilitiesCooldownHigh", L"HVC_global_abilitiesDamageLow_abilitiesCooldownLow", 1, -1, 1, 'A'},
    {L"HVC_global_abilitiesDamageLow_abilitiesCooldownLow", L"HVC_abilitiesDamageLow_abilitiesCooldownLow", L"HVC_global_abilitiesDamageHigh_abilitiesCooldownHigh", 1, -1, 1, 'A'},
    {L"HVC_global_damageresistanceBackNegative", L"HVC_damageresistanceBackNegative", L"", 1, -1, 1, 'A'},
    {L"HVC_global_energyNone", L"HVC_energyNone", L"HVC_global_energyRegenFast_energyCooldownLow_energyMaxLow;HVC_global_energyRegenHit_abilitiesCooldownHit;HVC_global_energyRegenNone_energyMaxHigh;HVC_players_energyFullKill_energyRegenNone;HVC_players_movementNone_movementFast;", 1, -1, 1, 'A'},
    {L"HVC_global_energyRegenFast_energyCooldownLow_energyMaxLow", L"HVC_energyRegenFast_energyCooldownLow_energyMaxLow", L"HVC_global_energyNone;HVC_players_energyFullKill_energyRegenNone;HVC_global_energyRegenNone_energyMaxHigh;", 1, -1, 1, 'A'},
    {L"HVC_global_energyRegenHit_abilitiesCooldownHit", L"HVC_energyRegenHit_abilitiesCooldownHit", L"HVC_global_energyNone", 1, -1, 1, 'A'},
    {L"HVC_global_energyRegenNone_energyMaxHigh", L"HVC_energyRegenNone_energyMaxHigh", L"HVC_global_energyNone;HVC_global_energyRegenFast_energyCooldownLow_energyMaxLow;HVC_players_energyFullKill_energyRegenNone;", 1, -1, 1, 'A'},
    {L"HVC_global_energyUnlimted_healthDecreaseHit", L"HVC_energyUnlimted_healthDecreaseHit_1;HVC_energyUnlimted_healthDecreaseHit_2;", L"", 999, -1, 1, 'A'},
    {L"HVC_global_healthRegenFast_healthCooldownHigh", L"HVC_healthRegenFast_healthCooldownHigh", L"HVC_global_healthRegenHit_healthRegenSlow;HVC_global_healthRegenNone_healthMaxHigh;HVC_players_healthFullKill_healthRegenNone;", 1, -1, 1, 'A'},
    {L"HVC_global_healthRegenHit_healthRegenSlow", L"HVC_healthRegenHit_healthRegenSlow_1;HVC_healthRegenHit_healthRegenSlow_2", L"HVC_global_healthRegenFast_healthCooldownHigh;HVC_players_healthFullKill_healthRegenNone", 1, -1, 1, 'A'},
    {L"HVC_global_healthRegenNone_healthMaxHigh", L"HVC_healthRegenNone_healthMaxHigh", L"HVC_global_healthRegenFast_healthCooldownHigh;HVC_players_healthFullKill_healthRegenNone", 1, -1, 1, 'A'},
    {L"HVC_global_weaponFirerateLow_weaponDamageHigh", L"HVC_weaponFirerateLow_weaponDamageHigh", L"", 1, -1, 1, 'A'},
    {L"HVC_players_abilitiesCooldownResetKill", L"HVC_abilitiesCooldownResetKill", L"", 1, -1, 1, 'A'},
    {L"HVC_players_aimLowWhileMoving_movementForwardFast", L"HVC_aimLowWhileMoving_movementForwardFast", L"", 1, -1, 1, 'A'},
    {L"HVC_players_movementNone_movementFast", L"HVC_movementNone_movementFast_1;HVC_movementNone_movementFast_2;", L"HVC_global_energyNone", 1, -1, 1, 'P'},
    {L"HVC_players_energyFullKill_energyRegenNone", L"HVC_energyFullKill_energyRegenNone_1;HVC_energyFullKill_energyRegenNone_2;", L"HVC_global_energyRegenNone_energyMaxHigh;HVC_global_energyRegenFast_energyCooldownLow_energyMaxLow;HVC_global_energyNone;", 1, -1, 1, 'A'},
    {L"HVC_players_energyMaxDecreaseKill", L"HVC_energyMaxDecreaseKill", L"", 999, -1, 1, 'A'},
    {L"HVC_players_energyUnlimted_wheelRestrictions_healthRegenNone", L"HVC_energyUnlimted_wheelRestrictions", L"", 999, -1, 1, 'A'},
    {L"HVC_players_healingNone_healingDamageresistanceFull", L"HVC_healingNone_healingDamageresistanceFull_1;HVC_healingNone_healingDamageresistanceFull_2;HVC_healingNone_healingDamageresistanceFull_3;", L"", 1, -1, 1, 'P'},
    {L"HVC_players_healthFullKill_healthRegenNone", L"HVC_healthFullKill_healthRegenNone_1;HVC_healthFullKill_healthRegenNone_2;", L"HVC_global_healthRegenNone_healthMaxHigh;HVC_global_healthRegenHit_healthRegenSlow;HVC_global_healthRegenFast_healthCooldownHigh;", 1, -1, 1, 'A'},
    {L"HVC_players_healthMaxDecreaseKill", L"HVC_healthMaxDecreaseKill", L"", 999, -1, 1, 'A'},
    {L"HVC_players_healthMaxIncreaseHeal", L"HVC_healthMaxIncreaseHeal", L"", 999, -1, 1, 'A'},
    {L"HVC_players_movementVerticalNone", L"HVC_movementVerticalNone", L"", 999, -1, 1, 'A'},
};
// DN_HavocPermanentEnemyModifiers_DT, verbatim: row, m_feats, m_wave.
static const HavocEnemyBoost kHavocEnemyBoosts[] = {
    {L"EnemyBoost_01_Damage", L"HVC_PEM_EnemyDamageBoost5", 3},
    {L"EnemyBoost_01_Health", L"HVC_PEM_EnemyHealthBoost5", 3},
    {L"EnemyBoost_02_Damage", L"HVC_PEM_EnemyDamageBoost5", 4},
    {L"EnemyBoost_02_Health", L"HVC_PEM_EnemyHealthBoost5", 4},
    {L"EnemyBoost_03_Damage", L"HVC_PEM_EnemyDamageBoost5", 5},
    {L"EnemyBoost_03_Health", L"HVC_PEM_EnemyHealthBoost5", 5},
    {L"EnemyBoost_04_Damage", L"HVC_PEM_EnemyDamageBoost5", 6},
    {L"EnemyBoost_04_Health", L"HVC_PEM_EnemyHealthBoost5", 6},
    {L"EnemyBoost_05_Damage", L"HVC_PEM_EnemyDamageBoost5", 8},
    {L"EnemyBoost_05_Health", L"HVC_PEM_EnemyHealthBoost5", 8},
    {L"EnemyBoost_06_Damage", L"HVC_PEM_EnemyDamageBoost5", 9},
    {L"EnemyBoost_06_Health", L"HVC_PEM_EnemyHealthBoost5", 9},
    {L"EnemyBoost_07_Damage", L"HVC_PEM_EnemyDamageBoost10", 10},
    {L"EnemyBoost_07_Health", L"HVC_PEM_EnemyHealthBoost10", 10},
    {L"EnemyBoost_08_Damage", L"HVC_PEM_EnemyDamageBoost10", 11},
    {L"EnemyBoost_08_Health", L"HVC_PEM_EnemyHealthBoost10", 11},
    {L"EnemyBoost_09_Damage", L"HVC_PEM_EnemyDamageBoost10", 13},
    {L"EnemyBoost_09_Health", L"HVC_PEM_EnemyHealthBoost10", 13},
    {L"EnemyBoost_10_Damage", L"HVC_PEM_EnemyDamageBoost10", 14},
    {L"EnemyBoost_10_Health", L"HVC_PEM_EnemyHealthBoost10", 14},
    {L"EnemyBoost_11_Damage", L"HVC_PEM_EnemyDamageBoost10", 15},
    {L"EnemyBoost_11_Health", L"HVC_PEM_EnemyHealthBoost10", 15},
    {L"EnemyBoost_12_Damage", L"HVC_PEM_EnemyDamageBoost10", 16},
    {L"EnemyBoost_12_Health", L"HVC_PEM_EnemyHealthBoost10", 16},
    {L"EnemyBoost_13_Damage", L"HVC_PEM_EnemyDamageBoost15", 18},
    {L"EnemyBoost_13_Health", L"HVC_PEM_EnemyHealthBoost15", 18},
    {L"EnemyBoost_14_Damage", L"HVC_PEM_EnemyDamageBoost15", 19},
    {L"EnemyBoost_14_Health", L"HVC_PEM_EnemyHealthBoost15", 19},
    {L"EnemyBoost_15_Damage", L"HVC_PEM_EnemyDamageBoost15", 20},
    {L"EnemyBoost_15_Health", L"HVC_PEM_EnemyHealthBoost15", 20},
    {L"EnemyBoost_16_Damage", L"HVC_PEM_EnemyDamageBoost15", 21},
    {L"EnemyBoost_16_Health", L"HVC_PEM_EnemyHealthBoost15", 21},
    {L"EnemyBoost_17_Damage", L"HVC_PEM_EnemyDamageBoost15", 23},
    {L"EnemyBoost_17_Health", L"HVC_PEM_EnemyHealthBoost15", 23},
};
// DN_HavocBossWaves_DT m_waveNumber.
static const int kHavocBossWaves[] = {7, 12, 17, 22};
static const int kHavocModifierCount = (int)(sizeof kHavocModifiers / sizeof kHavocModifiers[0]);
static const int kHavocEnemyBoostCount = (int)(sizeof kHavocEnemyBoosts / sizeof kHavocEnemyBoosts[0]);
#define RVA_FEATS_REMOVE_FEAT 0x359770 // (featsComp, FName): RemoveFeat thunk 0x76ECE0's body
typedef void(__fastcall *tRemoveFeat)(void *featsComp, uint64_t featName);

static int g_hvcActiveMods[2] = {-1, -1}; // kHavocModifiers index
static volatile LONG g_hvcModGen = 0;      // bumped per wave: pawns re-check their feats
static int g_hvcModWave = 0;               // the wave the active modifiers are for

static int HavocModifierIndex(const wchar_t *id) {
  for (int i = 0; i < kHavocModifierCount; ++i)
    if (!wcscmp(kHavocModifiers[i].id, id))
      return i;
  return -1;
}
static bool HavocListHas(const wchar_t *list, const wchar_t *id) {
  size_t n = wcslen(id);
  for (const wchar_t *p = list; p && *p;) {
    const wchar_t *e = wcschr(p, L';');
    size_t len = e ? (size_t)(e - p) : wcslen(p);
    if (len == n && !wcsncmp(p, id, n))
      return true;
    p = e ? e + 1 : nullptr;
  }
  return false;
}
static bool HavocIsBossWave(int wave) {
  for (int w : kHavocBossWaves)
    if (w == wave)
      return true;
  return false;
}
static uint32_t HavocRand() {
  static uint32_t s = 0;
  if (!s)
    s = GetTickCount() | 1;
  s ^= s << 13, s ^= s >> 17, s ^= s << 5;
  return s;
}

// Calls a UFunction, filling each parameter by its NAME (and class).
typedef void (*tNamedFill)(uint8_t *parms, const char *propClass, const char *propName, uint32_t off, void *ctx);
static bool CallNamed(void *obj, const wchar_t *name, tNamedFill fill, void *ctx) {
  if (!IsReadable(obj, sizeof(UObjectMin)))
    return false;
  FNameMin n = {};
  ((tFNameCtor)(g_base + RVA_FNAME_CTOR_WIDE))(&n, name, 1 /* FNAME_Add */);
  void *fn = ((tFindFunctionChecked)(g_base + RVA_FIND_FUNCTION_CHECKED))(obj, *(uint64_t *)&n);
  if (!fn || !IsReadable((uint8_t *)fn + 0x40, 8))
    return false;
  alignas(16) uint8_t parms[256] = {};
  UObjectMin *pp = *(UObjectMin **)((uint8_t *)fn + 0x38);
  for (int j = 0; pp && j < 8 && IsReadable(pp, 0x58); ++j) {
    uint32_t off = *(uint32_t *)((uint8_t *)pp + 0x50);
    const char *pn = NameText(pp->Name);
    if (fill && off < sizeof parms - 16)
      fill(parms, ClassNameOf(pp), pn ? pn : "", off, ctx);
    pp = *(UObjectMin **)((uint8_t *)pp + 0x28);
  }
  void **vt = *(void ***)obj;
  __try {
    ((tProcessEventVirt)vt[VT_PROCESS_EVENT / 8])(obj, fn, parms);
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    Logf("havoc: EXCEPTION in %ls on %s", name, ClassNameOf(obj));
    return false;
  }
  return true;
}

struct HavocNameArrays {
  FNameMin active[4];
  int nActive;
  FNameMin permanent[64];
  int nPermanent;
};
static void FillModifierArrays(uint8_t *parms, const char *pc, const char *pn, uint32_t off, void *ctx) {
  HavocNameArrays *a = (HavocNameArrays *)ctx;
  if (strcmp(pc, "ArrayProperty"))
    return;
  bool perm = strstr(pn, "Permanent") != nullptr;
  *(void **)(parms + off) = perm ? (void *)a->permanent : (void *)a->active;
  *(int32_t *)(parms + off + 8) = perm ? a->nPermanent : a->nActive;
  *(int32_t *)(parms + off + 12) = perm ? a->nPermanent : a->nActive;
}

// Picks the wave's modifiers and tells every client.
static void HavocStartWaveModifiers(uint8_t *gs, int wave) {
  g_hvcActiveMods[0] = g_hvcActiveMods[1] = -1;
  if (HavocIsBossWave(wave)) {
    g_hvcActiveMods[0] = HavocModifierIndex(L"BossWave");
  } else {
    for (int slot = 0; slot < 2; ++slot) {
      int total = 0;
      int ok[64], nok = 0;
      for (int i = 0; i < kHavocModifierCount && nok < 64; ++i) {
        const HavocModifier &m = kHavocModifiers[i];
        if (m.team == 'N' || m.weight <= 0 || wave < m.minWave || (m.maxWave >= 0 && wave > m.maxWave))
          continue;
        bool clash = false;
        for (int s = 0; s < slot; ++s) {
          int c = g_hvcActiveMods[s];
          if (c == i || (c >= 0 && (HavocListHas(kHavocModifiers[c].excludes, m.id) ||
                                    HavocListHas(m.excludes, kHavocModifiers[c].id))))
            clash = true;
        }
        if (!clash)
          ok[nok++] = i, total += m.weight;
      }
      if (total <= 0)
        break;
      int r = (int)(HavocRand() % (uint32_t)total);
      for (int k = 0; k < nok; ++k)
        if ((r -= kHavocModifiers[ok[k]].weight) < 0) {
          g_hvcActiveMods[slot] = ok[k];
          break;
        }
    }
  }
  g_hvcModWave = wave;
  InterlockedIncrement(&g_hvcModGen);
  HavocNameArrays a = {};
  for (int s = 0; s < 2; ++s) {
    const wchar_t *id = g_hvcActiveMods[s] >= 0 ? kHavocModifiers[g_hvcActiveMods[s]].id : L"NoModifier";
    ((tFNameCtor)(g_base + RVA_FNAME_CTOR_WIDE))(&a.active[a.nActive++], id, 1);
  }
  for (int i = 0; i < kHavocEnemyBoostCount && a.nPermanent < 64; ++i)
    if (kHavocEnemyBoosts[i].wave <= wave)
      ((tFNameCtor)(g_base + RVA_FNAME_CTOR_WIDE))(&a.permanent[a.nPermanent++], kHavocEnemyBoosts[i].id, 1);
  bool sent = CallNamed(gs, L"ClientUpdateActiveWaveModifier", FillModifierArrays, &a);
  Logf("havoc: wave %d modifiers %ls / %ls, %d permanent enemy modifier(s)%s", wave,
       g_hvcActiveMods[0] >= 0 ? kHavocModifiers[g_hvcActiveMods[0]].id : L"NoModifier",
       g_hvcActiveMods[1] >= 0 ? kHavocModifiers[g_hvcActiveMods[1]].id : L"NoModifier", a.nPermanent,
       sent ? "" : " -- ClientUpdateActiveWaveModifier NOT FOUND");
}

// Every feat of a ";" list, one call each.
static int HavocForEachFeat(const wchar_t *list, void *comp, bool add, const char *what, void *pawn) {
  int done = 0;
  wchar_t buf[128];
  for (const wchar_t *p = list; p && *p;) {
    const wchar_t *e = wcschr(p, L';');
    size_t len = e ? (size_t)(e - p) : wcslen(p);
    if (len > 0 && len < 128) {
      memcpy(buf, p, len * sizeof(wchar_t));
      buf[len] = 0;
      FNameMin name{};
      ((tFNameCtor)(g_base + RVA_FNAME_CTOR_WIDE))(&name, buf, 1);
      bool ok = true;
      __try {
        if (add)
          ok = ((tAddFeat)(g_base + RVA_FEATS_ADD_FEAT))(comp, *(uint64_t *)&name);
        else
          ((tRemoveFeat)(g_base + RVA_FEATS_REMOVE_FEAT))(comp, *(uint64_t *)&name);
      } __except (EXCEPTION_EXECUTE_HANDLER) {
        ok = false;
      }
      static int s_logged = 0;
      if (s_logged++ < 60 || !ok)
        Logf("havoc: %s feat %ls %s %s -> %s", what, buf, add ? "on" : "off", ClassNameOf(pawn), ok ? "ok" : "FAILED");
      ++done;
    }
    p = e ? e + 1 : nullptr;
  }
  return done;
}

// Puts the wave's modifiers and the permanent enemy modifiers on every ship
// that does not have them yet (new enemies, respawned players). Each director
// tick, so a ship spawned mid-wave is covered within a second.
static bool HavocPawnIsPlayer(void *pawn) {
  uint8_t *ctrl = IsReadable((uint8_t *)pawn + OFF_PAWN_CONTROLLER, 8) ? *(uint8_t **)((uint8_t *)pawn + OFF_PAWN_CONTROLLER) : nullptr;
  return ctrl && IsReadable(ctrl, sizeof(UObjectMin)) && ClassChainContains((UObjectMin *)ctrl, "PlayerController");
}
// A recorded ship that still exists: its feats component is still owned by it
// and it is a living YPawn. A destroyed ship's memory gets reused (live log
// 2026-10-19: AddFeat "on None" / on a weapon actor -> FAILED), so all three.
static bool HavocLiveShip(const FeatsByPawn &e) {
  if (!e.pawn || !IsReadable(e.comp, OFF_COMPONENT_OWNER + 8) || *(void **)((uint8_t *)e.comp + OFF_COMPONENT_OWNER) != e.pawn)
    return false;
  uint8_t *pw = (uint8_t *)e.pawn;
  return IsReadable(pw + OFF_PAWN_STATE, 1) && pw[OFF_PAWN_STATE] != 2 && pw[OFF_PAWN_STATE] != 3 &&
         ClassChainContains((UObjectMin *)pw, "YPawn");
}
static void HavocApplyModifiers() {
  LONG gen = g_hvcModGen;
  for (FeatsByPawn &e : g_featsByPawn) {
    if (!HavocLiveShip(e))
      continue;
    uint8_t *ctrl = IsReadable((uint8_t *)e.pawn + OFF_PAWN_CONTROLLER, 8) ? *(uint8_t **)((uint8_t *)e.pawn + OFF_PAWN_CONTROLLER) : nullptr;
    if (!ctrl)
      continue; // not possessed yet: its side is not known
    bool player = HavocPawnIsPlayer(e.pawn);
    if (e.modGen != gen) {
      e.modGen = gen;
      for (int s = 0; s < 2; ++s) {
        e.modsOn[s] = -1;
        int m = g_hvcActiveMods[s];
        if (m < 0)
          continue;
        char t = kHavocModifiers[m].team;
        if (t == 'A' || (t == 'P' && player) || (t == 'E' && !player)) {
          HavocForEachFeat(kHavocModifiers[m].feats, e.comp, true, "wave modifier", e.pawn);
          e.modsOn[s] = m;
        }
      }
    }
    if (!player)
      for (; e.permApplied < kHavocEnemyBoostCount && kHavocEnemyBoosts[e.permApplied].wave <= g_hvcModWave; ++e.permApplied)
        HavocForEachFeat(kHavocEnemyBoosts[e.permApplied].feat, e.comp, true, "permanent enemy modifier", e.pawn);
  }
}
// At a wave's end the wave modifiers come off every ship that has them.
static void HavocEndWaveModifiers() {
  for (FeatsByPawn &e : g_featsByPawn) {
    if (!HavocLiveShip(e)) {
      e.modsOn[0] = e.modsOn[1] = -1;
      continue;
    }
    for (int s = 0; s < 2; ++s)
      if (e.modsOn[s] >= 0) {
        HavocForEachFeat(kHavocModifiers[e.modsOn[s]].feats, e.comp, false, "wave modifier", e.pawn);
        e.modsOn[s] = -1;
      }
  }
  g_hvcActiveMods[0] = g_hvcActiveMods[1] = -1;
  InterlockedIncrement(&g_hvcModGen);
}

// ---------------------------------------------------------------------------
// Havoc game over (stage 2). PlayersWin / PlayersLose are compiled out (empty
// thunk 0x74C640). PVE_HordeManager_BP calls PlayersWin 4 s after the last
// wave of Waves when the map has no WaveToLoopBackTo; Havoc maps loop
// (SetWaveIndex back to that wave), so a run ends only when it is lost.
// Nothing in the cooked BPs calls PlayersLose: the rule was server code.
// YHavocHUDTeamInfo carries m_eliminated per player, and the team refills
// respawns only after a wave (m_refillPlayerTeamRespawnsAfterWave = 1,
// m_respawnsToRefill = 0): a ship lost during a wave is out until it ends.
// GUESS: the run is lost when no connected player has a living ship for 3 s
// during a wave. Respawns during a wave are NOT blocked yet (the host's
// respawn request is not traced), so in a group a destroyed player can come
// back early; solo, the rule is exact.
//
// At game over the director: EnterNewState(GameOver) (GameState_Havoc_BP plays
// its outro), reports each player's run to mmogbrain (GET /battle/havoc: the
// run's score is the team's total XP, m_totalXP -- YEOMHavocProgressionData
// m_teamTotalXP), sends the answer to the player's client --
// AYPlayerReplicationInfo_Havoc::ClientUpdateHighscore(lifetimePlayerScore,
// weeklyPlayerScore, matchScore) and ClientUpdateWeeklyScoreResetTime -- which
// the end-of-match screen reads (+0xA4C / +0xA48 / +0xA44 / +0xA58, 0x449470;
// it waits while +0xA44 is -1), marks the player's Havoc EOM data finalized
// (m_isInEndOfMatchDataFinalized, the byte AYPlayerController_Havoc::
// IsInEndOfMatchDataFinalized 0x401D20 checks), and ends the match 6 s later
// through EndMatch (vtable +0x668) with the enemy team as the winner.

static int64_t HavocField(const char *body, const char *key) {
  char k[32];
  _snprintf_s(k, sizeof k, _TRUNCATE, "%s=", key);
  for (const char *p = body; p && *p;) {
    if (!strncmp(p, k, strlen(k)))
      return _atoi64(p + strlen(k));
    p = strchr(p, '\n');
    if (p)
      ++p;
  }
  return -1;
}
struct HavocHighscore { int32_t lifetime, weekly, match; int64_t reset; };
static void FillHighscore(uint8_t *parms, const char *pc, const char *pn, uint32_t off, void *ctx) {
  HavocHighscore *h = (HavocHighscore *)ctx;
  if (!strcmp(pn, "lifetimePlayerScore"))
    *(int32_t *)(parms + off) = h->lifetime;
  else if (!strcmp(pn, "weeklyPlayerScore"))
    *(int32_t *)(parms + off) = h->weekly;
  else if (!strcmp(pn, "matchScore"))
    *(int32_t *)(parms + off) = h->match;
  else if (!strcmp(pn, "weeklyScoreResetTime")) {
    if (!strcmp(pc, "Int64Property"))
      *(int64_t *)(parms + off) = h->reset;
    else
      *(int32_t *)(parms + off) = (int32_t)h->reset;
  }
}

static void HavocReportRuns(uint8_t *gs) {
  int score = *(int32_t *)(gs + OFF_HVC_GS_TOTAL_XP);
  int waves = *(int32_t *)(gs + OFF_HVC_GS_WAVES_REACHED);
  char match[96];
  if (!MatchIDFromCommandLine(match, sizeof match)) {
    Logf("havoc: game over -- no -MatchID= on the command line; runs not reported");
    return;
  }
  uint8_t *pcs[16];
  int npc = HavocHumanPCs(gs, pcs, 16);
  for (int i = 0; i < npc; ++i) {
    uint8_t *pri = *(uint8_t **)(pcs[i] + OFF_PC_PLAYER_STATE);
    char pid[80];
    if (!PlayerPIDForController(pcs[i], pid, sizeof pid) && !RememberedControllerPID(pcs[i], pid, sizeof pid)) {
      Logf("havoc: game over -- controller %p has no DNPID; run not reported", pcs[i]);
      continue;
    }
    char path[512], body[1024];
    _snprintf_s(path, sizeof path, _TRUNCATE, "/battle/havoc?match=%s&pid=%s&score=%d&waves=%d", match, pid, score, waves);
    bool ok = HttpGetLoopback(path, body, sizeof body);
    HavocHighscore h = {0, 0, score, 0};
    if (ok) {
      int64_t v;
      if ((v = HavocField(body, "lifetime")) >= 0) h.lifetime = (int32_t)v;
      if ((v = HavocField(body, "weekly")) >= 0) h.weekly = (int32_t)v;
      if ((v = HavocField(body, "reset")) >= 0) h.reset = v;
    }
    bool sentScore = CallNamed(pri, L"ClientUpdateHighscore", FillHighscore, &h);
    bool sentReset = CallNamed(pri, L"ClientUpdateWeeklyScoreResetTime", FillHighscore, &h);
    int fin = PropOffset((UObjectMin *)pri, "m_isInEndOfMatchDataFinalized");
    if (fin > 0 && IsReadable(pri + fin, 1))
      pri[fin] = 1;
    for (char *c = body; *c; ++c)
      if (*c == '\n') *c = ' ';
    Logf("havoc: game over -- %s score %d waves %d -> %s%s; highscore sent %d, reset sent %d, EOM finalized byte +0x%X",
         pid, score, waves, ok ? "mmogbrain: " : "mmogbrain UNREACHABLE", ok ? body : "", sentScore, sentReset, fin);
  }
}

// Living ships of connected players; -1 when there is no connected player or
// one of them has not had a living ship yet this run (still choosing one, or
// in the orbit intro) -- the run cannot be lost before everyone is in.
static void *g_hvcSeenAlive[16];
static int HavocPlayersAlive(uint8_t *gs, int *humansOut) {
  uint8_t *pcs[16];
  int npc = HavocHumanPCs(gs, pcs, 16), alive = 0;
  bool allSeen = true;
  *humansOut = npc;
  for (int i = 0; i < npc; ++i) {
    uint8_t *pawn = IsReadable(pcs[i] + OFF_CONTROLLER_PAWN, 8) ? *(uint8_t **)(pcs[i] + OFF_CONTROLLER_PAWN) : nullptr;
    bool up = pawn && IsReadable(pawn + OFF_PAWN_STATE, 1) && pawn[OFF_PAWN_STATE] != 2 &&
              pawn[OFF_PAWN_STATE] != 3 && ClassChainContains((UObjectMin *)pawn, "YPawn");
    int slot = -1, freeSlot = -1;
    for (int k = 0; k < 16; ++k) {
      if (g_hvcSeenAlive[k] == pcs[i])
        slot = k;
      else if (!g_hvcSeenAlive[k] && freeSlot < 0)
        freeSlot = k;
    }
    if (up) {
      ++alive;
      if (slot < 0 && freeSlot >= 0)
        g_hvcSeenAlive[freeSlot] = pcs[i];
    } else if (slot < 0)
      allSeen = false;
  }
  return npc && allSeen ? alive : -1;
}

// ---------------------------------------------------------------------------
// Havoc respawn blocking (stage 3; dn_host_no_havoc_respawn_block.txt turns it
// off). A ship lost during a wave stays out until the wave ends:
// YHavocHUDTeamInfo m_eliminated, m_refillPlayerTeamRespawnsAfterWave 1 with
// m_respawnsToRefill 0. The gate: AYPlayerController::SpawnPlayer (0x5998A0)
// first asks the game mode (vtable +0x870 -- on GameMode_Havoc_BP 0x368380,
// read live 2026-10-19: allowed while the player's respawn count is under
// gm+0x6A4, 0 = unlimited) and returns WITHOUT spawning, leaving the pending
// flag PC +0xB88 set, when it says no. Nothing retries, so at the wave's end
// the director calls SpawnPlayer for every eliminated player still pending.
// GUESS: a player is eliminated when their ship is destroyed while a wave is
// in progress; one destroyed between waves respawns at once.
#define RVA_GM_SPAWN_GATE 0x368380
typedef uint32_t(__fastcall *tSpawnGate)(void *gm, void *pc);
static tSpawnGate g_origSpawnGate = nullptr;
static void *volatile g_hvcEliminated[16];

static bool HavocRespawnBlockOn() {
  static int s_on = -1;
  if (s_on < 0)
    s_on = SwitchOn("DN_HOST_NO_HAVOC_RESPAWN_BLOCK", "dn_host_no_havoc_respawn_block.txt") ? 0 : 1;
  return s_on == 1;
}
static bool HavocIsEliminated(void *pc) {
  for (void *e : g_hvcEliminated)
    if (e && e == pc)
      return true;
  return false;
}

static uint32_t __fastcall HookSpawnGate(void *gm, void *pc) {
  if (pc && HavocIsEliminated(pc)) {
    static void *s_lastLogged = nullptr;
    if (s_lastLogged != pc) {
      s_lastLogged = pc;
      Logf("havoc: respawn of %p held -- eliminated until the wave ends", pc);
    }
    return 0;
  }
  return g_origSpawnGate(gm, pc);
}

// During a wave: a player seen with a living ship who now has none is out.
static void HavocTrackEliminations(uint8_t *gs) {
  if (!HavocRespawnBlockOn())
    return;
  uint8_t *pcs[16];
  int npc = HavocHumanPCs(gs, pcs, 16);
  for (int i = 0; i < npc; ++i) {
    uint8_t *pawn = IsReadable(pcs[i] + OFF_CONTROLLER_PAWN, 8) ? *(uint8_t **)(pcs[i] + OFF_CONTROLLER_PAWN) : nullptr;
    bool up = pawn && IsReadable(pawn + OFF_PAWN_STATE, 1) && pawn[OFF_PAWN_STATE] != 2 && pawn[OFF_PAWN_STATE] != 3;
    bool seen = false;
    for (void *s : g_hvcSeenAlive)
      seen |= s == pcs[i];
    if (up || !seen || HavocIsEliminated(pcs[i]))
      continue;
    for (auto &e : g_hvcEliminated)
      if (!e) {
        e = pcs[i];
        Logf("havoc: %p eliminated -- ship lost during the wave, no respawn until it ends", pcs[i]);
        break;
      }
  }
}

// At the wave's end: everyone is back in; held spawns go through now.
static void HavocReleaseEliminated() {
  for (auto &e : g_hvcEliminated) {
    void *pc = e;
    e = nullptr;
    if (!pc || !IsReadable((uint8_t *)pc + OFF_PC_SPAWN_PENDING, 1))
      continue;
    uint8_t *p = (uint8_t *)pc;
    void *pawn = IsReadable(p + OFF_CONTROLLER_PAWN, 8) ? *(void **)(p + OFF_CONTROLLER_PAWN) : nullptr;
    bool pending = p[OFF_PC_SPAWN_PENDING] != 0;
    if (pending) {
      __try {
        ((tSpawnPlayer)(g_base + RVA_SPAWN_PLAYER))(pc);
      } __except (EXCEPTION_EXECUTE_HANDLER) {
        Logf("havoc: EXCEPTION releasing the held respawn of %p", pc);
      }
    }
    void *now = IsReadable(p + OFF_CONTROLLER_PAWN, 8) ? *(void **)(p + OFF_CONTROLLER_PAWN) : nullptr;
    Logf("havoc: %p back in -- %s (ship %p -> %p)", pc,
         pending ? "held respawn released" : "no respawn was requested yet", pawn, now);
  }
}

// ---------------------------------------------------------------------------
// Team Elimination rounds (on; dn_host_no_te_rounds.txt / DN_HOST_NO_TE_ROUNDS=1
// turns it off). Added 2026-10-25.
//
// The rules, from the game's own text (en DreadGame.locres): "Team Elimination
// is played in rounds. The first team to win 2 rounds wins the match." and "The
// team that won the previous round keeps the same ships." A destroyed ship is
// out until the next round; its captain flies a fighter meanwhile -- the game
// state's m_respawnFighterLoadoutFilter is the jets filter
// (GameState_TE_BP: VH_LoadoutFilter_Jets_DA), and the 2015 previews describe
// the same.
//
// Why the mod has to run them (verified in the exe, 2026-10-25):
//   - YGameMode_TeamElimination (ctor 0x362790, vtable 0x2E9FBE8, parent TDM
//     0x362560) overrides NOTHING it inherits but the destructor; its 29 new
//     virtuals (+0x9D8..+0xAB8) are stubs and getters of at most 120 bytes,
//     and nothing calls them. Its settings m_numberOfRoundsToWin +0xA50,
//     m_maxRoundTime +0xA54, m_postRoundInitialDelay +0xA58,
//     m_postRoundDuration +0xA5C, m_preRoundStartDuration +0xA60 (registration
//     offsets) are read by no code; GameInfo_TE_BP sets only m_maxRoundTime 11.
//     So a TE match was a 20-minute deathmatch (measured: 41 matches averaging
//     9.9 min, the long ones ending at ~22).
//   - YGameState_TE (ctor 0x389300, vtable 0x2EC2F08) and the client side ARE
//     here: m_rounds +0x1E28 (TArray of 0x18-byte rounds: +0 number, +4 winner
//     team, +8 an array -- read by 0x3A08C0 and 0x369BB0),
//     m_IsPerformingRoundTransition +0x1E90 (0x3A2440 reads it),
//     m_respawnFighterLoadoutFilter +0x1E98, and the controller RPCs (impls on
//     the PC vtable 0x2EF6670; CORRECTED 2026-10-26, first read one slot off
//     from 0x2EF6678): ClientPostRoundEnd (+0xF68, 0x573B40, no parameters:
//     clears the loadout filter, shows the last round's result from m_rounds
//     against the player's team), ClientPreRoundStart(preRoundStartDuration)
//     (+0xF60, 0x573CD0, timer at pc+0xC3C), ClientNewRoundStarts(roundNumber,
//     serverLateJoinState) (+0xF58, 0x573970: respawn step unless state 2),
//     ClientRespawnTeamElimination(respawnTime) (+0xF78, 0x573E10).
//   - The fighter switch is native: SetLoadoutsFilter 0x35B090 (manager,
//     filter) adds the filter's loadouts and activates the first allowed one
//     when the active loadout is not; a null filter clears it (0x33CF50). The
//     game state applies TE's jets filter itself only to late joiners
//     (0x3A2440, vtable +0x798, from AYPlayerController::ReadyForJoining).
//   - New round: AYPlayerController::ReplaceShipWithActiveLoadoutForNewRound,
//     body 0x590B80, puts the player back in a ship of their active loadout.
//
// What runs here, on the game-mode timer: a round ends when one team has no
// capital ship left (EYShipClass 1..15 in the global ship list 0x3E0E848;
// respawn jets are 16+), or at the round time limit; the round is recorded in
// m_rounds, the clients get ClientPostRoundEnd, then ClientPreRoundStart, then
// everyone is put back in a ship (bots refill through the spawner) and
// ClientNewRoundStarts; at 2 round wins the winner is set and the match ends.
// A human whose capital ship is destroyed during a round gets the jets filter.
//
// GUESSES, marked where used: m_maxRoundTime in minutes (as m_maxMatchTime 20
// is); round timeout winner = more capital ships left, equal = draw; the post-
// and pre-round durations (8 s, 10 s) when the game mode's are 0; surviving bots
// stay where they are; RPC parameter meanings (times in seconds).
// NOT verified against a live client.
#define OFF_GS_TE_ROUNDS 0x1E28
#define TE_ROUND_SIZE 0x18
#define OFF_GS_TE_TRANSITION 0x1E90
#define OFF_GS_TE_FIGHTER_FILTER 0x1E98
#define OFF_GM_TE_ROUNDS_TO_WIN 0xA50
#define OFF_GM_TE_MAX_ROUND_TIME 0xA54
#define OFF_GM_TE_POST_ROUND 0xA5C
#define OFF_GM_TE_PRE_ROUND 0xA60
#define RVA_SET_LOADOUTS_FILTER 0x35B090
#define RVA_REPLACE_SHIP_NEW_ROUND 0x590B80
#define RVA_SHIP_LIST 0x3E0E848      // TArray<YPawn*> of every ship (count at +8)
#define RVA_FMEMORY_FREE 0xBFC9C0
#define TE_ROUNDS_TO_WIN_TEXT 2      // "The first team to win 2 rounds wins the match."
typedef void(__fastcall *tSetLoadoutsFilter)(void *mgr, void *filter);
typedef void(__fastcall *tPcVoid)(void *pc);
typedef void(__fastcall *tFMemoryFree)(void *p);

enum { TE_WAIT, TE_ROUND, TE_POST, TE_PRE, TE_DONE };
struct TeElim {
  void *pc;
  void *capital; // the active loadout when the capital ship was lost
  int cls;       // its ship class
};
struct TeKeep {
  void *pc;
  void *loadout; // the round winner's ship, kept for the next round
};
struct TeHudRow {
  void *pri;
  int cls;  // the last capital class seen
  bool had; // had a capital ship this round
};
struct TeMatch {
  void *gm;
  int phase, round, wins[2], rule;
  DWORD phaseAt, botsOffAt;
  bool seen[2]; // each team had a capital ship this round
  TeElim elim[16];
  TeKeep keep[16];
  void *ready[16]; // humans who readied up between rounds
  int numReady;
  TeHudRow hud[32];
  int lastWinner;
};
static TeMatch g_te = {};

static bool TeRoundsOn() {
  static int s_on = -1;
  if (s_on < 0)
    s_on = SwitchOn("DN_HOST_NO_TE_ROUNDS", "dn_host_no_te_rounds.txt") ? 0 : 1;
  return s_on == 1;
}

static uint8_t *TeGameState(uint8_t *gm) {
  if (!gm || !IsReadable(gm + OFF_GM_GAMESTATE, 8))
    return nullptr;
  uint8_t *gs = *(uint8_t **)(gm + OFF_GM_GAMESTATE);
  if (!gs || !IsReadable(gs + OFF_GS_GAME_MODE_TYPE, 1) || gs[OFF_GS_GAME_MODE_TYPE] != YGMT_TEAM_ELIMINATION ||
      !IsReadable(gs + OFF_GS_TE_ROUNDS, OFF_GS_TE_FIGHTER_FILTER + 8 - OFF_GS_TE_ROUNDS))
    return nullptr;
  return gs;
}

// Capital ships alive per team, from the global ship list.
static void TeCountShips(int alive[2]) {
  alive[0] = alive[1] = 0;
  uint8_t *base = (uint8_t *)g_base;
  if (!IsReadable(base + RVA_SHIP_LIST, 16))
    return;
  void **arr = *(void ***)(base + RVA_SHIP_LIST);
  int n = *(int32_t *)(base + RVA_SHIP_LIST + 8);
  if (n <= 0 || n > 512 || !IsReadable(arr, (size_t)n * 8))
    return;
  for (int i = 0; i < n; ++i) {
    uint8_t *p = (uint8_t *)arr[i];
    if (!p || !IsReadable(p + OFF_SHIP_TEAM, 1) || !IsReadable(p + OFF_PAWN_STATE, 1))
      continue;
    int cls = p[OFF_PAWN_SHIP_CLASS], team = ShipTeam(p);
    if (cls < 1 || cls > 15 || !team || p[OFF_PAWN_STATE] == 2 || p[OFF_PAWN_STATE] == 3)
      continue;
    alive[team - 1]++;
  }
}

// Connected human controllers, from the game state's PlayerArray.
static int TeHumanPCs(uint8_t *gs, uint8_t **out, int max) {
  if (!IsReadable(gs + OFF_GS_PLAYER_ARRAY, 16))
    return 0;
  uint8_t **arr = *(uint8_t ***)(gs + OFF_GS_PLAYER_ARRAY);
  int count = *(int32_t *)(gs + OFF_GS_PLAYER_ARRAY + 8);
  if (count < 0 || count > 256 || (count && !IsReadable(arr, (size_t)count * 8)))
    return 0;
  int n = 0;
  for (int i = 0; i < count && n < max; ++i) {
    uint8_t *pri = arr[i];
    if (!IsReadable(pri, OFF_ACTOR_OWNER + 8))
      continue;
    uint8_t *pc = *(uint8_t **)(pri + OFF_ACTOR_OWNER);
    if (!IsReadable(pc, OFF_PC_NETCONNECTION + 8) || !ClassChainContains((UObjectMin *)pc, "PlayerController") ||
        !*(void **)(pc + OFF_PC_NETCONNECTION) || *(uint8_t **)(pc + OFF_PC_PLAYER_STATE) != pri)
      continue;
    out[n++] = pc;
  }
  return n;
}

// RPC parameters by type, in order; the names are logged once per function.
struct TeRpcArgs {
  const wchar_t *fn;
  int32_t ints[2];
  int ni;
  bool log;
};
// Logs a function's parameters the first time it is sent.
static bool TeFirstSend(const wchar_t *fn) {
  static const wchar_t *s_logged[8];
  for (const wchar_t *&f : s_logged) {
    if (f == fn)
      return false;
    if (!f) {
      f = fn;
      return true;
    }
  }
  return false;
}
static void FillTeRpc(uint8_t *parms, const char *pc, const char *pn, uint32_t off, void *ctx) {
  TeRpcArgs *a = (TeRpcArgs *)ctx;
  if (a->log)
    Logf("te: %ls parameter %s %s at +%u", a->fn, pc, pn, off);
  int32_t v = a->ni < 2 ? a->ints[a->ni] : 0;
  a->ni++;
  if (!strcmp(pc, "IntProperty"))
    *(int32_t *)(parms + off) = v;
  else if (!strcmp(pc, "FloatProperty"))
    *(float *)(parms + off) = (float)v;
  else if (!strcmp(pc, "ByteProperty") || !strcmp(pc, "BoolProperty"))
    parms[off] = (uint8_t)v;
}
static void TeSendAll(uint8_t *gs, const wchar_t *fn, int a0, int a1) {
  uint8_t *pcs[16];
  int n = TeHumanPCs(gs, pcs, 16), ok = 0;
  for (int i = 0; i < n; ++i) {
    TeRpcArgs a = {fn, {a0, a1}, 0, i == 0 && TeFirstSend(fn)};
    ok += CallNamed(pcs[i], fn, FillTeRpc, &a) ? 1 : 0;
  }
  Logf("te: %ls(%d, %d) -> %d of %d players", fn, a0, a1, ok, n);
}

static void *TeLoadoutManager(void *pc) { return HavocLoadoutManager(pc); }

// m_rounds holds the round being PLAYED as its last entry, winner 0 until it
// ends. The game's Predator scorer (0x3EFF60, "kill the whole enemy team by
// yourself", a TE-only row) reads the last entry on every kill with no empty
// check: its +8 array (16-byte kill records, matched against the killer) is
// counted against the enemy team's size (GS +0x4F8/+0x4FC). With m_rounds
// empty it reads entry -1, 8 bytes before a null pointer -- the host crash
// "EXCEPTION_ACCESS_VIOLATION reading address 0xfffffff8" at the first
// capital-ship kill of every TE match with a scoring table (7 hosts on
// 2026-09-28/29, 2 more on 2026-10-26, each right after CaptainKill/FinalBlow).
// Every other reader checks the count (0x57CD40, 0x3A08C0, 0x369BB0). The
// kill records are not written here, so Predator never fires (GUESS: the
// stripped game mode added them).
// Appends a round entry (FMemory, as the engine's own arrays).
static bool TeAppendRound(uint8_t *gs, int number, int winner) {
  uint8_t **data = (uint8_t **)(gs + OFF_GS_TE_ROUNDS);
  int32_t *num = (int32_t *)(gs + OFF_GS_TE_ROUNDS + 8), *max = num + 1;
  int n = *num;
  if (n < 0 || n > 32)
    return false;
  uint8_t *nb = (uint8_t *)((tFMemoryMalloc)(g_base + RVA_FMEMORY_MALLOC))((size_t)(n + 1) * TE_ROUND_SIZE, 0);
  if (!nb)
    return false;
  memset(nb, 0, (size_t)(n + 1) * TE_ROUND_SIZE);
  if (n && *data && IsReadable(*data, (size_t)n * TE_ROUND_SIZE))
    memcpy(nb, *data, (size_t)n * TE_ROUND_SIZE);
  uint8_t *e = nb + (size_t)n * TE_ROUND_SIZE;
  *(int32_t *)e = number;
  e[4] = (uint8_t)winner;
  uint8_t *old = *data;
  *data = nb;
  *num = n + 1;
  *max = n + 1;
  if (old)
    ((tFMemoryFree)(g_base + RVA_FMEMORY_FREE))(old);
  return true;
}

static uint8_t *TeLastRound(uint8_t *gs) {
  uint8_t *data = *(uint8_t **)(gs + OFF_GS_TE_ROUNDS);
  int n = *(int32_t *)(gs + OFF_GS_TE_ROUNDS + 8);
  if (n < 1 || n > 33 || !data || !IsReadable(data, (size_t)n * TE_ROUND_SIZE))
    return nullptr;
  return data + (size_t)(n - 1) * TE_ROUND_SIZE;
}

// Makes round `number` the open last entry of m_rounds.
static void TeOpenRound(uint8_t *gs, int number) {
  uint8_t *last = TeLastRound(gs);
  if (last && *(int32_t *)last == number)
    return;
  bool ok = TeAppendRound(gs, number, 0);
  Logf("te: round %d entry opened in m_rounds (%d entries) -- the game's Predator scorer reads it on every kill%s",
       number, *(int32_t *)(gs + OFF_GS_TE_ROUNDS + 8), ok ? "" : " -- FAILED");
}

// Writes the winner into the round's entry (appends it if it was not open).
static void TeRecordRound(uint8_t *gs, int number, int winner) {
  uint8_t *last = TeLastRound(gs);
  if (last && *(int32_t *)last == number)
    last[4] = (uint8_t)winner;
  else
    TeAppendRound(gs, number, winner);
}

static void TeForceNetUpdate(uint8_t *actor) {
  void **vt = *(void ***)actor;
  ((tActorVoidFn)vt[0x580 / 8])(actor); // AActor::ForceNetUpdate
}

static int TeSeconds(uint8_t *gm, int off, int def) {
  float v = IsReadable(gm + off, 4) ? *(float *)(gm + off) : 0.f;
  return v > 0.5f && v < 120.f ? (int)v : def;
}
static int TeRoundTimeSecs(uint8_t *gm) {
  // GUESS: minutes, as GameInfo_TE_BP's m_maxMatchTime 20 is (TE hosts end at ~20 min).
  float v = IsReadable(gm + OFF_GM_TE_MAX_ROUND_TIME, 4) ? *(float *)(gm + OFF_GM_TE_MAX_ROUND_TIME) : 0.f;
  return v > 0.5f && v < 60.f ? (int)(v * 60.f) : 11 * 60;
}

// A human's capital ship was destroyed during a round: fighters until the next.
static void TeOnShipKilled(void *gameMode, void *victimCtrl, int victimClass) {
  if (!TeRoundsOn() || g_te.gm != gameMode || g_te.phase != TE_ROUND || victimClass < 1 || victimClass > 15)
    return;
  uint8_t *gs = TeGameState((uint8_t *)gameMode);
  uint8_t *pc = (uint8_t *)victimCtrl;
  if (!gs || !pc || !IsReadable(pc + OFF_PC_NETCONNECTION, 8) || !ClassChainContains((UObjectMin *)pc, "PlayerController") ||
      !*(void **)(pc + OFF_PC_NETCONNECTION))
    return;
  uint8_t *mgr = (uint8_t *)TeLoadoutManager(pc);
  void *filter = *(void **)(gs + OFF_GS_TE_FIGHTER_FILTER);
  if (!mgr || !filter || !IsReadable(mgr + OFF_LOADOUT_MGR_ACTIVE, 8))
    return;
  TeElim *slot = nullptr;
  for (auto &e : g_te.elim)
    if (e.pc == pc || (!slot && !e.pc))
      slot = &e;
  if (slot && slot->pc != pc) {
    slot->pc = pc;
    slot->capital = *(void **)(mgr + OFF_LOADOUT_MGR_ACTIVE);
    slot->cls = victimClass;
  }
  ((tSetLoadoutsFilter)(g_base + RVA_SET_LOADOUTS_FILTER))(mgr, filter);
  TeRpcArgs a = {L"ClientRespawnTeamElimination", {0, 0}, 0, TeFirstSend(L"ClientRespawnTeamElimination")};
  bool sent = CallNamed(pc, a.fn, FillTeRpc, &a); // GUESS: the original sent it here
  Logf("te: round %d -- %p lost its ship (class %d): fighters until the next round (filter %p%s)", g_te.round, pc,
       victimClass, filter, sent ? ", ClientRespawnTeamElimination sent" : "");
}

static void TeRestoreShips(uint8_t *gs) {
  for (auto &e : g_te.elim) {
    if (!e.pc)
      continue;
    uint8_t *mgr = (uint8_t *)TeLoadoutManager(e.pc);
    if (mgr && IsReadable(mgr + OFF_LOADOUT_MGR_ACTIVE, 8)) {
      ((tSetLoadoutsFilter)(g_base + RVA_SET_LOADOUTS_FILTER))(mgr, nullptr);
      if (e.capital && *(void **)(mgr + OFF_LOADOUT_MGR_ACTIVE) != e.capital)
        ((tActivateLoadout)(g_base + RVA_ACTIVATE_LOADOUT))(mgr, e.capital, 1);
    }
    e.pc = nullptr;
    e.capital = nullptr;
  }
  (void)gs;
}

static void TeResetRoundState(uint8_t *gs);
static bool TeSidesSwapped();
static void TeSendTeamInfo(uint8_t *gs, bool force);
static void TeStartRound(uint8_t *gm, uint8_t *gs, DWORD now) {
  g_te.round++;
  TeOpenRound(gs, g_te.round);
  g_te.phase = TE_ROUND;
  g_te.phaseAt = now;
  g_te.seen[0] = g_te.seen[1] = false;
  gs[OFF_GS_TE_TRANSITION] = 0;
  TeResetRoundState(gs);
  TeForceNetUpdate(gs);
  // (roundNumber, serverLateJoinState) -- the parameter names the first live
  // match logged (2026-10-25). The client impl (PC vtable 0x2EF6670 +0xF58 =
  // 0x573970) skips its respawn step when the late-join state is 2; 0 here.
  TeSendAll(gs, L"ClientNewRoundStarts", g_te.round, 0);
  Logf("te: round %d started (score %d-%d, %d s limit, sides %s)", g_te.round, g_te.wins[0], g_te.wins[1],
       TeRoundTimeSecs(gm), TeSidesSwapped() ? "swapped" : "as placed");
  TeSendTeamInfo(gs, true);
}

// ---------------------------------------------------------------------------
// Team Elimination: sides, fighter spawns, the winners' ships, ready-up and the
// team HUD (2026-10-29).
//
// The rules (Grey Box, "Dreadnought Patch Notes v1.1.0", 2017): "Winning team
// of a round cannot change loadouts for next round", "Enemy ship classes are
// set to unknown until you see them or a teammate calls them out", "Fighter
// Jets respawn closer to the action", "Spawn locations are swapped between
// rounds", "Players can ready-up between rounds to spawn faster".
//
// Verified in the exe and maps 2026-10-29:
//   - YGameMode_TeamElimination has the switches for three of them,
//     m_swapTeamsOnNewRound and m_disableShipSelectionForWinningTeam (bools,
//     registered beside m_numberOfRoundsToWin), and nothing reads them. Its
//     ctor (0x362790) sets only five empty FStrings (+0xA70..+0xA90, the
//     "enemies remaining" voice templates -- empty, so there is nothing to
//     play) and GameInfo_TE_BP only m_maxRoundTime 11, m_maxMatchTime 20,
//     m_timeSinceDamageToShowLastTarget 30. So every TE setting reads 0 here.
//   - Sides: every TE map's persistent level has 8 + 8 team capital starts
//     tagged YGMF_TE (YPlayerStartTE_Team1/2) and 25..47 team-less YGMF_TE
//     starts with m_onlyFighterJets (+0x425; Spawn_JetFighter). The new-round
//     respawn the mod used, ReplaceShipWithActiveLoadoutForNewRound (0x590B80),
//     calls SpawnNearActor(pc, active loadout id (+0xB0), pc, 0) (0x599470):
//     near the controller's OWN position, wherever the player last was -- no
//     sides at all from round 2 on. The server build chose the actor.
//   - Fighter starts: the player-start chooser 0x36C1A0 skips every start with
//     +0x425 set, so nothing in the exe ever uses the jet starts; and it takes
//     the TDM / Conquest starts too (the mode check is the return-true stub).
//   - Ready-up: ServerReadyUpPlayerForNextWave / ServerUnReadyPlayerForNextWave
//     (exec thunks 0x772650 / 0x774B40) end in `ret` stubs (PC vtable +0x1070 /
//     +0x1060): the server half is gone.
//   - The team HUD: AYGameState_TE::ClientTeamEliminationPlayersChanged
//     (NetMulticast, thunk 0x7475A0 -> GS vtable +0x830 = 0x390A60) takes a
//     YTeamEliminationHUDTeamInfo of parallel arrays -- +0x08 m_playerID
//     (FString), +0x18 m_classID (int32), +0x28 m_eliminated (uint8), +0x38
//     m_spotted (uint8), +0x48 m_teamID (int32) -- and hands them to the HUD's
//     team list (0x55B720 -> 0x4990C0), then marks each eliminated id
//     (0x559010 -> 0x492270, ids compared case-insensitively; any unique string
//     works). Nothing sends it. Spotting itself is native and live: the client's
//     ServerSetPawnToSpotted (impl 0x592BB0) sets YPlayerReplicationInfo
//     m_isSpotted (+0x94A, 0x5C2960) or the NPC one (+0x669, 0x2E21F0).
//
// So, on TE hosts:
//   - new rounds put each human at a capital start of their side (the i-th
//     player of a team at its i-th start, by name), sides swapped every other
//     round; the old controller-position respawn is the fallback;
//   - the chooser admits only YGMF_ALL / YGMF_TE starts (sides swapped as
//     above), and for an eliminated player only the jet starts;
//   - a human on the round's winning team gets the same ship back for the next
//     round even if they picked another in the pre-round;
//   - when every human has readied up in the pre-round, the round starts;
//   - the team list goes to the clients whenever it changes: every player's
//     class, team, spotted flag and whether they lost their capital ship this
//     round; m_isSpotted is cleared for everyone at each round start.
// GUESSES: the sides alternate every round (the notes say only "swapped
// between rounds"); the chooser's own rating decides which jet start is
// "closer to the action"; the ready-up RPC is the one the TE pre-round sends
// (logged when it arrives); the winner's ship is restored on the server rather
// than locked in the client's menu; clearing m_isSpotted per round; the HUD ids
// are the player states' addresses.
#define RVA_PC_SPAWN_NEAR_ACTOR 0x599470      // (pc, const loadout id*, AActor* near, bool)
#define RVA_PC_AFTER_NEW_ROUND_SPAWN 0x5953A0 // what 0x590B80 tail-calls after it
#define OFF_LOADOUT_ID 0xB0
#define OFF_PRI_SPOTTED 0x94A
#define OFF_PRI_SHIP_CLASS 0x941
typedef void(__fastcall *tSpawnNearActor)(void *pc, void *loadoutId, void *nearActor, uint8_t flag);
static bool TeIsEliminated(void *pc);

static bool TeSpawnsEnabled() {
  static int s_on = -1;
  if (s_on < 0)
    s_on = SwitchOn("DN_HOST_NO_TE_SPAWNS", "dn_host_no_te_spawns.txt") ? 0 : 1;
  return s_on == 1;
}
static bool TeWinnerLockOn() {
  static int s_on = -1;
  if (s_on < 0)
    s_on = SwitchOn("DN_HOST_NO_TE_WINNER_LOCK", "dn_host_no_te_winner_lock.txt") ? 0 : 1;
  return s_on == 1;
}
static bool TeHudOn() {
  static int s_on = -1;
  if (s_on < 0)
    s_on = SwitchOn("DN_HOST_NO_TE_HUD", "dn_host_no_te_hud.txt") ? 0 : 1;
  return s_on == 1;
}

static bool IsTeamEliminationMode(void *gm) {
  static void *s_gm = nullptr;
  static bool s_te = false;
  if (gm != s_gm) {
    s_gm = gm;
    s_te = gm && ClassChainContains((UObjectMin *)gm, "YGameMode_TeamElimination");
  }
  return s_te && TeSpawnsEnabled();
}

// The round whose sides apply now (in the pre-round, the next one).
static int TeSideRound() { return g_te.phase == TE_PRE ? g_te.round + 1 : (g_te.round > 0 ? g_te.round : 1); }
static bool TeSidesSwapped() { return TeRoundsOn() && g_te.gm && TeSideRound() % 2 == 0; }

static const char *PrepareTeStarts(void *ctrl, int *hidden, int *ours) {
  static uint8_t *s_all[1024];
  int n = AllPlayerStarts(s_all, 1024);
  *hidden = *ours = 0;
  bool jets = TeRoundsOn() && g_te.phase == TE_ROUND && TeIsEliminated(ctrl);
  bool swap = !jets && TeSidesSwapped();
  for (int i = 0; i < n; ++i) {
    uint8_t *ps = s_all[i];
    uint8_t mode = ps[OFF_PS_GAME_MODE], team = ps[OFF_PS_TEAM];
    bool jetStart = ps[OFF_PS_ONLY_JETS] != 0;
    if (jets) {
      if (mode == YGMF_TE && jetStart) {
        if (ps[OFF_PS_ACTIVE]) {
          PatchStart(ps, OFF_PS_ONLY_JETS, 0);
          ++*ours;
        }
      } else if (ps[OFF_PS_ACTIVE]) {
        PatchStart(ps, OFF_PS_ACTIVE, 0);
        ++*hidden;
      }
      continue;
    }
    if (mode != YGMF_ALL && mode != YGMF_TE) {
      if (ps[OFF_PS_ACTIVE]) {
        PatchStart(ps, OFF_PS_ACTIVE, 0);
        ++*hidden;
      }
      continue;
    }
    if (jetStart)
      continue; // the game skips these for every other spawn
    *ours += ps[OFF_PS_ACTIVE] ? 1 : 0;
    if (swap && mode == YGMF_TE && (team == 1 || team == 2))
      PatchStart(ps, OFF_PS_TEAM, (uint8_t)(3 - team));
  }
  return jets ? "TE jet" : swap ? "ALL/TE (sides swapped)" : "ALL/TE";
}

// One side's TE capital starts, by name.
static int TeSideStarts(int team, uint8_t **out, int cap) {
  static uint8_t *s_all[1024];
  int n = AllPlayerStarts(s_all, 1024), k = 0;
  for (int i = 0; i < n && k < cap; ++i) {
    uint8_t *ps = s_all[i];
    if (ps[OFF_PS_GAME_MODE] == YGMF_TE && ps[OFF_PS_TEAM] == team && !ps[OFF_PS_ONLY_JETS])
      out[k++] = ps;
  }
  for (int a = 1; a < k; ++a)
    for (int b = a; b > 0; --b) {
      const char *x = NameText(((UObjectMin *)out[b - 1])->Name), *y = NameText(((UObjectMin *)out[b])->Name);
      int nx = ((UObjectMin *)out[b - 1])->Name.Number, ny = ((UObjectMin *)out[b])->Name.Number;
      int c = x && y ? strcmp(x, y) : 0;
      if (c < 0 || (c == 0 && nx <= ny))
        break;
      uint8_t *t = out[b];
      out[b] = out[b - 1];
      out[b - 1] = t;
    }
  return k;
}

static int TePcTeam(uint8_t *pc) {
  uint8_t *pri = IsReadable(pc + OFF_PC_PLAYER_STATE, 8) ? *(uint8_t **)(pc + OFF_PC_PLAYER_STATE) : nullptr;
  int t = pri && IsReadable(pri + OFF_PRI_TEAM, 1) ? pri[OFF_PRI_TEAM] : 0;
  return t == 1 || t == 2 ? t : 0;
}

// The new-round spawn of 0x590B80, near the given start instead of the controller.
static bool TeSpawnAt(void *pc, uint8_t *start) {
  uint8_t *mgr = (uint8_t *)TeLoadoutManager(pc);
  uint8_t *lo = mgr && IsReadable(mgr + OFF_LOADOUT_MGR_ACTIVE, 8) ? *(uint8_t **)(mgr + OFF_LOADOUT_MGR_ACTIVE) : nullptr;
  if (!lo || !start || !IsReadable(lo + OFF_LOADOUT_ID, 8))
    return false;
  ((tSpawnNearActor)(g_base + RVA_PC_SPAWN_NEAR_ACTOR))(pc, lo + OFF_LOADOUT_ID, start, 0);
  ((tPcVoid)(g_base + RVA_PC_AFTER_NEW_ROUND_SPAWN))(pc);
  return true;
}

// The round's winners keep their ship: remembered at the round's end.
static void TeRememberWinners(uint8_t *gs, int winner) {
  memset(g_te.keep, 0, sizeof g_te.keep);
  g_te.lastWinner = winner;
  if (!winner || !TeWinnerLockOn())
    return;
  uint8_t *pcs[16];
  int n = TeHumanPCs(gs, pcs, 16), k = 0;
  for (int i = 0; i < n && k < 16; ++i) {
    if (TePcTeam(pcs[i]) != winner)
      continue;
    void *lo = nullptr;
    for (auto &e : g_te.elim)
      if (e.pc == pcs[i])
        lo = e.capital;
    if (!lo) {
      uint8_t *mgr = (uint8_t *)TeLoadoutManager(pcs[i]);
      lo = mgr && IsReadable(mgr + OFF_LOADOUT_MGR_ACTIVE, 8) ? *(void **)(mgr + OFF_LOADOUT_MGR_ACTIVE) : nullptr;
    }
    if (lo)
      g_te.keep[k++] = {pcs[i], lo};
  }
  Logf("te: round %d winners (team %d): %d human ship(s) kept for the next round", g_te.round, winner, k);
}
static void TeApplyKeep(void *pc) {
  for (auto &k : g_te.keep) {
    if (k.pc != pc || !k.loadout)
      continue;
    uint8_t *mgr = (uint8_t *)TeLoadoutManager(pc);
    void *now = mgr && IsReadable(mgr + OFF_LOADOUT_MGR_ACTIVE, 8) ? *(void **)(mgr + OFF_LOADOUT_MGR_ACTIVE) : nullptr;
    if (mgr && now != k.loadout) {
      ((tActivateLoadout)(g_base + RVA_ACTIVATE_LOADOUT))(mgr, k.loadout, 1);
      Logf("te: %p won the last round -- its pick %p undone, ship %p kept", pc, now, k.loadout);
    }
  }
}

// Ready-up between rounds.
static bool TeAllReady(uint8_t *gs) {
  uint8_t *pcs[16];
  int n = TeHumanPCs(gs, pcs, 16);
  if (n == 0)
    return false;
  for (int i = 0; i < n; ++i) {
    bool r = false;
    for (int j = 0; j < g_te.numReady; ++j)
      r |= g_te.ready[j] == pcs[i];
    if (!r)
      return false;
  }
  return true;
}
static void TeSetReady(void *pc, bool ready) {
  if (!TeRoundsOn() || !g_te.gm)
    return;
  int at = -1;
  for (int j = 0; j < g_te.numReady; ++j)
    if (g_te.ready[j] == pc)
      at = j;
  if (ready && at < 0 && g_te.numReady < 16)
    g_te.ready[g_te.numReady++] = pc;
  if (!ready && at >= 0)
    g_te.ready[at] = g_te.ready[--g_te.numReady];
  uint8_t *gs = TeGameState((uint8_t *)g_te.gm);
  uint8_t *pcs[16];
  int n = gs ? TeHumanPCs(gs, pcs, 16) : 0;
  Logf("te: %p %s (phase %d, round %d; %d of %d humans ready)", pc, ready ? "READY for the next round" : "not ready",
       g_te.phase, g_te.round, g_te.numReady, n);
}
#define RVA_EXEC_SERVER_READY_NEXT_WAVE 0x772650
#define RVA_EXEC_SERVER_UNREADY_NEXT_WAVE 0x774B40
static tExecThunk g_origExecReadyNextWave = nullptr, g_origExecUnReadyNextWave = nullptr;
static void __fastcall HookExecReadyNextWave(void *pc, uint8_t *stack, void *result) {
  g_origExecReadyNextWave(pc, stack, result);
  __try {
    TeSetReady(pc, true);
  } __except (EXCEPTION_EXECUTE_HANDLER) {
  }
}
static void __fastcall HookExecUnReadyNextWave(void *pc, uint8_t *stack, void *result) {
  g_origExecUnReadyNextWave(pc, stack, result);
  __try {
    TeSetReady(pc, false);
  } __except (EXCEPTION_EXECUTE_HANDLER) {
  }
}

// The team HUD.
struct TeTeamInfoArgs {
  FStringMin ids[32];
  wchar_t idbuf[32][24];
  int32_t cls[32], team[32];
  uint8_t elim[32], spotted[32];
  int n;
};
static UObjectMin *g_teTeamInfoProp = nullptr;
static void TeTeamInfoLayout(int off[5]) {
  static int s_off[5] = {0x08, 0x18, 0x28, 0x38, 0x48}, s_done = 0;
  static const char *kNames[5] = {"m_playerID", "m_classID", "m_eliminated", "m_spotted", "m_teamID"};
  if (!s_done && g_teTeamInfoProp) {
    s_done = 1;
    for (int k = 0x70; k <= 0x80; k += 8) {
      UObjectMin *st = IsReadable((uint8_t *)g_teTeamInfoProp + k, 8) ? *(UObjectMin **)((uint8_t *)g_teTeamInfoProp + k) : nullptr;
      if (!st || !IsReadable(st, 0x40) || strcmp(ClassNameOf(st), "ScriptStruct"))
        continue;
      for (UObjectMin *f = *(UObjectMin **)((uint8_t *)st + 0x38); f && IsReadable(f, 0x58);
           f = *(UObjectMin **)((uint8_t *)f + 0x28)) {
        const char *nm = NameText(f->Name);
        for (int j = 0; j < 5; ++j)
          if (nm && !strcmp(nm, kNames[j]))
            s_off[j] = (int)*(uint32_t *)((uint8_t *)f + 0x50);
      }
      Logf("te: YTeamEliminationHUDTeamInfo (struct at UStructProperty +0x%X): m_playerID +0x%X, m_classID +0x%X, "
           "m_eliminated +0x%X, m_spotted +0x%X, m_teamID +0x%X", k, s_off[0], s_off[1], s_off[2], s_off[3], s_off[4]);
      break;
    }
  }
  for (int j = 0; j < 5; ++j)
    off[j] = s_off[j];
}
static void FillTeTeamInfo(uint8_t *parms, const char *pc, const char *pn, uint32_t off, void *ctx) {
  TeTeamInfoArgs *a = (TeTeamInfoArgs *)ctx;
  if (strcmp(pc, "StructProperty") || off + 0x58 > 256)
    return;
  int o[5];
  TeTeamInfoLayout(o);
  uint8_t *s = parms + off;
  void *arrays[5] = {a->ids, a->cls, a->elim, a->spotted, a->team};
  for (int j = 0; j < 5; ++j) {
    *(void **)(s + o[j]) = a->n ? arrays[j] : nullptr;
    *(int32_t *)(s + o[j] + 8) = a->n;
    *(int32_t *)(s + o[j] + 12) = a->n;
  }
}
static TeHudRow *TeHudRowFor(void *pri) {
  TeHudRow *free = nullptr;
  for (auto &r : g_te.hud) {
    if (r.pri == pri)
      return &r;
    if (!r.pri && !free)
      free = &r;
  }
  if (free)
    *free = {pri, 0, false};
  return free;
}
static void TeSendTeamInfo(uint8_t *gs, bool force) {
  if (!TeHudOn() || !IsReadable(gs + OFF_GS_PLAYER_ARRAY, 16))
    return;
  uint8_t **arr = *(uint8_t ***)(gs + OFF_GS_PLAYER_ARRAY);
  int count = *(int32_t *)(gs + OFF_GS_PLAYER_ARRAY + 8);
  if (count < 0 || count > 256 || (count && !IsReadable(arr, (size_t)count * 8)))
    return;
  static TeTeamInfoArgs a;
  memset(&a, 0, sizeof a);
  int elimN = 0, spottedN = 0;
  for (int i = 0; i < count && a.n < 32; ++i) {
    uint8_t *pri = arr[i];
    if (!IsReadable(pri, OFF_PRI_SPOTTED + 1) || !ClassChainContains((UObjectMin *)pri, "YPlayerReplicationInfo"))
      continue;
    int team = pri[OFF_PRI_TEAM];
    if (team != 1 && team != 2)
      continue;
    uint8_t *ctrl = IsReadable(pri + OFF_ACTOR_OWNER, 8) ? *(uint8_t **)(pri + OFF_ACTOR_OWNER) : nullptr;
    uint8_t *pawn = ctrl && IsReadable(ctrl + OFF_CONTROLLER_PAWN, 8) ? *(uint8_t **)(ctrl + OFF_CONTROLLER_PAWN) : nullptr;
    int pcls = pawn && IsReadable(pawn + OFF_PAWN_STATE, 1) ? pawn[OFF_PAWN_SHIP_CLASS] : 0;
    bool live = pcls >= 1 && pcls <= 15 && pawn[OFF_PAWN_STATE] != 2 && pawn[OFF_PAWN_STATE] != 3;
    TeHudRow *row = TeHudRowFor(pri);
    if (row && live) {
      row->had = true;
      row->cls = pcls;
    }
    int k = a.n++;
    swprintf(a.idbuf[k], 24, L"%llX", (unsigned long long)(uintptr_t)pri);
    a.ids[k] = {a.idbuf[k], (int32_t)wcslen(a.idbuf[k]) + 1, 24};
    a.cls[k] = row && row->cls ? row->cls : pri[OFF_PRI_SHIP_CLASS];
    a.team[k] = team;
    a.elim[k] = g_te.phase == TE_ROUND && row && row->had && !live ? 1 : 0;
    a.spotted[k] = pri[OFF_PRI_SPOTTED] ? 1 : 0;
    elimN += a.elim[k];
    spottedN += a.spotted[k];
  }
  static uint32_t s_lastSig = 0xFFFFFFFF;
  uint32_t sig = (uint32_t)a.n * 2654435761u;
  for (int i = 0; i < a.n; ++i) {
    sig = (sig ^ (uint32_t)(a.cls[i] << 4 | a.team[i] << 2 | a.elim[i] << 1 | a.spotted[i])) * 16777619u;
    for (const wchar_t *c = a.idbuf[i]; *c; ++c)
      sig = (sig ^ (uint32_t)*c) * 16777619u;
  }
  if (!force && sig == s_lastSig)
    return;
  s_lastSig = sig;
  if (!g_teTeamInfoProp) {
    FNameMin nm = {};
    ((tFNameCtor)(g_base + RVA_FNAME_CTOR_WIDE))(&nm, L"ClientTeamEliminationPlayersChanged", 1);
    void *fn = ((tFindFunctionChecked)(g_base + RVA_FIND_FUNCTION_CHECKED))(gs, *(uint64_t *)&nm);
    g_teTeamInfoProp = fn && IsReadable((uint8_t *)fn + 0x40, 8) ? *(UObjectMin **)((uint8_t *)fn + 0x38) : nullptr;
  }
  bool sent = CallNamed(gs, L"ClientTeamEliminationPlayersChanged", FillTeTeamInfo, &a);
  Logf("te: team HUD -> %d player(s), %d eliminated, %d spotted%s", a.n, elimN, spottedN,
       sent ? "" : " -- ClientTeamEliminationPlayersChanged NOT FOUND");
}

// Round start: nobody has lost a ship yet, nobody is spotted. GUESS: the reset.
static void TeResetRoundState(uint8_t *gs) {
  for (auto &r : g_te.hud)
    r.had = false;
  g_te.numReady = 0;
  if (!IsReadable(gs + OFF_GS_PLAYER_ARRAY, 16))
    return;
  uint8_t **arr = *(uint8_t ***)(gs + OFF_GS_PLAYER_ARRAY);
  int count = *(int32_t *)(gs + OFF_GS_PLAYER_ARRAY + 8);
  if (count < 0 || count > 256 || (count && !IsReadable(arr, (size_t)count * 8)))
    return;
  for (int i = 0; i < count; ++i)
    if (IsReadable(arr[i], OFF_PRI_SPOTTED + 1) && ClassChainContains((UObjectMin *)arr[i], "YPlayerReplicationInfo") &&
        arr[i][OFF_PRI_SPOTTED]) {
      arr[i][OFF_PRI_SPOTTED] = 0;
      TeForceNetUpdate(arr[i]);
    }
}

// Every second, from the game-mode timer.
static void TeRoundsTick(uint8_t *gm) {
  __try {
    uint8_t *gs = TeGameState(gm);
    if (!gs)
      return;
    // Crash guard, on every TE host (rounds on or off): never an empty m_rounds.
    if (!TeLastRound(gs))
      TeOpenRound(gs, TeRoundsOn() && g_te.gm == gm && g_te.round > 0 ? g_te.round : 1);
    if (!TeRoundsOn())
      return;
    DWORD now = GetTickCount();
    if (g_te.gm != gm) {
      memset(&g_te, 0, sizeof g_te);
      g_te.gm = gm;
      int r = IsReadable(gm + OFF_GM_TE_ROUNDS_TO_WIN, 4) ? *(int32_t *)(gm + OFF_GM_TE_ROUNDS_TO_WIN) : 0;
      g_te.rule = r >= 1 && r <= 5 ? r : TE_ROUNDS_TO_WIN_TEXT;
      Logf("te: match %p -- rounds armed: %d to win (game mode says %d), round limit %d s, post-round %d s, "
           "pre-round %d s, kill limit %d lifted", gm, g_te.rule, r, TeRoundTimeSecs(gm),
           TeSeconds(gm, OFF_GM_TE_POST_ROUND, 8), TeSeconds(gm, OFF_GM_TE_PRE_ROUND, 10),
           *(int32_t *)(gm + OFF_GM_KILLS_FOR_WINNING));
      *(int32_t *)(gm + OFF_GM_KILLS_FOR_WINNING) = ONS_KILL_LIMIT_OFF;
    }
    int alive[2];
    TeCountShips(alive);
    if (gs[OFF_GS_MATCH_RUNNING] && g_te.phase != TE_DONE)
      TeSendTeamInfo(gs, false);
    switch (g_te.phase) {
    case TE_WAIT: {
      // Smoke only (dn_host_te_smoke.txt): start the match 20 s in with nobody
      // joined, then report the side starts and send the team HUD once.
      static void *s_smoke = nullptr;
      static DWORD s_armedAt = 0;
      if (s_smoke != gm && SwitchOn("DN_HOST_TE_SMOKE", "dn_host_te_smoke.txt")) {
        if (!s_armedAt)
          s_armedAt = now;
        if (!gs[OFF_GS_MATCH_RUNNING] && now - s_armedAt > 20000) {
          Logf("te: SMOKE -- starting match %p with nobody joined", gm);
          ((void(__fastcall *)(void *))(*(void ***)gm)[0x660 / 8])(gm);
          ((void(__fastcall *)(void *, char))(g_base + 0x380F70))(gs, 1);
        } else if (gs[OFF_GS_MATCH_RUNNING]) {
          s_smoke = gm;
          uint8_t *side[16];
          for (int t = 1; t <= 2; ++t) {
            int k = TeSideStarts(t, side, 16);
            Logf("te: SMOKE -- team %d side: %d TE start(s), first %s", t, k,
                 k ? NameText(((UObjectMin *)side[0])->Name) : "none");
          }
          int h = 0, o = 0;
          const char *rule = PrepareTeStarts(nullptr, &h, &o);
          int patched = g_numStartPatches;
          RestoreStarts();
          Logf("te: SMOKE -- chooser rule %s: %d hidden, %d usable, %d patches restored", rule, h, o, patched);
          TeSendTeamInfo(gs, true);
        }
      }
      if (gs[OFF_GS_MATCH_RUNNING] && alive[0] > 0 && alive[1] > 0)
        TeStartRound(gm, gs, now);
      break;
    }
    case TE_ROUND: {
      for (int t = 0; t < 2; ++t)
        g_te.seen[t] |= alive[t] > 0;
      if (g_te.botsOffAt && now - g_te.botsOffAt < 0x80000000u) {
        gm[OFF_GM_ENABLE_SPAWN_AI] = 0;
        g_te.botsOffAt = 0;
        Logf("te: round %d -- bots refilled, spawner off again", g_te.round);
      }
      int winner = -1;
      const char *why = nullptr;
      bool settled = now - g_te.phaseAt > 15000 && g_te.seen[0] && g_te.seen[1];
      if (settled && (alive[0] == 0 || alive[1] == 0)) {
        winner = alive[0] ? 1 : alive[1] ? 2 : 0;
        why = "eliminated";
      } else if (now - g_te.phaseAt > (DWORD)TeRoundTimeSecs(gm) * 1000) {
        winner = alive[0] > alive[1] ? 1 : alive[1] > alive[0] ? 2 : 0; // GUESS
        why = "time";
      }
      if (winner < 0)
        break;
      TeRecordRound(gs, g_te.round, winner);
      if (winner)
        g_te.wins[winner - 1]++;
      TeRememberWinners(gs, winner);
      gs[OFF_GS_TE_TRANSITION] = 1;
      TeForceNetUpdate(gs);
      g_te.phase = TE_POST;
      g_te.phaseAt = now;
      Logf("te: round %d over (%s) -- %s; ships left %d-%d; score %d-%d", g_te.round, why,
           winner ? (winner == 1 ? "team 1 wins it" : "team 2 wins it") : "draw", alive[0], alive[1], g_te.wins[0],
           g_te.wins[1]);
      TeSendAll(gs, L"ClientPostRoundEnd", TeSeconds(gm, OFF_GM_TE_POST_ROUND, 8), 0);
      int champ = g_te.wins[0] >= g_te.rule ? 1 : g_te.wins[1] >= g_te.rule ? 2 : 0;
      if (champ) {
        g_te.phase = TE_DONE;
        gs[OFF_GS_WINNING_TEAM] = (uint8_t)champ;
        Logf("te: team %d wins the match %d-%d -- ending it", champ, g_te.wins[0], g_te.wins[1]);
        void **vt = *(void ***)gm;
        ((void(__fastcall *)(void *))vt[VT_GM_END_MATCH / 8])(gm);
      }
      break;
    }
    case TE_POST:
      if (now - g_te.phaseAt >= (DWORD)TeSeconds(gm, OFF_GM_TE_POST_ROUND, 8) * 1000) {
        g_te.phase = TE_PRE;
        g_te.phaseAt = now;
        TeRestoreShips(gs);
        gm[OFF_GM_ENABLE_SPAWN_AI] = 1; // refill the bots destroyed this round
        TeSendAll(gs, L"ClientPreRoundStart", TeSeconds(gm, OFF_GM_TE_PRE_ROUND, 10), 0);
      }
      break;
    case TE_PRE: {
      bool allReady = now - g_te.phaseAt >= 2000 && TeAllReady(gs);
      if (allReady || now - g_te.phaseAt >= (DWORD)TeSeconds(gm, OFF_GM_TE_PRE_ROUND, 10) * 1000) {
        if (allReady)
          Logf("te: every human is ready -- round %d starts %u s early", g_te.round + 1,
               (unsigned)(TeSeconds(gm, OFF_GM_TE_PRE_ROUND, 10) - (now - g_te.phaseAt) / 1000));
        uint8_t *pcs[16];
        int n = TeHumanPCs(gs, pcs, 16), atStart = 0;
        bool swap = TeSidesSwapped();
        uint8_t *side[2][16];
        int nSide[2] = {0, 0}, next[2] = {0, 0};
        if (TeSpawnsEnabled())
          for (int t = 1; t <= 2; ++t)
            nSide[t - 1] = TeSideStarts(swap ? 3 - t : t, side[t - 1], 16);
        for (int i = 0; i < n; ++i) {
          __try {
            TeApplyKeep(pcs[i]);
            int team = TePcTeam(pcs[i]);
            uint8_t *start = team && nSide[team - 1] ? side[team - 1][next[team - 1]++ % nSide[team - 1]] : nullptr;
            if (start && TeSpawnAt(pcs[i], start))
              ++atStart;
            else
              ((tPcVoid)(g_base + RVA_REPLACE_SHIP_NEW_ROUND))(pcs[i]);
          } __except (EXCEPTION_EXECUTE_HANDLER) {
            Logf("te: EXCEPTION putting %p back in a ship", pcs[i]);
          }
        }
        Logf("te: %d players put back in a ship for round %d -- %d at their side's TE starts (sides %s; %d / %d "
             "starts), %d near their own position", n, g_te.round + 1, atStart, swap ? "swapped" : "as placed",
             nSide[0], nSide[1], n - atStart);
        g_te.botsOffAt = now + 15000;
        TeStartRound(gm, gs, now);
      }
      break;
    }
    default:
      break;
    }
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    Logf("te: EXCEPTION in the round tick");
  }
}

// Fighters only while eliminated. Measured in the first live TE match
// (2026-10-25): with the jets filter on, the player's capital loadout was
// refused once ("ActivateLoadout | Loadout [...] is not valid", host 22:58:22)
// and they flew a respawn jet -- but when the jet was lost, the next respawn
// was the capital ship again: something on the respawn path clears or bypasses
// the filter (the client's own respawn steps call ClearLoadoutFilter 0x33CF50).
// So the filter is checked where the ship is made, AYPlayerController::
// SpawnPlayer (0x5998A0, one argument): missing, or the active loadout not
// valid under it (0x33C680), it is applied again, which activates the first
// fighter (SetLoadoutsFilter). Installed on TE hosts only.
static tSpawnPlayer g_origTeSpawnPlayer = nullptr;
static bool HostIsTE() {
  const wchar_t *cmd = GetCommandLineW();
  return cmd && wcsstr(cmd, L"-GameMode=TE ") != nullptr;
}
static bool TeIsEliminated(void *pc) {
  for (auto &e : g_te.elim)
    if (e.pc && e.pc == pc)
      return true;
  return false;
}
static void __fastcall HookTeSpawnPlayer(void *pc) {
  __try {
    if (TeRoundsOn() && g_te.gm && g_te.phase == TE_ROUND && TeIsEliminated(pc)) {
      uint8_t *gs = TeGameState((uint8_t *)g_te.gm);
      uint8_t *mgr = (uint8_t *)TeLoadoutManager(pc);
      void *filter = gs ? *(void **)(gs + OFF_GS_TE_FIGHTER_FILTER) : nullptr;
      if (mgr && filter && IsReadable(mgr + OFF_LOADOUT_MGR_ACTIVE, 8)) {
        void *have = *(void **)(mgr + OFF_LOADOUT_MGR_FILTER);
        void *active = *(void **)(mgr + OFF_LOADOUT_MGR_ACTIVE);
        bool valid = active && ((tLoadoutGate)(g_base + RVA_LOADOUT_IS_REGISTERED))(mgr, active);
        if (have != filter || !valid) {
          if (have == filter)
            ((tSetLoadoutsFilter)(g_base + RVA_SET_LOADOUTS_FILTER))(mgr, nullptr);
          ((tSetLoadoutsFilter)(g_base + RVA_SET_LOADOUTS_FILTER))(mgr, filter);
        }
        Logf("te: round %d -- respawn of eliminated %p: filter %s, %s loadout %p -> %s %p", g_te.round, pc,
             have == filter ? "still on" : "WAS CLEARED", valid ? "valid" : "invalid", active,
             have != filter || !valid ? "re-filtered, now" : "kept", *(void **)(mgr + OFF_LOADOUT_MGR_ACTIVE));
      }
    }
  } __except (EXCEPTION_EXECUTE_HANDLER) {
    Logf("te: EXCEPTION checking a respawn");
  }
  g_origTeSpawnPlayer(pc);
}

// The winner by rounds won (EndMatch and the match timer recompute it from kills).
static void TeFixLeader(uint8_t *gm) {
  if (!TeRoundsOn() || g_te.gm != gm || (!g_te.wins[0] && !g_te.wins[1]))
    return;
  uint8_t *gs = TeGameState(gm);
  if (!gs)
    return;
  uint8_t byKills = gs[OFF_GS_WINNING_TEAM];
  gs[OFF_GS_WINNING_TEAM] = g_te.wins[0] > g_te.wins[1] ? 1 : g_te.wins[1] > g_te.wins[0] ? 2 : 3;
  if (gs[OFF_GS_WINNING_TEAM] != byKills)
    Logf("te: leader by rounds %d (%d-%d), was %d by kills", gs[OFF_GS_WINNING_TEAM], g_te.wins[0], g_te.wins[1],
         byKills);
}

// ---------------------------------------------------------------------------
// Havoc team HUD (stage 4, 2026-10-19). AYGameState_Havoc::
// ClientHavocPlayersChanged(teamsInfo) (thunk 0x745410, NetMulticast) carries
// ONE YHavocHUDTeamInfo of parallel arrays (0x38 bytes: +0x08 TArray<FString>
// m_playerID, +0x18 TArray<int32> m_classID, +0x28 TArray<uint8>
// m_eliminated -- the thunk's copy); its _Implementation (GS vtable +0x888 =
// 0x3689E0) hands the class and eliminated arrays to the HUD's team list
// (0x5598B0). The client's own refresh (AYHUD::UpdateHavocTeamState 0x562C00)
// builds the same two arrays from the player states (class = PRI +0x941;
// eliminated when it is 0x10 or 0x13-0x15), but nothing on a login-less host
// marks a player eliminated there, so the director sends the list itself:
// one entry per connected player in PlayerArray order, the class the client
// would read, eliminated from HavocTrackEliminations. Offsets are read from the
// struct's reflection when found (logged once), else the thunk's.
// GUESS: m_playerID is left empty -- the implementation does not read it.
static void HavocTeamInfoLayout(UObjectMin *structProp, int *offId, int *offClass, int *offElim) {
  static int s_id = -1, s_class = -1, s_elim = -1, s_done = 0;
  if (!s_done && structProp) {
    s_done = 1;
    for (int k = 0x70; k <= 0x80; k += 8) {
      UObjectMin *st = IsReadable((uint8_t *)structProp + k, 8) ? *(UObjectMin **)((uint8_t *)structProp + k) : nullptr;
      if (!st || !IsReadable(st, 0x40) || strcmp(ClassNameOf(st), "ScriptStruct"))
        continue;
      for (UObjectMin *f = *(UObjectMin **)((uint8_t *)st + 0x38); f && IsReadable(f, 0x58);
           f = *(UObjectMin **)((uint8_t *)f + 0x28)) {
        const char *n = NameText(f->Name);
        int off = (int)*(uint32_t *)((uint8_t *)f + 0x50);
        if (n && !strcmp(n, "m_playerID")) s_id = off;
        else if (n && !strcmp(n, "m_classID")) s_class = off;
        else if (n && !strcmp(n, "m_eliminated")) s_elim = off;
      }
      Logf("havoc: YHavocHUDTeamInfo (struct at UStructProperty +0x%X): m_playerID +0x%X, m_classID +0x%X, "
           "m_eliminated +0x%X", k, s_id, s_class, s_elim);
      break;
    }
  }
  *offId = s_id >= 0 ? s_id : 0x08;
  *offClass = s_class >= 0 ? s_class : 0x18;
  *offElim = s_elim >= 0 ? s_elim : 0x28;
}

struct HavocTeamInfoArgs {
  int32_t classes[16];
  uint8_t elim[16];
  int n;
};
static void FillTeamInfo(uint8_t *parms, const char *pc, const char *pn, uint32_t off, void *ctx) {
  HavocTeamInfoArgs *a = (HavocTeamInfoArgs *)ctx;
  if (strcmp(pc, "StructProperty") || off + 0x38 > 256)
    return;
  extern UObjectMin *g_hvcTeamInfoProp;
  int oId, oClass, oElim;
  HavocTeamInfoLayout(g_hvcTeamInfoProp, &oId, &oClass, &oElim);
  uint8_t *s = parms + off;
  *(void **)(s + oId) = nullptr; // TArray<FString>: empty
  *(int32_t *)(s + oId + 8) = 0;
  *(int32_t *)(s + oId + 12) = 0;
  *(void **)(s + oClass) = a->classes;
  *(int32_t *)(s + oClass + 8) = a->n;
  *(int32_t *)(s + oClass + 12) = a->n;
  *(void **)(s + oElim) = a->elim;
  *(int32_t *)(s + oElim + 8) = a->n;
  *(int32_t *)(s + oElim + 12) = a->n;
}
UObjectMin *g_hvcTeamInfoProp = nullptr;

// Sends the team list when it changed (players, classes or eliminations).
static void HavocSendTeamInfo(uint8_t *gs, bool force) {
  if (!IsReadable(gs + OFF_GS_PLAYER_ARRAY, 16))
    return;
  uint8_t **arr = *(uint8_t ***)(gs + OFF_GS_PLAYER_ARRAY);
  int count = *(int32_t *)(gs + OFF_GS_PLAYER_ARRAY + 8);
  if (count < 0 || count > 256 || (count && !IsReadable(arr, (size_t)count * 8)))
    return;
  HavocTeamInfoArgs a = {};
  for (int i = 0; i < count && a.n < 16; ++i) {
    uint8_t *pri = arr[i];
    if (!IsReadable(pri, 0x948) || !ClassChainContains((UObjectMin *)pri, "YPlayerReplicationInfo"))
      continue;
    uint8_t *pc = IsReadable(pri + OFF_ACTOR_OWNER, 8) ? *(uint8_t **)(pri + OFF_ACTOR_OWNER) : nullptr;
    if (!pc || !IsReadable(pc, OFF_PC_NETCONNECTION + 8) || !ClassChainContains((UObjectMin *)pc, "PlayerController") ||
        !*(void **)(pc + OFF_PC_NETCONNECTION))
      continue;
    a.classes[a.n] = pri[0x941];
    a.elim[a.n] = HavocIsEliminated(pc) ? 1 : 0;
    ++a.n;
  }
  static uint32_t s_lastSig = 0xFFFFFFFF;
  uint32_t sig = (uint32_t)a.n * 2654435761u;
  for (int i = 0; i < a.n; ++i)
    sig = (sig ^ (uint32_t)(a.classes[i] << 1 | a.elim[i])) * 16777619u;
  if (!force && sig == s_lastSig)
    return;
  s_lastSig = sig;
  if (!g_hvcTeamInfoProp) {
    FNameMin n = {};
    ((tFNameCtor)(g_base + RVA_FNAME_CTOR_WIDE))(&n, L"ClientHavocPlayersChanged", 1);
    void *fn = ((tFindFunctionChecked)(g_base + RVA_FIND_FUNCTION_CHECKED))(gs, *(uint64_t *)&n);
    g_hvcTeamInfoProp = fn && IsReadable((uint8_t *)fn + 0x40, 8) ? *(UObjectMin **)((uint8_t *)fn + 0x38) : nullptr;
  }
  bool sent = CallNamed(gs, L"ClientHavocPlayersChanged", FillTeamInfo, &a);
  int out = 0;
  for (int i = 0; i < a.n; ++i)
    out += a.elim[i];
  Logf("havoc: team HUD -> %d player(s), %d eliminated%s", a.n, out, sent ? "" : " -- ClientHavocPlayersChanged NOT FOUND");
}

// The reflected property at byte offset off of obj's class chain: its name
// and PropertyFlags (UProperty +0x38; CPF_Net = 0x20). For logging.
static const char *PropAt(UObjectMin *obj, int off, uint64_t *flags) {
  *flags = 0;
  if (!IsReadable(obj, sizeof(UObjectMin)))
    return nullptr;
  for (UObjectMin *cls = obj->Class; cls && IsReadable(cls, 0x40); cls = *(UObjectMin **)((uint8_t *)cls + OFF_USTRUCT_SUPER))
    for (UObjectMin *f = *(UObjectMin **)((uint8_t *)cls + 0x38); f && IsReadable(f, 0x58); f = *(UObjectMin **)((uint8_t *)f + 0x28))
      if (*(int32_t *)((uint8_t *)f + 0x50) == off && strcmp(ClassNameOf(f), "Function")) {
        *flags = *(uint64_t *)((uint8_t *)f + 0x38);
        return NameText(f->Name);
      }
  return nullptr;
}

// Havoc HUD wave progress. AYGameState_Havoc::ClientUpdateWaveProgress
// (NetMulticast, no parameters; thunk 0x747D60 -> GS vtable 0x2E9C9A0 +0x8A8
// = 0x369600) reads the client's replicated copy of m_wavesReached (+0x2024),
// m_maxWaveEnemies (+0x1F44), m_numEnemiesKilledThisWave (+0x1F40) and +0x1F54,
// adds the DN_HavocBossWaves_DT flag, and hands them to the HUD (0x5598F0 ->
// UI_HUD_GameModeHavoc_WBP BP_SetWaveProgress: the "WAVE n" text and the kill
// progress bar). Its only sender was the server build, so the bar never moved
// and wave 1 never got a number (the client sets the next wave's number only
// on entering UpgradeSelection, ClientHavocStateChanged 0x536820).
// Sent when the values change, during WaveInProgress and WaveComplete only:
// in UpgradeSelection the HUD shows the NEXT wave, which this would overwrite.
// The values must have replicated before the RPC lands (an RPC goes out at
// once, properties at the next net update), so a change is sent one director
// tick (1 s) after it is seen, if it held.
// GUESS: when the original server sent it (per kill, per spawn?) is unknown.
static void HavocSendWaveProgress(uint8_t *gs, int phase) {
  static const int kOffs[4] = {OFF_HVC_GS_WAVES_REACHED, OFF_HVC_GS_MAX_WAVE_ENEMIES, OFF_HVC_GS_KILLED, 0x1F54};
  static void *s_logged = nullptr;
  if (s_logged != gs) {
    s_logged = gs;
    for (int off : kOffs) {
      uint64_t fl = 0;
      const char *n = PropAt((UObjectMin *)gs, off, &fl);
      Logf("havoc: wave progress field +0x%X = %s, flags 0x%llX (%s)", off, n ? n : "?", (unsigned long long)fl,
           fl & 0x20 ? "replicated" : "NOT replicated");
    }
  }
  static uint64_t s_seen = ~0ull, s_sent = ~0ull;
  if (phase != 2 && phase != 3) {
    s_seen = ~0ull;
    return;
  }
  uint64_t sig = 0;
  for (int off : kOffs)
    sig = sig << 16 | (uint16_t)*(int32_t *)(gs + off);
  bool held = sig == s_seen;
  s_seen = sig;
  if (!held || sig == s_sent)
    return;
  s_sent = sig;
  bool ok = CallNamed(gs, L"ClientUpdateWaveProgress", nullptr, nullptr);
  Logf("havoc: wave progress -> wave %d, killed %d of %d (+%d)%s", *(int32_t *)(gs + kOffs[0]),
       *(int32_t *)(gs + kOffs[2]), *(int32_t *)(gs + kOffs[1]), *(int32_t *)(gs + kOffs[3]),
       ok ? "" : " -- ClientUpdateWaveProgress NOT SENT");
}

// Havoc next-wave countdown. AYGameState_Havoc::ClientUpdateTimeToNextWave
// (int; thunk 0x747CD0 -> GS vtable +0x890 = 0x369580) feeds the upgrade
// menu's wave info (0x5598D0 -> UI_HUD_HavocProgressionMenu_WaveInfo_WBP
// BP_SetTimeUntilNextWave: "wave begins in {seconds}") and the controller.
// The widget has no timer of its own -- it shows the number it is given and
// hides the line on -1 (BP_GetTimeLeftHideTimerValue 0x2A1FC50 = `or eax, -1`)
// -- so the server sent it every second. Never sent before: no countdown.
// GUESS: whether the original counted to WaveStarting (here) or to the
// enemies' arrival (+m_waveStartDelayDuration) is unknown.
static void FillTimeToNextWave(uint8_t *parms, const char *pc, const char *, uint32_t off, void *ctx) {
  if (!strcmp(pc, "IntProperty"))
    *(int32_t *)(parms + off) = *(int *)ctx;
}
static void HavocSendTimeToNextWave(uint8_t *gs, int secs) {
  static int s_last = -1;
  if (secs < -1)
    secs = 0;
  if (secs == s_last)
    return;
  bool first = s_last == -1;
  s_last = secs;
  bool ok = CallNamed(gs, L"ClientUpdateTimeToNextWave", FillTimeToNextWave, &secs);
  if (first || secs == -1 || !ok)
    Logf("havoc: next-wave countdown -> %d s%s", secs, ok ? "" : " -- ClientUpdateTimeToNextWave NOT SENT");
}

// Havoc end-of-match rewards list. The client's Havoc end-of-match flow
// (UYEndOfMatchManager::StartEndOfMatchFlow 0x454610 adds the pending step
// "ApplyHavocProgressionData" for game mode 17) runs 0x449470, which waits
// for TWO things, binding a delegate and returning while either is missing:
//   - the PRI match score (+0xA44 != -1): ClientUpdateHighscore, sent at
//     game over (HavocReportRuns);
//   - the game state's reward list (+0x20D0, getter 0x36E940: TArray of 12-byte
//     entries, an "available" bool at +0x20E0, a delegate at +0x20E8) --
//     "PlayerScoreData not yet available. Bind function call to delegate."
// Only AYGameState_Havoc::ClientUpdateRewards (thunk 0x747C30 -> GS vtable
// +0x8A0 -> 0x373E40) fills it and sets "available", and nothing in this exe
// sends it, so the step never completed: the end-of-match scene showed the
// captain and stopped there. Its parameter is TArray<YHavocRewardDataTableRow>
// (0x18 bytes, registration 0x9311F0: m_highscore int +0x08, m_rewardType
// EYHavocRewardType byte +0x0C, m_value int +0x10) -- the DN_HavocRewards_DT
// rows, which the server loaded from m_rewardsDataTableCfgPath
// (DefaultHavoc.ini, an mmogbrain .cfg). 0x373E40 converts m_rewardType 0 and
// 1 only (YHRT_FREEXP, YHRT_CREDITS, the first two names of the enum and the
// only two types the table uses). The screen walks the list in order against
// the weekly best and the match score, so it goes lowest score first.
// The function is an UNRELIABLE multicast (flags 0x84C40, no NetReliable), so
// it is sent at game over and twice more before EndMatch.
// GUESS: when the original server sent it (match start?) is unknown.
static const struct { int32_t highscore; uint8_t type; int32_t value; } kHavocRewardRows[] = {
    // DN_HavocRewards_DT: Beginner, 2, 3, 4, 5, 6, 7 (0 = YHRT_FREEXP, 1 = YHRT_CREDITS)
    {1000, 0, 500}, {1500, 1, 750}, {2500, 1, 1000}, {4500, 0, 2000},
    {8500, 1, 2500}, {15000, 0, 5000}, {25000, 1, 7500},
};
#define HVC_REWARD_ROW_SIZE 0x18
static void FillHavocRewards(uint8_t *parms, const char *pc, const char *, uint32_t off, void *ctx) {
  if (strcmp(pc, "ArrayProperty"))
    return;
  int n = (int)(sizeof kHavocRewardRows / sizeof kHavocRewardRows[0]);
  *(void **)(parms + off) = ctx;
  *(int32_t *)(parms + off + 8) = n;
  *(int32_t *)(parms + off + 12) = n;
}
// Checks the row layout above against reflection once (ArrayProperty Inner
// +0x78 -> StructProperty Struct +0x78 -> fields, as the team-info struct
// was found); false = do not send.
static bool HavocRewardRowLayoutOK(uint8_t *gs) {
  static int s_ok = -1;
  if (s_ok >= 0)
    return s_ok == 1;
  s_ok = 0;
  FNameMin n = {};
  ((tFNameCtor)(g_base + RVA_FNAME_CTOR_WIDE))(&n, L"ClientUpdateRewards", 1);
  void *fn = ((tFindFunctionChecked)(g_base + RVA_FIND_FUNCTION_CHECKED))(gs, *(uint64_t *)&n);
  UObjectMin *arr = fn && IsReadable((uint8_t *)fn + 0x40, 8) ? *(UObjectMin **)((uint8_t *)fn + 0x38) : nullptr;
  UObjectMin *inner = arr && IsReadable((uint8_t *)arr + 0x80, 8) ? *(UObjectMin **)((uint8_t *)arr + 0x78) : nullptr;
  UObjectMin *st = inner && IsReadable((uint8_t *)inner + 0x80, 8) ? *(UObjectMin **)((uint8_t *)inner + 0x78) : nullptr;
  int hs = -1, ty = -1, va = -1;
  if (st && IsReadable(st, 0x40) && !strcmp(ClassNameOf(st), "ScriptStruct"))
    for (UObjectMin *f = *(UObjectMin **)((uint8_t *)st + 0x38); f && IsReadable(f, 0x58); f = *(UObjectMin **)((uint8_t *)f + 0x28)) {
      const char *fnm = NameText(f->Name);
      int off = (int)*(uint32_t *)((uint8_t *)f + 0x50);
      if (fnm && !strcmp(fnm, "m_highscore")) hs = off;
      else if (fnm && !strcmp(fnm, "m_rewardType")) ty = off;
      else if (fnm && !strcmp(fnm, "m_value")) va = off;
    }
  s_ok = hs == 0x08 && ty == 0x0C && va == 0x10 && inner && IsReadable((uint8_t *)inner + 0x34, 4) &&
         *(int32_t *)((uint8_t *)inner + 0x34) == HVC_REWARD_ROW_SIZE;
  Logf("havoc: YHavocRewardDataTableRow by reflection (%s %s, %s): element size %d, m_highscore +0x%X, "
       "m_rewardType +0x%X, m_value +0x%X -> %s", arr ? ClassNameOf(arr) : "?", arr ? NameText(arr->Name) : "?",
       st ? NameText(st->Name) : "?", inner && IsReadable((uint8_t *)inner + 0x34, 4) ? *(int32_t *)((uint8_t *)inner + 0x34) : -1,
       hs, ty, va, s_ok ? "OK" : "MISMATCH, not sent");
  return s_ok == 1;
}

static void HavocSendRewards(uint8_t *gs, const char *why) {
  if (!HavocRewardRowLayoutOK(gs))
    return;
  static uint8_t s_rows[sizeof kHavocRewardRows / sizeof kHavocRewardRows[0]][HVC_REWARD_ROW_SIZE];
  for (size_t i = 0; i < sizeof kHavocRewardRows / sizeof kHavocRewardRows[0]; ++i) {
    memset(s_rows[i], 0, HVC_REWARD_ROW_SIZE);
    *(int32_t *)(s_rows[i] + 0x08) = kHavocRewardRows[i].highscore;
    s_rows[i][0x0C] = kHavocRewardRows[i].type;
    *(int32_t *)(s_rows[i] + 0x10) = kHavocRewardRows[i].value;
  }
  bool ok = CallNamed(gs, L"ClientUpdateRewards", FillHavocRewards, s_rows);
  uint8_t *avail = gs + 0x20E0;
  Logf("havoc: reward list (%d DN_HavocRewards_DT rows) -> clients (%s)%s; host copy available %d",
       (int)(sizeof kHavocRewardRows / sizeof kHavocRewardRows[0]), why, ok ? "" : " -- ClientUpdateRewards NOT SENT",
       IsReadable(avail, 1) ? *avail : -1);
}

// Clears every connected player's ready flag (a new wave, a new choice).
static void HavocClearReady(uint8_t *gs) {
  uint8_t *pcs[16];
  int n = HavocHumanPCs(gs, pcs, 16);
  for (int i = 0; i < n; ++i)
    HavocSetReadyFlag(pcs[i], false);
  InterlockedExchange(&g_hvcReadyCount, 0);
}

// One-time check of every reflected offset the director uses against the
// engine's own reflection (PropOffset: UProperty Offset_Internal). The native
// ones were also read off their property registration in the exe
// (2026-10-19); the Blueprint ones (GameState_Havoc_BP, PVE_HordeManager_BP)
// exist only in the cooked assets, so this is their only check. A MISMATCH
// means the constant is wrong for this build: the director writes there.
static void HavocCheckOffsets(uint8_t *gm, uint8_t *gs) {
  struct Want { int which; const char *name; int off; };
  static const Want kWant[] = {
      {0, "m_waveCompleteDuration", OFF_HVC_GM_WAVE_COMPLETE_DUR}, {0, "m_upgradeSelectionDuration", OFF_HVC_GM_UPGRADE_DUR},
      {0, "m_waveStartDelayDuration", OFF_HVC_GM_WAVE_START_DELAY}, {0, "m_pveEventManager", OFF_HVC_GM_PVE_EVENT_MANAGER},
      {0, "OnPVEModeStarted", OFF_HVC_GM_ON_PVE_MODE_STARTED}, {0, "OnAnyNPCKilled", OFF_HVC_GM_ON_ANY_NPC_KILLED},
      {1, "m_isMatchStarted", OFF_HVC_GS_IS_MATCH_STARTED}, {1, "m_numAliveEnemies", OFF_HVC_GS_NUM_ALIVE},
      {1, "m_numEnemiesKilledThisWave", OFF_HVC_GS_KILLED}, {1, "m_maxWaveEnemies", OFF_HVC_GS_MAX_WAVE_ENEMIES},
      {1, "m_maxNumberOfWaves", OFF_HVC_GS_MAX_WAVES}, {1, "m_currentWaveIndex", OFF_HVC_GS_WAVE_INDEX},
      {1, "m_currentWaveState", OFF_HVC_GS_WAVE_STATE}, {1, "m_currentWaveDuration", OFF_HVC_GS_WAVE_DURATION},
      {1, "m_currentHavocState", OFF_HVC_GS_HAVOC_STATE}, {1, "m_wavesReached", OFF_HVC_GS_WAVES_REACHED},
      {1, "m_playerWaveXP", OFF_HVC_GS_PLAYER_WAVE_XP}, {1, "m_totalXP", OFF_HVC_GS_TOTAL_XP},
      {1, "m_pveEventManager", OFF_HVC_GS_PVE_EVENT_MANAGER}, {1, "OnHordeWaveStateChanged", OFF_HVC_GS_ON_WAVE_STATE_CHANGED},
      {1, "EventStarted", OFF_HVC_GS_EVENT_STARTED}, {1, "HordeManager", OFF_HVC_GS_HORDE_MANAGER},
      {2, "GameMode", OFF_HVC_HM_GAME_MODE}, {2, "Waves", OFF_HVC_HM_WAVES},
      {2, "m_combatSceneManager", OFF_PVE_EM_COMBAT_SCENE_MANAGER},
  };
  int hmOff = PropOffset((UObjectMin *)gs, "HordeManager");
  uint8_t *hm = hmOff > 0 && IsReadable(gs + hmOff, 8) ? *(uint8_t **)(gs + hmOff) : nullptr;
  uint8_t *objs[3] = {gm, gs, hm};
  int ok = 0, bad = 0, missing = 0;
  for (const Want &w : kWant) {
    uint8_t *o = objs[w.which];
    int got = o ? PropOffset((UObjectMin *)o, w.name) : -2;
    if (got == w.off)
      ++ok;
    else if (got < 0) {
      ++missing;
      Logf("havoc: offsets: %s.%s not reflected%s (constant 0x%X unchecked)", o ? ClassNameOf(o) : "?", w.name,
           o ? "" : " -- object missing", w.off);
    } else {
      ++bad;
      Logf("havoc: offsets: MISMATCH %s.%s is +0x%X, constant +0x%X", ClassNameOf(o), w.name, got, w.off);
    }
  }
  Logf("havoc: offsets checked against reflection: %d OK, %d MISMATCH, %d not reflected", ok, bad, missing);
}

// One-time dump of the game mode's / game state's own Havoc rule settings
// (reflected names from the class registrations; values are the BP defaults
// of GameMode_Havoc_BP / GameState_Havoc_BP plus DefaultHavoc.ini). The
// director should follow these rather than its own guesses.
static void HavocLogRules(uint8_t *gm, uint8_t *gs) {
  static const char *const kGm[] = {"m_firstWaveDelay", "m_automaticallyStartFirstWave", "m_waveStartDelayDuration",
                                    "m_waveStartDuration", "m_waveEndDuration", "m_waveCompleteDuration",
                                    "m_upgradeSelectionDuration", "m_monsterKillXP", "m_waveScoreXPMultiplier",
                                    "m_killFighterJetsOnWaveEnd", "m_refillPlayerTeamRespawnsAfterWave",
                                    "m_respawnsToRefill", "m_forceRespawnInWave", "m_respawningWaveTime",
                                    "m_respawningWaveGracePeriodTime", "m_canPlayersRespawnAsCapitalShipsAfterWave",
                                    "m_maxPlayersPerTeam"};
  static const char *const kGs[] = {"m_maxNumberOfWaves", "m_maxWaveEnemies", "m_waveRespawningTime",
                                    "m_allowCancelWaveSpawning", "m_useWaveSpawning", "m_endOfMatchDuration",
                                    "m_maxModifiers", "m_pveDifficulty"};
  for (int pass = 0; pass < 2; ++pass) {
    uint8_t *o = pass ? gs : gm;
    const char *const *names = pass ? kGs : kGm;
    int n = pass ? (int)(sizeof kGs / sizeof *kGs) : (int)(sizeof kGm / sizeof *kGm);
    for (int i = 0; i < n; ++i) {
      int off = PropOffset((UObjectMin *)o, names[i]);
      if (off < 0 || !IsReadable(o + off, 4)) {
        Logf("havoc: rule %s.%s not reflected", ClassNameOf(o), names[i]);
        continue;
      }
      Logf("havoc: rule %s.%s +0x%X = int %d / float %g / byte %u", ClassNameOf(o), names[i], off,
           *(int32_t *)(o + off), *(float *)(o + off), o[off]);
    }
  }
}

static void HavocDirectorTick(uint8_t *gm) {
  static void *s_for = nullptr;
  static int s_on = 0, s_phase = -1, s_wave = 0, s_secs = 0, s_waitStart = 0, s_peakAlive = 0;
  if (s_for != gm) {
    s_for = gm;
    s_on = ClassChainContains((UObjectMin *)gm, "YGameMode_Havoc") &&
           !SwitchOn("DN_HOST_NO_HAVOC_DIRECTOR", "dn_host_no_havoc_director.txt");
    s_phase = -1, s_wave = 0, s_secs = 0, s_waitStart = 0, s_peakAlive = 0;
    if (s_on)
      Logf("havoc: director on for %p (start delay %d s, complete %.0f s, upgrade %d s)", gm,
           *(int32_t *)(gm + OFF_HVC_GM_WAVE_START_DELAY), *(float *)(gm + OFF_HVC_GM_WAVE_COMPLETE_DUR),
           *(int32_t *)(gm + OFF_HVC_GM_UPGRADE_DUR));
  }
  if (!s_on || !IsReadable(gm + OFF_GM_GAMESTATE, 8))
    return;
  uint8_t *gs = *(uint8_t **)(gm + OFF_GM_GAMESTATE);
  if (!IsReadable(gs, OFF_HVC_GS_HORDE_MANAGER + 8) || !ClassChainContains((UObjectMin *)gs, "YGameState_Havoc"))
    return;
  static void *s_checked = nullptr;
  if (s_checked != gs) {
    s_checked = gs;
    HavocCheckOffsets(gm, gs);
    HavocLogRules(gm, gs);
    {
      void **vt = *(void ***)gm;
      Logf("havoc: game mode vtable +0x870 (SpawnPlayer's spawn gate) -> RVA 0x%llX",
           (unsigned long long)((uintptr_t)vt[0x870 / 8] - g_base));
    }
  }
  uintptr_t base = (uintptr_t)GetModuleHandleA(nullptr);
  auto enterState = (void (*)(uint8_t *, uint8_t))(base + RVA_HVC_ENTER_NEW_STATE);
  auto waveChanged = (void (*)(uint8_t *))(base + RVA_HORDE_WAVE_STATE_CHANGED);
  {
    int listed = HavocCountAliveEnemies(gm, gs);
    if (listed >= 0)
      *(int32_t *)(gs + OFF_HVC_GS_NUM_ALIVE) = listed;
  }
  int alive = *(int32_t *)(gs + OFF_HVC_GS_NUM_ALIVE);
  auto go = [&](int havoc, int horde) {
    s_phase = havoc, s_secs = 0;
    gs[OFF_HVC_GS_HAVOC_STATE] = (uint8_t)havoc;
    if (horde >= 0) {
      *(int32_t *)(gs + OFF_HVC_GS_WAVE_INDEX) = s_wave;
      gs[OFF_HVC_GS_WAVE_STATE] = (uint8_t)horde;
      waveChanged(gs);
      // The horde manager's YHordeEventManager::OnWaveStateChanged is a
      // native-called event; its caller went with the server code.
      CallWaveEvent(*(void **)(gs + OFF_HVC_GS_HORDE_MANAGER), L"OnWaveStateChanged", horde, s_wave);
    }
    enterState(gs, (uint8_t)havoc);
    void *hmGm = IsReadable(*(uint8_t **)(gs + OFF_HVC_GS_HORDE_MANAGER), OFF_HVC_HM_GAME_MODE + 8)
                     ? *(void **)(*(uint8_t **)(gs + OFF_HVC_GS_HORDE_MANAGER) + OFF_HVC_HM_GAME_MODE) : nullptr;
    Logf("havoc: wave %d -> havoc state %d, horde wave state %d (alive %d, killed %d, max %d, EventStarted %d, "
         "hm.GameMode %p)", s_wave, havoc, horde, *(int32_t *)(gs + OFF_HVC_GS_NUM_ALIVE), *(int32_t *)(gs + OFF_HVC_GS_KILLED),
         *(int32_t *)(gs + OFF_HVC_GS_MAX_WAVE_ENEMIES), gs[OFF_HVC_GS_EVENT_STARTED], hmGm);
  };
  ++s_secs;
  HavocApplyBoosts();
  HavocApplyModifiers();
  // Game over: the run is reported, the clients get their scores, and the
  // match ends 6 s later (GameState_Havoc_BP's outro is 4 s).
  if (s_phase == 4) {
    if (s_secs == 2 || s_secs == 4)
      HavocSendRewards(gs, "game over, resend");
    if (s_secs == 6) {
      int win = g_hvcBpEnd == 1;
      gs[OFF_GS_WINNING_TEAM] = (uint8_t)(win ? 1 : 2);
      Logf("havoc: ending the match (%s, wave %d, waves reached %d, team XP %d)", win ? "won" : "lost", s_wave,
           *(int32_t *)(gs + OFF_HVC_GS_WAVES_REACHED), *(int32_t *)(gs + OFF_HVC_GS_TOTAL_XP));
      void **vt = *(void ***)gm;
      ((void(__fastcall *)(void *))vt[VT_GM_END_MATCH / 8])(gm);
    }
    return;
  }
  auto gameOver = [&](const char *why) {
    Logf("havoc: GAME OVER at wave %d (%s)", s_wave, why);
    HavocSendTimeToNextWave(gs, -1);
    HavocEndWaveModifiers();
    for (auto &e : g_hvcEliminated)
      e = nullptr;
    s_phase = 4, s_secs = 0;
    gs[OFF_HVC_GS_HAVOC_STATE] = 4;
    enterState(gs, 4);
    HavocReportRuns(gs);
    HavocSendRewards(gs, "game over");
  };
  static int s_smokeEnd = -1;
  if (s_smokeEnd < 0)
    s_smokeEnd = SwitchOn("DN_HOST_HAVOC_SMOKE", "dn_host_havoc_smoke.txt") ? 1 : 0;
  if (s_smokeEnd && s_wave >= 3 && s_phase == 0 && s_secs >= 5) {
    HavocVerifyShipClasses();
    gameOver("smoke test switch after wave 3");
    return;
  }
  if (s_phase >= 0 && g_hvcBpEnd) {
    gameOver(g_hvcBpEnd == 1 ? "the horde manager called PlayersWin" : "the BP called PlayersLose");
    return;
  }
  // Lost: no connected player has a living ship for 3 s during a wave.
  static int s_downSecs = 0;
  if (s_phase == 2)
    HavocTrackEliminations(gs);
  if (s_phase >= 0) {
    HavocSendTeamInfo(gs, false);
    HavocSendWaveProgress(gs, s_phase);
  }
  if (s_phase == 1 || s_phase == 2) {
    int humans = 0, up = HavocPlayersAlive(gs, &humans);
    s_downSecs = up == 0 ? s_downSecs + 1 : 0;
    if (s_downSecs == 1)
      Logf("havoc: no living player ship (%d connected) -- game over in 3 s unless one comes back", humans);
    if (s_downSecs >= 3) {
      s_downSecs = 0;
      gameOver("every player's ship was destroyed during the wave");
      return;
    }
  } else
    s_downSecs = 0;
  if (s_phase < 0) {
    // Wave 1 starts m_firstWaveDelay after the match starts: the game mode's
    // own value, 18 s in GameMode_Havoc_BP (read 2026-10-19 by reflection;
    // the PR waited a guessed 20 s). GUESS: the trigger. The BP also has
    // m_automaticallyStartFirstWave = false, so the original server may have
    // waited for something else (a ready-up?) before counting.
    // Test switch for a headless host with no player (dn_host_havoc_smoke.txt):
    // start without m_isMatchStarted and, after wave 3, force game over --
    // exercises modifiers, enemy boosts, the multicast and EndMatch. Use with
    // dn_host_havoc_fast_waves.txt.
    static int s_smoke = -1;
    if (s_smoke < 0)
      s_smoke = SwitchOn("DN_HOST_HAVOC_SMOKE", "dn_host_havoc_smoke.txt") ? 1 : 0;
    if (!gs[OFF_HVC_GS_IS_MATCH_STARTED] && !s_smoke)
      return;
    if (++s_waitStart == 1) {
      UObjectMin *hm = *(UObjectMin **)(gs + OFF_HVC_GS_HORDE_MANAGER);
      UObjectMin *em = IsReadable(gm + OFF_HVC_GM_PVE_EVENT_MANAGER, 8) ? *(UObjectMin **)(gm + OFF_HVC_GM_PVE_EVENT_MANAGER) : nullptr;
      Logf("havoc: match started; HordeManager %p %s, event manager %p %s", hm, ClassNameOf(hm), em, ClassNameOf(em));
      // The PvE start the server build would have run: register the map's
      // horde manager as the event manager, then OnPVEModeStarted. GameState
      // _Havoc_BP's EventStarted and the manager's wave spawning hang off it.
      Logf("havoc: before start: hm.GameMode %p, hm.Waves %d, bound OnPVEModeStarted %d, "
           "OnHordeWaveStateChanged %d", IsReadable(hm, OFF_HVC_HM_GAME_MODE + 8) ? *(void **)((uint8_t *)hm + OFF_HVC_HM_GAME_MODE) : nullptr,
           DelegateBound((uint8_t *)hm + OFF_HVC_HM_WAVES), DelegateBound(gm + OFF_HVC_GM_ON_PVE_MODE_STARTED),
           DelegateBound(gs + OFF_HVC_GS_ON_WAVE_STATE_CHANGED));
      if (hm && ClassChainContains(hm, "YPVEEventManager")) {
        void **csm = (void **)((uint8_t *)hm + OFF_PVE_EM_COMBAT_SCENE_MANAGER);
        void *scene = ((tGetSceneManager)(g_base + RVA_COMBAT_SCENE_MANAGER))();
        Logf("havoc: hm.m_combatSceneManager %p, world scene manager %p %s", *csm, scene,
             scene ? ClassNameOf((UObjectMin *)scene) : "");
        if (!*csm && scene)
          *csm = scene;
        // PvE difficulty. PVE_EnemyGroupSpawn_Component picks the enemy name by
        // YGameState_PVE::GetPVEDifficulty (1 Recruit / 2 Veteran / 3 Legendary);
        // 0 has no case, so PawnIdToSpawn stays None and the group stalls with
        // GroupSpawnInProgress set (m_pveDifficulty is 0 on the host). First the manager's own native InitializePVEDifficulty (real
        // code, 0x75D7A0 -> vtable +0x108); if that leaves 0, the manager's
        // m_previewDifficulty -- the map's own value, 1 on HVC_Map1.
        // GUESS: the server build's source of the lobby-chosen difficulty is unknown.
        int dOff = PropOffset((UObjectMin *)gs, "m_pveDifficulty");
        int pOff = PropOffset(hm, "m_previewDifficulty");
        if (dOff > 0 && !gs[dOff]) {
          bool called = CallNoParms(hm, L"InitializePVEDifficulty");
          int after = gs[dOff];
          if (!gs[dOff] && pOff > 0 && ((uint8_t *)hm)[pOff])
            gs[dOff] = ((uint8_t *)hm)[pOff];
          Logf("havoc: pve difficulty: InitializePVEDifficulty called %d -> %d; now %d (preview %d)", called, after,
               gs[dOff], pOff > 0 ? ((uint8_t *)hm)[pOff] : -1);
        }
        // Start the combat scene manager. Its tick (0x2693A0) spawns entries only
        // while +0x444 is 0; BeginPlay (0x23B83D) sets it to 1 when the net mode
        // is 1, and only AYAICombatSceneManager::StartCombat (0x2681A0) clears it
        // -- it also schedules each queued entry's spawn time (+0x00 = world time
        // + the summed +0x80 delays). The game calls it only from the game mode's
        // team fill (0x381FA0, at 0x3822C6, notify=false), which Havoc never
        // runs, so AddNPCSpawnByDataset entries (time 0, pawn class resolved by
        // the tick from the dataset id at +0x18 via 0x24FD00) sat unspawned.
        // GUESS: where the server build started combat in PvE is unknown; this
        // makes the same call the game makes, once, at PvE start.
        if (scene && IsReadable((uint8_t *)scene + OFF_SCENE_COMBAT_NOT_STARTED, 1)) {
          uint8_t before = ((uint8_t *)scene)[OFF_SCENE_COMBAT_NOT_STARTED];
          int queued = *(int32_t *)((uint8_t *)scene + OFF_SCENE_NPCS_COUNT);
          if (before)
            ((tSceneStartCombat)(g_base + RVA_SCENE_START_COMBAT))(scene, 0);
          Logf("havoc: scene manager StartCombat: not-started flag %u -> %u (%d entries queued)", before,
               ((uint8_t *)scene)[OFF_SCENE_COMBAT_NOT_STARTED], queued);
        }
        if (!em)
          *(UObjectMin **)(gm + OFF_HVC_GM_PVE_EVENT_MANAGER) = hm;
        if (!*(void **)(gs + OFF_HVC_GS_PVE_EVENT_MANAGER))
          *(UObjectMin **)(gs + OFF_HVC_GS_PVE_EVENT_MANAGER) = hm;
        if (DelegateBound(gm + OFF_HVC_GM_ON_PVE_MODE_STARTED) > 0) {
          ((void (*)(void *, void *))(base + RVA_MULTICAST_BROADCAST_NOPARMS))(gm + OFF_HVC_GM_ON_PVE_MODE_STARTED, nullptr);
          Logf("havoc: OnPVEModeStarted broadcast");
        } else {
          bool a = CallNoParms(hm, L"OnPVEModeStarted");
          bool b = CallNoParms(gs, L"OnPVEModeStarted_Event");
          Logf("havoc: OnPVEModeStarted unbound -- called directly: manager %d, game state %d", a, b);
        }
        Logf("havoc: after start: EventStarted %d, bound OnHordeWaveStateChanged %d, hm.Waves %d",
             gs[OFF_HVC_GS_EVENT_STARTED], DelegateBound(gs + OFF_HVC_GS_ON_WAVE_STATE_CHANGED),
             DelegateBound((uint8_t *)hm + OFF_HVC_HM_WAVES));
      }
    }
    static int s_firstDelay = -1;
    if (s_firstDelay < 0) {
      int off = PropOffset((UObjectMin *)gm, "m_firstWaveDelay");
      float f = off > 0 && IsReadable(gm + off, 4) ? *(float *)(gm + off) : -1.0f;
      s_firstDelay = f >= 1.0f && f <= 300.0f ? (int)(f + 0.5f) : 20;
      Logf("havoc: first wave %d s after the match start (m_firstWaveDelay %g)", s_firstDelay, f);
    }
    if (s_waitStart < s_firstDelay) {
      HavocSendTimeToNextWave(gs, s_firstDelay - s_waitStart);
      return;
    }
    s_wave = 1;
    HavocStartWaveModifiers(gs, s_wave);
    go(1, 1);
    return;
  }
  if (alive > s_peakAlive)
    s_peakAlive = alive;
  if (s_secs % 10 == 0)
    Logf("havoc: wave %d state %d t+%d s: alive %d (peak %d), killed %d, max %d", s_wave, s_phase, s_secs, alive,
         s_peakAlive, *(int32_t *)(gs + OFF_HVC_GS_KILLED), *(int32_t *)(gs + OFF_HVC_GS_MAX_WAVE_ENEMIES));
  switch (s_phase) {
  case 1: // WaveStarting
    if (s_secs >= HvcMax(3, *(int32_t *)(gm + OFF_HVC_GM_WAVE_START_DELAY))) {
      s_peakAlive = 0;
      *(int32_t *)(gs + OFF_HVC_GS_KILLED) = 0;
      *(int32_t *)(gs + OFF_HVC_GS_PLAYER_WAVE_XP) = 0;
      InterlockedExchange(&g_hvcWaveConditions, 0);
      go(2, 2);
    }
    break;
  case 2: { // WaveInProgress
    // GUESS: 15 s grace for the spawn-in; 120 s with nothing alive = nothing spawns.
    // Test switch for unattended runs (nobody kills the enemies): the wave
    // ends after 40 s with the 750 XP GameMode_Havoc_BP grants at a clear.
    static int s_fastWaves = -1;
    if (s_fastWaves < 0)
      s_fastWaves = SwitchOn("DN_HOST_HAVOC_FAST_WAVES", "dn_host_havoc_fast_waves.txt") ? 1 : 0;
    bool fastEnd = s_fastWaves && s_secs >= 40;
    if (fastEnd) {
      Logf("havoc: fast-waves test switch: ending wave %d with %d alive", s_wave, alive);
      HavocAddXP(gm, 750, "fast-waves test switch");
    }
    // Over when GameState_Havoc_BP says so: SetSpecialWaveConditionsFulfilled
    // (true) after its CheckWaveOver -- enemies dead and every XP drop picked
    // up or expired (HookBoolParmExecThunk). GUESS fallbacks: 60 s after the
    // last listed enemy died (a drop lives 40 s), or 120 s with nothing
    // spawned. m_wavesReached is NOT written here: IncrementWavesReached is
    // real code, called by the BP's OnEnterWaveStartingState.
    static int s_clearSecs = 0;
    s_clearSecs = s_peakAlive > 0 && alive == 0 ? s_clearSecs + 1 : 0;
    bool bpOver = g_hvcWaveConditions != 0;
    if (fastEnd || bpOver || s_clearSecs >= 60 || (s_peakAlive == 0 && s_secs >= 120)) {
      Logf("havoc: wave %d over after %d s (%s)", s_wave, s_secs,
           fastEnd ? "fast-waves test switch" : bpOver ? "BP: SetSpecialWaveConditionsFulfilled"
           : s_peakAlive ? "fallback: 60 s after the last kill" : "fallback: nothing spawned");
      s_clearSecs = 0;
      *(int32_t *)(gs + OFF_HVC_GS_WAVE_DURATION) = s_secs;
      HavocEndWaveModifiers();
      HavocReleaseEliminated();
      go(3, 3);
    }
    break;
  }
  case 3: // WaveComplete
    if (s_secs >= HvcMax(3, (int)*(float *)(gm + OFF_HVC_GM_WAVE_COMPLETE_DUR))) {
      HavocClearReady(gs);
      go(0, -1);
      HavocOfferUpgrades(gs);
    }
    break;
  case 0: { // UpgradeSelection
    int ready = 0, humans = HavocHumanCount(gs, &ready);
    bool allReady = humans > 0 && ready >= humans && s_secs >= 3;
    if (allReady)
      Logf("havoc: all %d human(s) ready after %d s -- starting wave %d", humans, s_secs, s_wave + 1);
    if (allReady || s_secs >= HvcMax(5, *(int32_t *)(gm + OFF_HVC_GM_UPGRADE_DUR))) {
      // The next wave follows the game state's index: at the last wave of
      // Waves, PVE_HordeManager_BP sets it back to WaveToLoopBackTo - 1
      // (AYGameState_Havoc::SetWaveIndex, real code) and the run loops.
      int gsWave = *(int32_t *)(gs + OFF_HVC_GS_WAVE_INDEX);
      if (gsWave != s_wave)
        Logf("havoc: the horde manager moved the wave index %d -> %d (loop)", s_wave, gsWave);
      s_wave = gsWave + 1;
      HavocStartWaveModifiers(gs, s_wave);
      HavocClearReady(gs);
      go(1, 1);
    }
    break;
  }
  }
  // The countdown to the next WaveStarting: WaveComplete + UpgradeSelection;
  // -1 (hidden) otherwise.
  int upgradeSecs = HvcMax(5, *(int32_t *)(gm + OFF_HVC_GM_UPGRADE_DUR));
  int completeSecs = HvcMax(3, (int)*(float *)(gm + OFF_HVC_GM_WAVE_COMPLETE_DUR));
  HavocSendTimeToNextWave(gs, s_phase == 0 ? upgradeSecs - s_secs
                              : s_phase == 3 ? completeSecs - s_secs + upgradeSecs : -1);
}

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

  if (MH_CreateHook((LPVOID)processEvent, (LPVOID)&HookProcessEvent,
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
    Logf("post-login: ProcessEvent hooked at %p; players keep ship selection "
         "and the normal orbit flow.",
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
  InstallHostCrashHandler();

  if (MH_Initialize() != MH_OK) {
    Logf("MH_Initialize failed. Standing down.");
    return 0;
  }

  void *target = (void *)(g_base + RVA_FIND_LOADOUT_BY_ID);
  if (MH_CreateHook((LPVOID)target, (LPVOID)&HookFindLoadoutByID,
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
    if (MH_CreateHook((LPVOID)nm, (LPVOID)&HookGetNetMode, (LPVOID *)&g_origGetNetMode) != MH_OK ||
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
  InstallSwitchedHook("restrictions (ClientSetPlayerRestrictions RPC, local controller)",
                      RVA_CLIENT_SET_PLAYER_RESTRICTIONS_RPC,
                      (void *)&HookClientSetPlayerRestrictionsRPC,
                      (void **)&g_origClientSetPlayerRestrictionsRPC);

  InstallSwitchedHook("respawn filter (ClientApplyRespawnFilter RPC, re-entrancy guard)",
                      RVA_CLIENT_APPLY_RESPAWN_FILTER_RPC,
                      (void *)&HookClientApplyRespawnFilter,
                      (void **)&g_origClientApplyRespawnFilter);

  // On by default: without it every score is 0. Opt out to diagnose.
  if (!SwitchOn("DN_HOST_NO_SCORING", "dn_host_no_scoring.txt")) {
    InstallSwitchedHook("scoring table (UYScoringEventManager::InitializeData)",
                        RVA_SCORING_INIT_DATA, (void *)&HookScoringInitData,
                        (void **)&g_origScoringInitData);
    InstallSwitchedHook("scoring mode log (scoring table copy)", RVA_SCORING_COPY,
                        (void *)&HookScoringCopy, (void **)&g_origScoringCopy);
  } else
    Logf("scoring: OFF (dn_host_no_scoring.txt / DN_HOST_NO_SCORING=1). Every "
         "score stays 0.");

  if (TerScoringEnabled()) {
    InstallSwitchedHook("conquest scoring manager (game mode creator A)", RVA_GM_CREATE_SCORING_MGR_A,
                        (void *)&HookCreateScoringMgrA, (void **)&g_origCreateScoringMgrA);
    InstallSwitchedHook("conquest scoring manager (game mode creator B)", RVA_GM_CREATE_SCORING_MGR_B,
                        (void *)&HookCreateScoringMgrB, (void **)&g_origCreateScoringMgrB);
  } else
    Logf("conquest scoring: OFF (dn_host_no_ter_scoring.txt / DN_HOST_NO_TER_SCORING=1)");
  if (TerScoringEnabled() || ConquestPointOwnerEnabled())
    InstallSwitchedHook("conquest capture (YCapturePoint::OnCaptured: point owner + capture score)",
                        RVA_CP_ON_CAPTURED, (void *)&HookCpOnCaptured, (void **)&g_origCpOnCaptured);
  if (!ConquestPointOwnerEnabled())
    Logf("conquest: captured points' territory OFF (dn_host_no_conquest_point_owner.txt / "
         "DN_HOST_NO_CONQUEST_POINT_OWNER=1)");
  if (ConquestSpawnsEnabled() || TeSpawnsEnabled())
    InstallSwitchedHook("conquest / te spawns (YGameMode player-start chooser)", RVA_GM_CHOOSE_PLAYER_START,
                        (void *)&HookChoosePlayerStart, (void **)&g_origChoosePlayerStart);
  if (!ConquestSpawnsEnabled())
    Logf("conquest spawns: OFF (dn_host_no_conquest_spawns.txt / DN_HOST_NO_CONQUEST_SPAWNS=1)");
  if (!TeSpawnsEnabled())
    Logf("te spawns: OFF (dn_host_no_te_spawns.txt / DN_HOST_NO_TE_SPAWNS=1)");

  if (!SwitchOn("DN_HOST_NO_EOM_STATS", "dn_host_no_eom_stats.txt"))
    InstallSwitchedHook("eom stats (ClientStartEndOfMatchTransition)",
                        RVA_CLIENT_START_EOM_TRANSITION,
                        (void *)&HookClientStartEomTransition,
                        (void **)&g_origClientStartEomTransition);
  else
    Logf("eom stats: OFF (dn_host_no_eom_stats.txt / DN_HOST_NO_EOM_STATS=1). "
         "Clients will stop on a black screen at SetupUIWidgets.");

  // The game-mode timer carries the proving-ground bots AND the per-second
  // team/name/bot-balance pass (PlayersTick), so it installs if either is on.
  g_bcAIArmed = BootcampAIEnabled();
  g_botsBCOnly = SwitchOn("DN_HOST_BOTS_BC_ONLY", "dn_host_bots_bc_only.txt");
  g_teamSyncArmed = !SwitchOn("DN_HOST_NO_TEAM_SYNC", "dn_host_no_team_sync.txt");
  if (!g_bcAIArmed)
    Logf("bc ai: OFF (create dn_host_bc_ai.txt beside the executable, or set "
         "DN_HOST_BC_AI=1). The proving ground has no bots.");
  Logf("team sync / names / bot balance: %s", g_teamSyncArmed ? "ON (game-mode timer)"
       : "OFF (dn_host_no_team_sync.txt / DN_HOST_NO_TEAM_SYNC=1)");
  if (g_bcAIArmed || g_teamSyncArmed)
    InstallSwitchedHook("game-mode timer (bc ai, teams, names, bot balance)",
                        RVA_GAMEMODE_MP_TIMER, (void *)&HookGameModeTimer,
                        (void **)&g_origGameModeTimer);

  if (!SwitchOn("DN_HOST_NO_EVENT_XP", "dn_host_no_event_xp.txt"))
    InstallSwitchedHook("original match xp (AssignScoringReward)", RVA_ASSIGN_SCORING_REWARD,
                        (void *)&HookAssignScoringReward, (void **)&g_origAssignScoringReward);
  else
    Logf("event xp: OFF (dn_host_no_event_xp.txt / DN_HOST_NO_EVENT_XP=1). mmogbrain "
         "pays its formula XP.");

  Logf("onslaught creeps: %s", OnslaughtCreepsEnabled() ? "ON (game-mode timer; Onslaught hosts only)"
       : "OFF (dn_host_no_onslaught_creeps.txt / DN_HOST_NO_ONSLAUGHT_CREEPS=1)");
  bool onsPoints = !SwitchOn("DN_HOST_NO_ONSLAUGHT_POINTS", "dn_host_no_onslaught_points.txt");
  g_onsPointsOn = onsPoints;
  Logf("te rounds: %s", TeRoundsOn() ? "ON (Team Elimination hosts: best of 3, fighters after a lost ship)"
       : "OFF (dn_host_no_te_rounds.txt / DN_HOST_NO_TE_ROUNDS=1): Team Elimination plays as a deathmatch");
  if (TeRoundsOn() && HostIsTE()) {
    InstallSwitchedHook("te fighters while eliminated (AYPlayerController::SpawnPlayer)", RVA_SPAWN_PLAYER,
                        (void *)&HookTeSpawnPlayer, (void **)&g_origTeSpawnPlayer);
    InstallSwitchedHook("te ready-up (ServerReadyUpPlayerForNextWave exec)", RVA_EXEC_SERVER_READY_NEXT_WAVE,
                        (void *)&HookExecReadyNextWave, (void **)&g_origExecReadyNextWave);
    InstallSwitchedHook("te un-ready (ServerUnReadyPlayerForNextWave exec)", RVA_EXEC_SERVER_UNREADY_NEXT_WAVE,
                        (void *)&HookExecUnReadyNextWave, (void **)&g_origExecUnReadyNextWave);
    Logf("te: winners keep their ship %s; team HUD %s", TeWinnerLockOn() ? "ON" : "OFF (dn_host_no_te_winner_lock.txt)",
         TeHudOn() ? "ON" : "OFF (dn_host_no_te_hud.txt)");
  }
  // Both modes ride on the same two hooks; each acts only on its own mode.
  if (onsPoints || TeRoundsOn()) {
    InstallSwitchedHook("onslaught points + te ships lost (TDM Killed)", RVA_TDM_KILLED,
                        (void *)&HookTdmKilled, (void **)&g_origTdmKilled);
    InstallSwitchedHook("winner by points / rounds (TDM leader)", RVA_TDM_DETERMINE_LEADER,
                        (void *)&HookDetermineLeader, (void **)&g_origDetermineLeader);
  }
  if (!onsPoints)
    Logf("onslaught points: OFF (dn_host_no_onslaught_points.txt / "
         "DN_HOST_NO_ONSLAUGHT_POINTS=1). Onslaught ends at 30 kills.");

  if (!SwitchOn("DN_HOST_NO_ONS_GUN_ROWS", "dn_host_no_ons_gun_rows.txt"))
    InstallSwitchedHook("command ship gun rows (LoadWeaponDataTableRow)", RVA_WEAPON_GROUP_LOAD_ROW,
                        (void *)&HookWeaponGroupLoadRow, (void **)&g_origWeaponGroupLoadRow);

  if (!HostIsTurboTDM())
    ;
  else if (SwitchOn("DN_HOST_NO_GAME_MODIFIERS", "dn_host_no_game_modifiers.txt"))
    Logf("turbo modifiers: OFF (dn_host_no_game_modifiers.txt / "
         "DN_HOST_NO_GAME_MODIFIERS=1). Turbo TDM plays as plain TDM.");
  else
    g_featsTurbo = true;

  if (!HostIsHavoc())
    ;
  else if (SwitchOn("DN_HOST_NO_HAVOC_DIRECTOR", "dn_host_no_havoc_director.txt"))
    Logf("havoc: OFF (dn_host_no_havoc_director.txt / DN_HOST_NO_HAVOC_DIRECTOR=1). "
         "Fighters fight, but no wave ever starts.");
  else {
    g_featsHavoc = true;
    InstallSwitchedHook("havoc director (compiled-out native exec thunk)", RVA_EMPTY_EXEC_THUNK,
                        (void *)&HookEmptyExecThunk, (void **)&g_origEmptyExecThunk);
    InstallSwitchedHook("havoc enemy list + wave xp (compiled-out int-parm exec thunk)", RVA_INT_PARM_EXEC_THUNK,
                        (void *)&HookIntParmExecThunk, (void **)&g_origIntParmExecThunk);
    InstallSwitchedHook("havoc kill xp (compiled-out int+object exec thunk)", RVA_INT_OBJ_PARM_EXEC_THUNK,
                        (void *)&HookIntObjParmExecThunk, (void **)&g_origIntObjParmExecThunk);
    InstallSwitchedHook("havoc wave over (compiled-out bool-parm exec thunk)", RVA_BOOL_PARM_EXEC_THUNK,
                        (void *)&HookBoolParmExecThunk, (void **)&g_origBoolParmExecThunk);
    InstallSwitchedHook("havoc ready up (ServerReadyUpPlayerForNextWave exec thunk)", RVA_HVC_READY_UP_THUNK,
                        (void *)&HookHavocReadyUpThunk, (void **)&g_origHavocReadyUpThunk);
    InstallSwitchedHook("havoc unready (ServerUnReadyPlayerForNextWave exec thunk)", RVA_HVC_UNREADY_THUNK,
                        (void *)&HookHavocUnReadyThunk, (void **)&g_origHavocUnReadyThunk);
    InstallSwitchedHook("havoc upgrade pick (ServerReceiveSelectedUpgradeIndex_Implementation)",
                        RVA_HVC_SERVER_SELECTED_UPGRADE_IMPL, (void *)&HookHvcSelectedUpgrade,
                        (void **)&g_origHvcSelectedUpgrade);
    if (HavocRespawnBlockOn())
      InstallSwitchedHook("havoc respawn block (game mode spawn gate, SpawnPlayer)", RVA_GM_SPAWN_GATE,
                          (void *)&HookSpawnGate, (void **)&g_origSpawnGate);
    else
      Logf("havoc respawn block: OFF (dn_host_no_havoc_respawn_block.txt / DN_HOST_NO_HAVOC_RESPAWN_BLOCK=1)");
    if (!HavocShipUpgradesOn())
      Logf("havoc ship upgrades: OFF (dn_host_no_havoc_ship_upgrades.txt / DN_HOST_NO_HAVOC_SHIP_UPGRADES=1)");
    // Not verified in PR #80 either: own switch, to bisect without a rebuild.
    if (SwitchOn("DN_HOST_NO_HAVOC_RESPAWN_GATE", "dn_host_no_havoc_respawn_gate.txt"))
      Logf("havoc respawn ship gate: OFF (dn_host_no_havoc_respawn_gate.txt / "
           "DN_HOST_NO_HAVOC_RESPAWN_GATE=1). A respawn pick keeps the old ship.");
    else {
      InstallSwitchedHook("havoc respawn ship gate (UYLoadoutManager loadout validity)", RVA_LOADOUT_IS_REGISTERED,
                          (void *)&HookLoadoutGate, (void **)&g_origLoadoutGate);
      InstallSwitchedHook("havoc respawn ship (UYLoadoutManager::ActivateLoadout)", RVA_ACTIVATE_LOADOUT,
                          (void *)&HookActivateLoadout, (void **)&g_origActivateLoadout);
    }
  }

  if (g_featsTurbo || g_featsHavoc)
    InstallSwitchedHook(g_featsTurbo ? "turbo modifiers (UYFeatsComponent setup)"
                                     : "havoc boosts (UYFeatsComponent setup)",
                        RVA_FEATS_COMPONENT_BEGIN, (void *)&HookFeatsBegin, (void **)&g_origFeatsBegin);

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
