package main

import (
	"strings"

	dreadconfig "github.com/dreadnought-ps/shared/dreadgameconfig"
)

func configBackedFleetEligibilities() []dreadconfig.FleetEligibility {
	return dreadconfig.FleetEligibilityValues()
}

func mustConfigBackedFleetEligibility(token string) dreadconfig.FleetEligibility {
	eligibility, ok := dreadconfig.FleetEligibilityByToken(token)
	if !ok {
		panic("missing config-backed fleet eligibility for " + token)
	}
	return eligibility
}

func configBackedFleetToken(eligibility dreadconfig.FleetEligibility) string {
	token := strings.TrimSpace(eligibility.Token)
	if token == "" {
		return ""
	}
	if strings.HasSuffix(strings.ToLower(token), "fleet") {
		return token
	}
	return token + "Fleet"
}

func buildConfigBackedStarterFleet(loadouts []mmogShipLoadoutSeed) mmogFleetSeed {
	eligibility := mustConfigBackedFleetEligibility("Recruit")
	fleet := mmogFleetSeed{
		fleetID:              eligibility.FleetType,
		token:                configBackedFleetToken(eligibility),
		displayName:          eligibility.DisplayName,
		fleetType:            eligibility.FleetType,
		tiers:                append([]int32(nil), eligibility.AllowedTiers...),
		active:               true,
		shipLoadouts:         loadouts,
		flagshipLoadoutIndex: -1,
	}
	if len(loadouts) > 0 {
		fleet.flagshipShipID = loadouts[0].effectiveFleetShipID()
		fleet.flagshipLoadoutID = loadouts[0].loadoutID()
		fleet.flagshipLoadoutIndex = loadouts[0].loadoutIndex
	}
	return fleet
}

func buildConfigBackedFleetSeeds(starterLoadouts []mmogShipLoadoutSeed) []mmogFleetSeed {
	eligibilities := configBackedFleetEligibilities()
	fleets := make([]mmogFleetSeed, 0, len(eligibilities))
	for _, eligibility := range eligibilities {
		fleet := mmogFleetSeed{
			fleetID:     eligibility.FleetType,
			token:       configBackedFleetToken(eligibility),
			displayName: eligibility.DisplayName,
			fleetType:   eligibility.FleetType,
			tiers:       append([]int32(nil), eligibility.AllowedTiers...),
		}
		if strings.EqualFold(strings.TrimSpace(eligibility.Token), "Recruit") {
			fleet.active = true
			fleet.shipLoadouts = starterLoadouts
			if len(starterLoadouts) > 0 {
				fleet.flagshipShipID = starterLoadouts[0].effectiveFleetShipID()
				fleet.flagshipLoadoutID = starterLoadouts[0].loadoutID()
				fleet.flagshipLoadoutIndex = starterLoadouts[0].loadoutIndex
			}
		} else {
			fleet.flagshipLoadoutIndex = -1
		}
		fleets = append(fleets, fleet)
	}
	return fleets
}

// The progression-taxonomy helpers that used to live here (m_categories /
// m_categoryDTPath for YA_GetStaticCareerData and YA_GetCareerProgression) are
// gone: the client parses career progression as a GOALS system, so those
// payloads now come from career_goals.go. m_categories actually belongs to
// UYPlayerMatchStatisticsManager (end-of-match statistics), which is why the
// client always reported our career data as empty.
//
// Worth remembering from that era: the client SYNCHRONOUSLY scans each
// progression category's configured asset paths, so sending all 24 categories
// froze its game thread for ~39s (UE4 frame counter advanced by exactly 1
// across the gap). If anything ever reintroduces a category list, keep it
// small.
