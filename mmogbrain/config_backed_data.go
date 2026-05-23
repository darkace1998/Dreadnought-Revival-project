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

func configBackedProgressionTaxonomies() []dreadconfig.ProgressionTaxonomy {
	return dreadconfig.ProgressionTaxonomies()
}

func configBackedProgressionCategoryDataTablePath() string {
	for _, surface := range dreadconfig.UnlockContainerExportSurfaces() {
		if strings.TrimSpace(surface.ExportPath) != "" {
			return surface.ExportPath
		}
	}
	return ""
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
