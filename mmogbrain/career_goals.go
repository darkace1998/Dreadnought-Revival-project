package main

import (
	"strconv"

	"github.com/dreadnought-ps/mmogbrain/protocol"
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
	id                 string
	title              string
	description        string
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
			id:                 "UnlockAllModes",
			title:              "Full Deployment",
			description:        "All game modes are available.",
			uiGuideAvailable:   false,
			counterID:          "MatchesPlayed",
			category:           "EYGoalCategory::YGC_RECRUIT",
			platformVisibility: "EYGoalPlatformVisibility::YGPV_PC",
			stages: []careerGoalStage{
				{amountToComplete: 1, reward: 0, rewardType: "EYGoalRewardType::YGR_NONE"},
			},
		},
		{
			id:                 "GOAL_MATCHES_PLAYED",
			title:              "Shakedown Cruise",
			description:        "Complete multiplayer matches.",
			uiGuideAvailable:   false,
			counterID:          "MatchesPlayed",
			category:           "EYGoalCategory::YGC_RECRUIT",
			platformVisibility: "EYGoalPlatformVisibility::YGPV_PC",
			stages: []careerGoalStage{
				{amountToComplete: 1, reward: 1000, rewardType: "EYGoalRewardType::YGR_CREDITS"},
				{amountToComplete: 5, reward: 2500, rewardType: "EYGoalRewardType::YGR_CREDITS"},
				{amountToComplete: 25, reward: 5000, rewardType: "EYGoalRewardType::YGR_CREDITS"},
			},
		},
		{
			id:                 "GOAL_MATCHES_WON",
			title:              "Victory Conditions",
			description:        "Win multiplayer matches.",
			uiGuideAvailable:   false,
			counterID:          "MatchesWon",
			category:           "EYGoalCategory::YGC_CAPTAIN",
			platformVisibility: "EYGoalPlatformVisibility::YGPV_PC",
			stages: []careerGoalStage{
				{amountToComplete: 1, reward: 1500, rewardType: "EYGoalRewardType::YGR_CREDITS"},
				{amountToComplete: 10, reward: 4000, rewardType: "EYGoalRewardType::YGR_CREDITS"},
			},
		},
		{
			id:                 "GOAL_SHIPS_DESTROYED",
			title:              "Hull Breaker",
			description:        "Destroy enemy ships.",
			uiGuideAvailable:   false,
			counterID:          "ShipsDestroyed",
			category:           "EYGoalCategory::YGC_ACHIEVEMENT",
			platformVisibility: "EYGoalPlatformVisibility::YGPV_PC",
			stages: []careerGoalStage{
				{amountToComplete: 10, reward: 1000, rewardType: "EYGoalRewardType::YGR_CREDITS"},
				{amountToComplete: 100, reward: 5000, rewardType: "EYGoalRewardType::YGR_FREEXP"},
			},
		},
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
		b = protocol.AppendStringField(b, "m_title", goal.title)
		b = protocol.AppendStringField(b, "m_description", goal.description)
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
		b = protocol.AppendStringField(b, "claimed_stage", "0")
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
		// Otherwise fall back to what this server observed. Only matches
		// PLAYED is derivable: nothing records who won a match yet -- the
		// matches table has no result column at all -- so a wins goal stays at
		// zero rather than being fabricated from something else.
		if goal.counterID == counterMatchesPlayed {
			return matchesPlayedByPlayer(playerPID)
		}
		return 0
	}
	return 0
}

// counterMatchesPlayed is the one counter this server can satisfy from its own
// records. There is deliberately no counterMatchesWon: nothing writes a match
// result anywhere, so a wins goal has no honest source and stays at zero until
// one exists.
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
