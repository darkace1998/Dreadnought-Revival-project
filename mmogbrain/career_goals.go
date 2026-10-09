package main

import (
	"strconv"
	"strings"

	"github.com/darkace1998/Dreadnought-Revival-project/mmogbrain/protocol"
)

// Career progression is a GOALS system, not the progression-item taxonomy we
// used to send. Ground truth comes from the client's own parsers:
//
//   - FYCareerProgressionConfig::Load (FUN_142a68120) reads a "CareerGoalsConfig"
//     array from the STATIC response. Per entry it registers exactly: m_id,
//     m_title, m_description, m_uiGuideAvailable, m_counterID, m_counterSubId,
//     m_category, m_platformVisibility, m_stageData; and per stage:
//     m_amountToComplete, m_reward, m_rewardType.
//   - The three enum fields are resolved with UEnum::GetValueByName against
//     EYGoalCategory / EYGoalPlatformVisibility / EYGoalRewardType, so they must
//     be sent as enum name strings, not ordinals — see the qualification note
//     below for the exact form.
//   - FYCareerProgressionData::Update (FUN_142a68f90) reads per-goal progress
//     entries of {goalId, progress, claimed_stage} from the DYNAMIC response,
//     and warns "goal %s does not exist in the current data" for ids missing
//     from the static config — so the two must agree on m_id.
//
// The previous payloads sent m_categories/m_categoryDTPath, which belong to
// UYPlayerMatchStatisticsManager (end-of-match statistics) — a different class
// whose m_categoryDTPath is even Config-driven, not wire-driven. The client
// therefore parsed our career data as empty and logged "Career progression
// [static] Data empty. Not initialized."
//
// Enum names are from the SDK dump (DreadGame_Structs.h):
//
// BOTH payloads are wrapped in "result", but in different shapes. The response
// dispatcher (FUN_142a21cf0) does GetField(response, L"result") and hands that
// node straight to the parser:
//
//   - static:  Load() then looks up "CareerGoalsConfig" by name on it, so
//     result is an OBJECT containing the array.
//   - dynamic: Update() reads the node's element count and walks its entries
//     directly, so result IS the array (the YA_PlayerFleets shape).
//
// This matters far beyond the career UI. Both parsers set a flag — static
// +0x4020, dynamic +0x4078 — and the dispatcher only fires the
// career-data-ready delegate when BOTH are set, which is what makes
// UYGoalManager::Initialized() return true. The hangar player controller's
// CheckWhetherToStartTutorial polls until the onboarding manager AND the goal
// manager are both initialized before it will start a new player's tutorial, so
// getting this shape wrong silently blocks onboarding entirely.
//
// An earlier revision put both arrays at the payload root on the theory that
// Load read them off the response directly; the disassembly above shows it does
// not, and the client logged "Career progression Data empty. Not initialized."
//
// Numbers go out as strings, not int32: these parsers use the client's
// restrictive tagged union (bool/double/int64/string only), where an int32 wire
// field reads back as 0.
//
// UEnum::GetValueByName matches the FULLY-QUALIFIED entry name that UE4 stores
// for a UENUM'd `enum class`, so these must be sent as
// "EYGoalCategory::YGC_RECRUIT", not the bare "YGC_RECRUIT". Confirmed live:
// bare names produced "FYCareerProgressionConfig::Load | Error parsing
// EYGoalCategory" (and the same for the other two enums), and confirmed in the
// exe string table, which contains only the qualified forms.
//
//	EYGoalCategory:           YGC_RECRUIT, YGC_CAPTAIN, YGC_ACHIEVEMENT, YGC_NONE
//	EYGoalRewardType:         YGR_GP, YGR_CREDITS, YGR_FREEXP, YGR_ID, YGR_ACHIEVEMENT, YGR_MEMBERSHIP, YGR_NONE
//	EYGoalPlatformVisibility: YGPV_PC, YGPV_PS4, YGPV_BOTH, YGPV_NONE
type careerGoalStage struct {
	amountToComplete int32
	reward           int32
	rewardType       string
}

type careerGoal struct {
	id          string
	title       string
	description string
	// titleKey / descriptionKey are the texts' keys in the client's
	// DreadGame_MmogData.locres (namespace ""), so the client shows them in
	// its own language. Empty = our own namespace (nsLocText fallback).
	titleKey           string
	descriptionKey     string
	uiGuideAvailable   bool
	counterID          string
	counterSubID       string
	category           string
	platformVisibility string
	stages             []careerGoalStage
}

