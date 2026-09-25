# Open issues

Known gaps that are understood but not fixed. Both entries below share a cause:
the values lived in the original mmogbrain / server build, not in anything the
client ships, so there is no source to take them from. Per the project's first
rule (never invent data), they stay open until real values are found.

Candidate sources, in order of preference: an archived copy of the original
mmogbrain configs, community wiki / forum snapshots, gameplay videos and
screenshots (score pop-ups, research and store panels). Record the source next
to every recovered value; anything still unknown stays `// GUESS:`.

---

## Tech tree: research and purchase costs are server-authored guesses

**Status:** open, working placeholders in place. GitHub #66.

- **Hull research (XP):** `techTreeXPCostByTier` in
  `mmogbrain/response_builders.go` is a ladder we chose
  (`1: 0, 2: 2500, 3: 7500, 4: 20000, 5: 50000`). No client asset or community
  reference states it.
- **Module research (XP):** `techTreeModuleXPCost` is `tier * 1000`, marked
  `GUESS:`. There is no per-module cost table in the client or in `data/`.
- **Purchase prices (credits):** `gatewayMarketCreditPrice(itemType, tier)`
  (25,000 for a Tier 1 hull, doubling per tier). The store and the charge now
  agree by construction (`purchasePriceForItemChecked`), but the ladder itself is
  not recovered data. `catalogPrices` holds about twenty researched per-item
  values and is where a real table would go.
- **Hard constraint (verified 2026-08-04):** the client's cost field renders at
  most 5 digits. A live screenshot showed "PURCHASE COST 99999" for a hull priced
  at 100000, so any recovered value of 100000 or more needs a decision on how to
  display it.

**Done when:** each tier/item cost comes from a cited source, or is explicitly
accepted as a design choice and documented as such.

---

## Match scores are always 0 (PvP scoring table missing)

**Status:** open. Found 2026-09-25. GitHub #67. Kills, deaths and K/D display correctly;
every player's score is 0 on the scoreboard and the end-of-match screen.

**Cause (verified from the exe and config):**

- Every scoring event (kill, assist, capture, ...; 76 `EYScoringEventID` values)
  is looked up in a scoring table. `UYScoringEventManager::InitializeData`
  (`0x423610`) builds it through `0x423450`, which copies the data from the
  **YMmogbrain subsystem** at `+0x43F8`/`+0x4408`/`+0x4460`, with a "present" flag
  at `+0x4470`. Only a logged-in game receives that from mmogbrain; the battle
  server never logs in, so its table is empty and every event is worth 0.
- `DreadGame/Config/DefaultScoring.ini`: the table was authored as a
  `YScoringAsset` and exported to the original mmogbrain
  (`m_mmogbrainExportPath = Source/Programs/mmogbrain/instances/dreadnought/ScoringTable.cfg`),
  which served it back to the games.
- The PvP values are **not in the shipped paks**. Only the PvE tables exist
  (`Generic/DataTables/PVE/*Scoring*`, extracted to `data/datatables/PVE/`).

**Work needed:**

1. Recover the point values (see the candidate sources above).
2. Find which mmog response fills `+0x43F8…+0x4470` on a client, and its format:
   no writer was found by offset, so it is likely a parser writing through a
   sub-struct pointer. Then either answer it from mmogbrain or have
   `battle-server-mod` fill the host's copy before `InitializeData` runs.

**Done when:** a kill in a live match adds the recovered value to the killer's
score on the scoreboard.

---

## Career goals (progression screen) are invented placeholders

**Status:** open, working placeholders in place. Found 2026-09-25. GitHub #68.

The main-menu progression screen is the career goals system (`YA_GetStaticCareerData` / `YA_GetCareerProgression`). Every goal it shows is authored by us in `mmogbrain/career_goals.go`: four goals with made-up titles ("Shakedown Cruise", "Hull Breaker", ...), made-up stage targets and credit rewards, and counter IDs the file itself calls "a best guess". Only `UnlockAllModes` is a real ID (the exe hardcodes it).

**What is recoverable (verified):**

- `DreadGame/Content/Localization/DreadGame/<lang>/DreadGame_MmogData.locres` holds all 375 player-facing texts of the original mmogbrain data, in 7 languages, keyed by GUID. It includes goal titles ("Know the Ropes", "Veteran Captain", "Legendary Captain", "Fabled Fleet", "Fame and Fortune", ...), counter names ("Matches played", "Matches won", "Enemy ships destroyed", "Daily Contracts completed", "T2 ships owned", "Fully researched T3 ships", "Tutorial finished", ...) and goal descriptions, including the one for `UnlockAllModes` ("Special helper goal to configure the amount of matches to be played to unlock all modes").
- `DreadGame/Config/Localization/ExtractNsLocTextDataFromMmogData.py` shows the original data carried texts as `NSLOCTEXT("namespace","key","source")` macros. Our goals now use that format (own namespace), which is what made names display at all.

**What is lost:** which title belongs to which goal ID, each goal's stages (`m_amountToComplete`), rewards (`m_reward`, `m_rewardType`), counters (`m_counterID`/`m_counterSubId`) and category. There are no goal assets in the paks, and no goal list in the exe beyond `UnlockAllModes`.

**Work needed:**

1. Find the original career goal list: wiki archives, forum posts, gameplay videos or screenshots of the career screen showing titles, targets and rewards.
2. Rebuild `careerGoalsConfig()` from it, sending each title and description as `NSLOCTEXT("", "<GUID>", "<source>")` with its real locres key, so the texts are translated by the client.
3. Match counter IDs to what the client's goal manager increments (not traced yet).

**Done when:** every goal on the progression screen is a recovered goal with a cited source, or is explicitly accepted as a design choice. Anything unrecovered stays marked `// GUESS:`.
