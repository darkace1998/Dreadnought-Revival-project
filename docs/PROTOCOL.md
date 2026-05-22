# Dreadnought Revival Project — Protocol Documentation

> Derived from reverse engineering analysis in `/src/Documents/` (Sessions 1–7)

## Overview

Dreadnought uses a hybrid TCP/UDP networking stack built on Unreal Engine 4, extended with
**YMmogbrain** — Yager's proprietary backend system. This document records everything needed
to emulate those services.

---

## API Endpoints (Launcher → Backend)

The launcher (`DreadnoughtLauncher.exe`) reads its backend URLs from an embedded ZIP archive
(offset 715650, password `8smGty7tRNVFDRQz2muTmG3p`) containing `configs/production.json`.

### Original Production Endpoints

| Service | Base URL |
|---|---|
| Profile API (auth) | `https://profile-api.prod.greybox.sixfoot.live` |
| Legacy API (game data) | `https://legacyapi.prod.greybox.sixfoot.live` |

### Private Server Replacements

| Endpoint | Method | Purpose | Handler |
|---|---|---|---|
| `/auth/` | POST | Login (JWT issuance) | auth-server :8081 |
| `/auth/register` | POST | New player registration | auth-server :8081 |
| `/auth/me` | GET | Token validation | auth-server :8081 |
| `/auth/logout` | POST | Session invalidation | auth-server :8081 |
| `/v2/dreadnought/launcher/dn/tiles/` | GET | Launcher news tiles | legacy-api :8082 |
| `/v2/dreadnought/ageconsent/` | GET | Age consent (always approved) | legacy-api :8082 |
| `/v2/dreadnought/player/{id}/profile` | GET | Player profile + stats | legacy-api :8082 |
| `/v2/dreadnought/player/{id}/inventory` | GET | Ships, modules, cosmetics | legacy-api :8082 |
| `/v2/dreadnought/match/result` | POST | Post-match stat recording | legacy-api :8082 |

---

## JWT Token Format

The Dreadnought client expects a JWT with these specific claims:

```json
{
  "sub": "<user_uuid>",
  "username": "<display_name>",
  "realm": "dreadnought.pc-us",
  "aud": "dreadnought",
  "iss": "Dreadnought-Revival-project",
  "iat": 1234567890,
  "exp": 1234654290
}
```

**Algorithm:** HS256 (HMAC-SHA256) for simplicity. Upgrade to RS256 for production.

---

## YMmogbrain Backend (Matchmaking)

YMmogbrain is Yager's proprietary backend. The game's `OnlineSubsystemMmogbrain` plugin
connects to it for matchmaking, lobby management, chat, and progression sync.

The plugin source is embedded as a UE4 plugin in the game binary. Key classes (from binary
string analysis and Ghidra decompilation):

- `YMmogClient` — Client-side plugin entry point
- `YFirmamentClient` — Cloud service client
- `YMmogChat` — Chat system
- `OnlineIdentityMmog` — Authentication integration
- `YGameMode_Multiplayer` — Networked multiplayer game mode
- `YGameMode_TeamDeathmatch` — TDM mode
- `YGameMode_Havoc` — PvE Havoc mode
- `YGameMode_Outpost` — Hub/lobby mode

### Private Server Matchmaking Endpoints (mmogbrain :8083)

| Endpoint | Method | Auth | Purpose |
|---|---|---|---|
| `/mmog/queue` | POST | Bearer | Join matchmaking queue |
| `/mmog/queue/status` | GET | Bearer | Poll for match assignment |
| `/mmog/queue` | DELETE | Bearer | Leave queue |
| `/mmog/match/{id}` | GET | Bearer | Get match details |
| `/mmog/chat` | POST | Bearer | Send chat message |
| `/mmog/chat` | GET | — | Get chat history |

---

## UE4 Wire Protocol

### Packet Header (17 bytes)

