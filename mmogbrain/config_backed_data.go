package main

import (
	"strings"

	dreadconfig "github.com/dreadnought-ps/shared/dreadgameconfig"
	"github.com/dreadnought-ps/mmogbrain/protocol"
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

// essentialProgressionCategoryIDs are the only progression categories a new
// player's hangar actually needs: ship loadouts (1), weapons (2), officer perks
// (3), abilities (4), ships (5), and trader/hero loadouts (6/7). For EACH
// category we send in YA_GetStaticCareerData/YA_GetCareerProgression, the client
// synchronously scans that category's configured asset paths
// (DefaultProgression.ini [ProgressionItemListAssetPaths], via
// ScanPathsSynchronous/GetAssetsByPath) to build the category. Sending all 24
// categories — including the huge vanity (20-24), booster/membership (30-31),
// unlock-container (32-34), character-customization (50-52) and menu (80-83)
// trees — made the client freeze the game thread for ~39s inside static-career
// processing (proven: frame counter advances by 1 across the 39s gap that ends
// exactly at "Career progression static Data empty"). None of those extra
// categories are needed to enter the hangar with owned ships. Trim to the
// essentials so the synchronous scan stays small.
var essentialProgressionCategoryIDs = map[int32]bool{1: true, 2: true, 3: true, 4: true, 5: true, 6: true, 7: true}

func configBackedProgressionTaxonomies() []dreadconfig.ProgressionTaxonomy {
	all := dreadconfig.ProgressionTaxonomies()
	out := make([]dreadconfig.ProgressionTaxonomy, 0, len(essentialProgressionCategoryIDs))
	for _, taxonomy := range all {
		if essentialProgressionCategoryIDs[taxonomy.CategoryID] {
			out = append(out, taxonomy)
		}
	}
	return out
}

func configBackedProgressionCategoryDataTablePath() string {
	// m_categoryDTPath must be a client-loadable /Game/ asset path pointing at a
	// real UDataTable in the cooked content. The client's career/progression
	// handler LOADS this DataTable to populate the category rows; if the path is
	// unloadable (a server .cfg path) OR empty, the load fails and the client
	// treats the whole progression category set as empty and never initializes
	// ("Career progression static Data empty. Not initialized", then
	// "...must receive Static data out of order" for the dynamic response). Both
	// the old server-.cfg path AND a later empty-string attempt reproduced that.
	// The correct value is the client's own configured DataTable
	// (DefaultProgression.ini [/Script/DreadGame.YPlayerMatchStatisticsManager]
	// m_categoryDTPath), which exists in the cooked content at
	// Content/Generic/DataTables/Progression/DN_PlayerMatchStatistics_DT.uasset.
	return "/Game/Generic/DataTables/Progression/DN_PlayerMatchStatistics_DT"
}

func appendMmogProgressionCategoryEntry(b []byte, stack []int, taxonomy dreadconfig.ProgressionTaxonomy) ([]byte, []int) {
	b, stack = protocol.AppendUnnamedObjectStart(b, stack)
	b = protocol.AppendStringField(b, "TableCategory", taxonomy.TableCategory)
	b = protocol.AppendInt32Field(b, "CategoryID", taxonomy.CategoryID)
	b, stack = protocol.AppendArrayStart(b, stack, "AssetRoots")
	for _, assetRoot := range taxonomy.AssetRoots {
		b = protocol.AppendUnnamedStringField(b, assetRoot)
	}
	b, stack = protocol.AppendObjectEnd(b, stack)
	b, stack = protocol.AppendObjectEnd(b, stack)
	return b, stack
}

func appendMmogProgressionCategories(b []byte, stack []int) ([]byte, []int) {
	for _, taxonomy := range configBackedProgressionTaxonomies() {
		b, stack = appendMmogProgressionCategoryEntry(b, stack, taxonomy)
	}
	return b, stack
}