// careerGoalsConfig is the static goal catalogue.
//
// NOTE on m_counterID: counters are defined client-side in a FYGoalCounters
// DataTable that is not present in our extracted assets, so these counter ids
// are a best guess. An unrecognised counter only means the goal never
// progresses — it does not stop the config from parsing, which is what clears
// the "Career progression Data empty" state. Correct them if the client logs
// unknown-counter warnings.
func careerGoalsConfig() []careerGoal {
	return []careerGoal{
		{
			// The client hardcodes this goal id. UYGoalManager::UpdateData
			// compares the goal's FINAL stage m_amountToComplete against the
			// player's current amount for it and stores the result as
			// IsGameModesUnlocked(); with the goal absent it logs
			// "###### GoalID NOT Found! ###, UnlockAllModes" on every refresh.
			//
			// We report it as already satisfied. The comparison is driven by a
			// per-player counter that this server does not track, so gating on
			// it would leave the game modes locked permanently rather than
			// unlocking them through play.
			id:               "UnlockAllModes",
			title:            "Full Deployment",
			description:      "All game modes are available.",
			uiGuideAvailable: false,
			counterID:        "MatchesPlayed",
			// Not a career milestone: the game's own text calls it a "Special
			// helper goal to configure the amount of matches to be played to
			// unlock all modes" (DreadGame_MmogData.locres). In the Recruit
			// career it blocked the rank-up to Captain -- the client unlocks a
			// career only when every goal of the previous one has every stage
			// claimed (0x535300) -- and nobody claims a helper (operator
			// 2026-10-09: "i claimed and got 2/2 i should rank up but didnt").
			// UYGoalManager still finds it by id for IsGameModesUnlocked.
			category:           "EYGoalCategory::YGC_NONE",
			platformVisibility: "EYGoalPlatformVisibility::YGPV_PC",
			stages: []careerGoalStage{
				{amountToComplete: 1, reward: 0, rewardType: "EYGoalRewardType::YGR_NONE"},
			},
		},
		// The careers below, rebuilt 2026-10-09 (operator: "please rebuild
		// it"). RECOVERED: every title and description is an original text
		// of the game, with its locres key -- titles like "Know the Ropes",
		// "Veteran Captain", "Fame and Fortune", and the counter texts
		// ("Matches played", "Daily Contracts completed", "T3 ships owned").
		// LOST (issue #68; no published list): which title went with which
		// counter, the stage targets, the rewards and the career of each.
		// GUESS: the pairing, targets, rewards and careers below -- an
		// onboarding Recruit career, then the Captain career it unlocks (the
		// help text: "Initially you will see only the Recruit-ranked
		// milestones ... once you have completed them, a new group of
		// Captain-ranked milestones will become available"). Every counter
		// is one this server can count (careerCounterValue).
		careerGoalDef("GOAL_TUTORIAL", "New Commission", "2E1734904B0297C5C209D28917B27944",
			"Tutorial finished", "B0AE20804D0AAA595D92EBA85529C886", counterTutorialFinished, "YGC_RECRUIT",
			cs(1, 1000, rewardCredits)),
		careerGoalDef("GOAL_PROVING_GROUNDS", "Action Stations", "05A0D0B046A8CFD0C6883C98EF431694",
			"Proving Grounds played", "3F93DE194F7B404F31B6EA86905B87DF", counterProvingGrounds, "YGC_RECRUIT",
			cs(1, 1000, rewardCredits), cs(3, 2000, rewardCredits)),
		careerGoalDef("GOAL_MATCHES_PLAYED", "Know the Ropes", "629D8DDB47B8372AC5DB5B97CF7A1F45",
			"Matches played", "29AFB2A34D03601987111BAAEFD10618", counterMatchesPlayed, "YGC_RECRUIT",
			cs(1, 1000, rewardCredits), cs(5, 2500, rewardCredits), cs(10, 1000, rewardFreeXP)),
		careerGoalDef("GOAL_SHIPS_DESTROYED", "Eliminate the Enemy", "D16D7C6A4465561ACC77BC8057436160",
			"Enemy ships destroyed", "A55B7144421596BA917808AB1F3FFACB", counterShipsDestroyed, "YGC_RECRUIT",
			cs(5, 1500, rewardCredits), cs(25, 3000, rewardCredits)),
		careerGoalDef("GOAL_MODULES_RESEARCHED", "Modulist", "7E4669DB4F6F0625D6D45F986B7299DD",
			"T1 modules researched", "9A09D2AD460E20FE480C71BF851DDEC0", counterModulesResearched, "YGC_RECRUIT",
			cs(1, 1000, rewardCredits), cs(5, 2000, rewardCredits)),
		careerGoalDef("GOAL_T2_OWNED", "On the Rise", "A9F162A4490610DD79009AA1244C7BBF",
			"T2 ships owned", "1BBFB69041F6B23267DF9F8F33235974", counterT2Owned, "YGC_RECRUIT",
			cs(1, 2500, rewardCredits)),

		careerGoalDef("GOAL_MATCHES_WON", "Conqueror", "0ABB5A5948FFD45E3619A198DEF31422",
			"Matches won", "314440E248D3F945B40BD8846B8AE2D6", counterMatchesWon, "YGC_CAPTAIN",
			cs(10, 5000, rewardCredits), cs(50, 10000, rewardCredits), cs(100, 100, rewardGP)),
		careerGoalDef("GOAL_DESTRUCTION", "Wreak Destruction", "8F9962DA4FFFCD519B9D168874836CD7",
			"Enemy ships destroyed", "A55B7144421596BA917808AB1F3FFACB", counterShipsDestroyed, "YGC_CAPTAIN",
			cs(100, 5000, rewardCredits), cs(500, 5000, rewardFreeXP), cs(1000, 150, rewardGP)),
		careerGoalDef("GOAL_T3_OWNED", "Veteran Captain", "8D02B34043877C80D75E9EAEDED9D956",
			"T3 ships owned", "0D2CEB514E6B7EC0F0A5F19FDDFFE18F", counterT3Owned, "YGC_CAPTAIN",
			cs(1, 5000, rewardCredits), cs(5, 10000, rewardCredits)),
		careerGoalDef("GOAL_T4_OWNED", "Legendary Captain", "35CD19F64BB7DC1963DA019EBB2CF5A0",
			"T4 ships or higher owned", "CA958C1B4A0CD7B9EB9A9E9FF3DBEB9C", counterT4Owned, "YGC_CAPTAIN",
			cs(1, 10000, rewardCredits), cs(5, 200, rewardGP)),
		careerGoalDef("GOAL_CONTRACTS", "Fame and Fortune", "23193AFC47D175DA23DD918965B75D0D",
			"Daily Contracts completed", "2F31B7184F3CEBD4C285CDA4D515FCE2", counterContracts, "YGC_CAPTAIN",
			cs(5, 5000, rewardCredits), cs(25, 10000, rewardCredits), cs(100, 200, rewardGP)),
		careerGoalDef("GOAL_TDM", "Spread the Dread", "15F1297A4B903CF28DEE15B9E777568C",
			"Team Deathmatches played", "F3BDBF534752E5EC068746B5283D8928", counterTDMPlayed, "YGC_CAPTAIN",
			cs(10, 3000, rewardCredits), cs(50, 3000, rewardFreeXP)),
		careerGoalDef("GOAL_TE", "Stand Your Ground", "B2E21F9A4A80B9D5052F2FB04C465486",
			"Team Eliminations played", "8EF5FC69425DDC8700B3EDA4C6FF6418", counterTEPlayed, "YGC_CAPTAIN",
			cs(5, 3000, rewardCredits), cs(25, 3000, rewardFreeXP)),
		careerGoalDef("GOAL_ONSLAUGHT", "Own the Skies", "9ECF198D446A083A08AC82BE48EBCF29",
			"Onslaught matches played", "0C7F56D6453053CC921991ABCDC68982", counterOnslaughtPlayed, "YGC_CAPTAIN",
			cs(5, 3000, rewardCredits), cs(25, 3000, rewardFreeXP)),
		careerGoalDef("GOAL_CONQUEST", "Colonial Scramble", "BDDA088D46445BE2546056BA044DB11F",
			"Conquest matches played", "9CD684BE42858900F5380B9D0E1B58EB", counterConquestPlayed, "YGC_CAPTAIN",
			cs(5, 3000, rewardCredits), cs(25, 3000, rewardFreeXP)),
	}
}