```
Offset 0-3:  uint32  Magic Number = 0x55453400 (ASCII "UE4\0")
Offset 4:    uint8   Packet Type  (see table)
Offset 5-6:  uint16  Sequence Number (little-endian)
Offset 7-8:  uint16  Acknowledgement Number
Offset 9-12: uint32  Connection ID
Offset 13:   uint8   Channel Index (0-5)
Offset 14:   uint8   Flags
Offset 15-16:uint16  Packet Size (total, header + payload)
```

### Packet Types

| ID | Name | Direction | Description |
|---|---|---|---|
| 0x00 | NMT_Control | Both | Connection control (connect/disconnect) |
| 0x01 | NMT_Ack | Both | Acknowledgement |
| 0x02 | NMT_Ping | Both | Keep-alive |
| 0x03 | NMT_OpenChannel | Both | Open a channel |
| 0x04 | NMT_CloseChannel | Both | Close a channel |
| 0x05 | NMT_Data | Both | Game data (RPCs, actor replication) |
| 0x06 | NMT_Voice | Both | Voice chat |

### Channels

| Index | Name | Reliability | Usage |
|---|---|---|---|
| 0 | Control | Reliable/Ordered | Connection management |
| 1 | Voice | Reliable/Ordered | Voice chat |
| 2 | Relevant (Actor) | Reliable/Ordered | Actor spawn/despawn |
| 3 | Unreliable | Unreliable/Unordered | Movement, damage, fast updates |
| 4 | Reliable | Reliable/Ordered | Critical game events |
| 5 | Low | Unreliable/Unordered | Background data |

---

## Game Engine (Dedicated Server) Configuration

The game binary supports dedicated server mode via command-line flags:

```bash
wine DreadGame-Win64-Shipping.exe \
  -dedicatedserver \
  -port=7777 \
  -maxplayers=10 \
  -GameMode=YGameMode_TeamDeathmatch \
  -Map=Charon \
  -MatchID=<uuid> \
  -nop4 \
  -nosound \
  -noeac \
  -NoSteam \
  -log=server.log
```

**Port:** 7777 UDP (default); game clients receive this via matchmaking response.

**EAC:** Pass `-noeac` to disable EasyAntiCheat in dedicated server mode.

---

## Port Reference

| Port | Protocol | Service |
|---|---|---|
| 80 | TCP | Gateway (HTTP → HTTPS redirect) |
| 443 | TCP | Gateway (TLS termination, all HTTPS traffic) |
| 8081 | TCP | Auth Server (internal) |
| 8082 | TCP | Legacy API (internal) |
| 8083 | TCP | YMmogbrain Emulator (internal) |
| 8084 | TCP | Master Server (internal) |
| 8085 | TCP | Game Instance Manager (internal) |
| 7777–7877 | UDP | Game Server Instances (one per active match) |
| 57005 | TCP | Crash Reporting (bugreports.greybox.com — can redirect to /dev/null) |

---

## DNS Redirect Setup

On client machines, add to `/etc/hosts` (Windows: `C:\Windows\System32\drivers\etc\hosts`):

```
<SERVER_IP>  profile-api.prod.greybox.sixfoot.live
<SERVER_IP>  legacyapi.prod.greybox.sixfoot.live
<SERVER_IP>  mmog.greybox.sixfoot.live
```

Or run `scripts/hosts-redirect.sh` on Linux, or add manually on Windows.

---

## Known Unknowns

- **YMmogbrain binary protocol:** The exact binary protocol between the game client's
  `YMmogClient` plugin and the YMmogbrain server is not fully documented. The HTTP API
  above is a best-effort emulation based on binary string analysis. Full protocol details
  require live packet capture between an unmodified client and the original servers (no
  longer available).

- **Session token pass-through:** How the game client passes the auth JWT to the dedicated
  game server instance is not confirmed. Possible mechanisms: command-line, config file,
  or a separate auth handshake. Requires testing with a live client.

- **Steam bypass:** The game uses `steam_api.dll` and may call Steamworks for player identity.
  The `-NoSteam` flag may not fully disable this. A Steamworks emulator (e.g. Goldberg)
  may be needed for the game binary to run without Steam.