func appendCareerGoalsConfig(b []byte, stack []int) ([]byte, []int) {
	// The client hands FYCareerProgressionConfig::Load the "result" child of the
	// response (mmog_client.cpp: GetField(response, L"result") -> Load), and Load
	// then looks up "CareerGoalsConfig" by name on that node. So the array lives
	// one level down, inside result -- not at the payload root.
	b, stack = protocol.AppendObjectStart(b, stack, "result")
	b, stack = protocol.AppendArrayStart(b, stack, "CareerGoalsConfig")
	for _, goal := range careerGoalsConfig() {
		b, stack = protocol.AppendUnnamedObjectStart(b, stack)
		b = protocol.AppendStringField(b, "m_id", goal.id)
		b = protocol.AppendStringField(b, "m_title", careerGoalText(goal.titleKey, goal.id+".Title", goal.title))
		b = protocol.AppendStringField(b, "m_description", careerGoalText(goal.descriptionKey, goal.id+".Description", goal.description))
		b = protocol.AppendBoolField(b, "m_uiGuideAvailable", goal.uiGuideAvailable)
		b = protocol.AppendStringField(b, "m_counterID", goal.counterID)
		b = protocol.AppendStringField(b, "m_counterSubId", goal.counterSubID)
		b = protocol.AppendStringField(b, "m_category", goal.category)
		b = protocol.AppendStringField(b, "m_platformVisibility", goal.platformVisibility)
		b, stack = protocol.AppendArrayStart(b, stack, "m_stageData")
		for _, stage := range goal.stages {
			b, stack = protocol.AppendUnnamedObjectStart(b, stack)
			// Numeric-looking fields go out as strings. Load reads them through
			// the client's restrictive tagged union, which only understands
			// bool/double/int64/string nodes -- an int32 wire field (tag 0x56)
			// is not one of them and silently reads back 0.
			b = protocol.AppendStringField(b, "m_amountToComplete", strconv.Itoa(int(stage.amountToComplete)))
			b = protocol.AppendStringField(b, "m_reward", strconv.Itoa(int(stage.reward)))
			b = protocol.AppendStringField(b, "m_rewardType", stage.rewardType)
			b, stack = protocol.AppendObjectEnd(b, stack)
		}
		b, stack = protocol.AppendObjectEnd(b, stack)
		b, stack = protocol.AppendObjectEnd(b, stack)
	}
	b, stack = protocol.AppendObjectEnd(b, stack) // CareerGoalsConfig
	return protocol.AppendObjectEnd(b, stack)     // result
}

// appendCareerGoalProgress writes the per-goal progress entries the dynamic
// response carries. FYCareerProgressionData::Update reads {goalId, progress}
// per entry; the name of the array that wraps them is chosen by the RT
// dispatcher, which is not statically traceable (no xrefs — it is invoked
// through the response dispatch table). "CareerProgression" and "goals" are
// both real wire-name candidates from the client's string table, so emit the
// same entries under both; the client reads whichever it looks for and ignores
// the other. Neither collides case-insensitively with anything else we send.
func appendCareerGoalProgress(b []byte, stack []int, playerPID string) ([]byte, []int) {
	// FYCareerProgressionData::Update is handed the response's "result" child
	// and immediately treats it as an array -- it reads the node's element
	// count and walks its entries directly, with no intermediate field lookup.
	// So "result" IS the array, the same shape YA_PlayerFleets uses. Per entry
	// it reads goalId, progress and claimed_stage; the two numbers go out as
	// strings for the tagged-union reason described above.
	b, stack = protocol.AppendArrayStart(b, stack, "result")
	for _, goal := range careerGoalsConfig() {
		b, stack = protocol.AppendUnnamedObjectStart(b, stack)
		b = protocol.AppendStringField(b, "goalId", goal.id)
		b = protocol.AppendStringField(b, "progress", strconv.Itoa(int(careerGoalProgressForPlayer(playerPID, goal.id))))
		b = protocol.AppendStringField(b, "claimed_stage", strconv.Itoa(int(careerGoalClaimedStages(playerPID, goal.id))))
		b, stack = protocol.AppendObjectEnd(b, stack)
	}
	return protocol.AppendObjectEnd(b, stack)
}

// careerGoalProgressForPlayer returns the player's current amount for a goal.
//
// This is the number the client actually uses. UYGoalManager::GetCurrentAmount
// (YGoalManager.cpp:0x193) looks the GOAL ID up in the map the dynamic career
// response fills and reads the amount straight out of it -- it does not resolve
// m_counterID itself. So whatever is reported here is the goal's progress, and
// while this returned a constant zero no goal could ever advance.
//
// Two sources feed it, both real:
//
//   - counters the CLIENT reports through YA_IncrementPlayerStatsCounter, which
//     carries counterId/counterSubId as strings ("Customize"/"Captain"). Those
//     names are the client's own, so a goal keyed on one progresses exactly when
//     the client says it should.
//   - match results this server recorded itself, for the goals the client has no
//     counter for. matchesPlayedByPlayer counts finished matches the player held
//     a slot in.
func careerGoalProgressForPlayer(playerPID string, goalID string) int32 {
	if goalID == "UnlockAllModes" {
		// Must meet the goal's final-stage amount, or UYGoalManager reports
		// game modes as locked. See the goal's definition above.
		return 1
	}

	for _, goal := range careerGoalsConfig() {
		if goal.id != goalID {
			continue
		}
		// A counter the client reports wins: it is the client's own count.
		if value := playerStatsCounterValue(playerPID, goal.counterID, goal.counterSubID); value > 0 {
			return value
		}
		return careerCounterValue(playerPID, goal.counterID)
	}
	return 0
}

// counterMatchesPlayed is the counter this server could satisfy from its own
// records before results existed. CORRECTED 2026-09-26: this said there was
// deliberately no counterMatchesWon because nothing wrote a match result; since
// battle-server-mod reports results (battle_result.go), MatchesWon and
// ShipsDestroyed come from battle_results via battleResultCounter.
const counterMatchesPlayed = "MatchesPlayed"

// matchesPlayedByPlayer counts finished matches the player held a slot in. A
// match counts once it has an ended_at; the matchmaker writes rows as 'active'
// and nothing has ended one yet, so today this is zero for everyone -- it will
// start moving as soon as match completion is recorded.
func matchesPlayedByPlayer(playerPID string) int32 {
	database := currentMmogPlayerStateDB()
	if database == nil {
		return 0
	}
	var count int32
	if err := database.QueryRow(`
		SELECT COUNT(*) FROM match_slots ms
		JOIN matches m ON ms.match_id = m.id
		WHERE ms.user_id = ? AND m.ended_at IS NOT NULL
	`, normalizedPlayerStatePID(playerPID)).Scan(&count); err != nil {
		return 0
	}
	return count
}

// m_title and m_description are FText properties, and the client showed them
// BLANK when sent as plain strings (live, 2026-09-25: both career slots had no
// name). The original mmog data carried every player-facing text as an
// NSLOCTEXT("namespace","key","source") macro -- the developers' own
// Config/Localization/ExtractNsLocTextDataFromMmogData.py exists to pull those
// macros out of the mmogbrain configs into DreadGame_MmogData.locres. UE4's
// FText import reads that macro; an unquoted bare string it does not.
//
// Our goal texts are not in that locres (the real goal catalogue is lost,
// see the GitHub issue on career goals), so they use their own namespace. A
// key the client's locres does not contain displays its source text.
const careerGoalTextNamespace = "DNPrivateServer.CareerGoals"

// nsLocText formats an NSLOCTEXT macro, escaping what would end the quoted
// arguments early.
func nsLocText(namespace, key, source string) string {
	esc := strings.NewReplacer(`\`, `\\`, `"`, `\"`)
	return `NSLOCTEXT("` + esc.Replace(namespace) + `", "` + esc.Replace(key) + `", "` + esc.Replace(source) + `")`
}

// careerGoalText is a goal text as an NSLOCTEXT macro: with its key in the
// client's DreadGame_MmogData.locres (namespace "") when it is an original
// text, so the client shows its own translation; otherwise our namespace.
func careerGoalText(locresKey, ownKey, source string) string {
	if locresKey != "" {
		return nsLocText("", locresKey, source)
	}
	return nsLocText(careerGoalTextNamespace, ownKey, source)
}

const (
	rewardCredits = "EYGoalRewardType::YGR_CREDITS"
	rewardFreeXP  = "EYGoalRewardType::YGR_FREEXP"
	rewardGP      = "EYGoalRewardType::YGR_GP"
)

func cs(amount, reward int32, rewardType string) careerGoalStage {
	return careerGoalStage{amountToComplete: amount, reward: reward, rewardType: rewardType}
}

func careerGoalDef(id, title, titleKey, description, descriptionKey, counter, category string, stages ...careerGoalStage) careerGoal {
	return careerGoal{id: id, title: title, titleKey: titleKey, description: description, descriptionKey: descriptionKey,
		counterID: counter, category: "EYGoalCategory::" + category,
		platformVisibility: "EYGoalPlatformVisibility::YGPV_PC", stages: stages}
}
