package main

// marketItemLocalizationKeys maps an item id to the localization KEY the client
// needs in a catalog entry's lowercase "name" field.
//
// The client does not display that string directly: FUN_142a80350 reads "name"
// and resolves it against the client's own string tables, writing the literal
// "<DNT>[[NotFound]]" when the lookup fails. That is why the catalog was
// previously served empty -- we had display names, not keys.
//
// The keys below were recovered from the game's own shipped localization data
// (Content/Localization/DreadGame/en/*.locres, 23566 entries), by matching each
// item's display name to the string it localizes to. Weapons and abilities are
// named per tier in those tables ("Repeater Turrets I".."V"), and every item we
// serve is the lowest tier of its family -- confirmed against ItemIDRegister
// asset paths, where 24 of our 30 weapon/ability items are literally the lowest
// /T<n>/ variant and the rest are the untiered base asset -- so the lowest
// numeral present is the correct one.
//
// The four starter hulls carry internal names ("Assault Medium T1") that do not
// appear in the tables; they resolve through the in-fiction name their loadout
// uses (Agosta, Simargl, Rurik, Cerberus).
//
// Generated from the extracted client data; regenerate if the item set changes.
var marketItemLocalizationKeys = map[int32]string{
	// The fleet identifies its ships by their development precast-loadout id
	// (VH_<Class>_Loadout_BP), not by the ship pawn id, and the client then
	// looks up an item under that id -- "ComposeShipManufacturerDataForLoadout
	// Could not find item for ship id 33489198". They name the same ships as
	// the precast loadouts above, so they share their keys.
	33489198:  "C0F4B2094FC4E520FC5EC4A6C8D794A3", // Agosta (fleet ship entry)
	33489199:  "782800DC42783501C6F177850AC2300B", // Rurik (fleet ship entry)
	33489200:  "60536F1F437BC4B3F0A650AAC0F62BCF", // Cerberus (fleet ship entry)
	33489239:  "BBD288674F213AD81CBC0798A521E6D1", // Simargl (fleet ship entry)
	33489262:  "C0F4B2094FC4E520FC5EC4A6C8D794A3", // Agosta (loadout)
	33489263:  "782800DC42783501C6F177850AC2300B", // Rurik (loadout)
	33489264:  "60536F1F437BC4B3F0A650AAC0F62BCF", // Cerberus (loadout)
	33489315:  "152FEF2542E405CA058F1E94CAE00484", // Athos (loadout)
	33489318:  "2ADB13724D563163284E29954CA7B155", // Zmey (loadout)
	33489331:  "031D39F1476B9E252F43D4809B11448B", // Aion (loadout)
	33489423:  "BBD288674F213AD81CBC0798A521E6D1", // Simargl (loadout)
	83820550:  "7F2D8A9C4DEC4F0FFDAB9CAE87F36C82", // Module Reboot I (ability)
	83820556:  "85D53ADD4011B99EBF9ED59B7A327F13", // Jump Drive II (ability)
	83820560:  "B62D0FA843087ED0A71A9A87B6E8C3E2", // Hell Lasers III (ability)
	83820565:  "AB03E3CC4E6740AC3B836FAF4A8FF5B5", // Protean Autoguns I (ability)
	83820574:  "4A1C325C46BBE689FF848080245B2EB5", // Tempest Missiles I (ability)
	83820594:  "75EBA7EF4801E28D6B302C9151D3B8A1", // Weaponbreaker Missile III (ability)
	83820606:  "9D2C8B4842DAD31A5DC29A8A28C75A19", // Torpedo Salvo I (ability)
	83820764:  "EEA47181406B8A3EC54DE6935B07686B", // Stationary Cloak I (ability)
	83820781:  "685820B34D9A93B13D40F4AE2A2F7ECB", // Anti-Missile Lasers I (ability)
	83820799:  "12BB69A14CC74305E4A5FAB99109FAB6", // Siege Mode I (ability)
	83820830:  "93CB8A41491C71748CDB2DBC10375515", // Flechette Missiles (ability)
	83820839:  "4F3FECEE4CFD95446A0CF7A2ACBD8253", // Autorepair I (ability)
	83820851:  "6EADA83244F705104BC1979623181E80", // Repair Autobeams I (ability)
	83820857:  "F2A8749E437BEBA7E0A6F592CB38A902", // Beam Amplifier I (ability)
	83820879:  "10B61600412D6E43214FFD900115E4E6", // Repair Drones IV (ability)
	83820882:  "40D698BC4C743C2C0B850BBAC56CA8DC", // Repair Pod I (ability)
	83821076:  "3DC1C9014E2E64BE183220BADAE6489E", // Warp Jump I (ability)
	83821082:  "D3F23BE3423CA352D3EBFD84CF9E71B8", // Plasma Broadside I (ability)
	83825291:  "27AA31724CDB0936C688899A1A2B9EE6", // Vulture Missiles I (ability)
	100597772: "AA639ABD406194105A9F3B80D2B0789E", // Repeater Turrets I (weapon)
	100597862: "DD0EFB084B4BE8AFBF559D90C470411C", // Heavy Repair Beam IV (weapon)
	100597870: "AFA338A541BC72CD8E5B8F8633814E9A", // Medium Beam Turrets I (weapon)
	100597877: "E443529F4933AF334458BFB06E6D79AE", // Light Machine Guns III (weapon)
	100597987: "AF6B8476426D916B7D590BB49E672FCF", // Heavy Tesla Cannon I (weapon)
	100598563: "9948B4E94F958C2C91AEADA62C5C3713", // Flak Turrets I (weapon)
	100598570: "93C265D949BFBF995E8452996B50C087", // Light Flak Turrets I (weapon)
	100598573: "72544E34472DA2919C5B19B0BE4CD40C", // Tesla Turrets I (weapon)
	100598595: "32F13C6D427CFD5A9261FABAA7CD8E27", // Heavy Plasma Cannons I (weapon)
	100598596: "06D3ADFC4BFB38D9B89A468FBA534DF3", // Repeater Guns I (weapon)
	117374977: "19B9027A46BD53FF6F715EBBDB5236E6", // Module Recycler (perk)
	117374979: "3AE2B74D468BDF8820C6F8BA015493D8", // Communications 101 (perk)
	117374980: "2D55AA124F25A799C9561B8D83C94ACB", // Feedback Loop (perk)
	117374982: "DC6DB5D24885320931CFEBB3EC8E91A1", // Engineering 101 (perk)
	117374985: "5FDEDFD44EF785F39E955C8E029C7657", // Mr. Fixit (perk)
	117374986: "CB22C99D4CC199694DF266B5C6180826", // Reinforced (perk)
	117374988: "EFD39C414282568C8EA57AA7CA2E3BDE", // Slow and Steady (perk)
	117374989: "43511A6A4EEA70E3A584E8A5F65283CA", // Navigation Expert (perk)
	117374991: "A529D5004BB4C736DBF6E9B566AA257F", // Navigation 101 (perk)
	117374993: "1F4C92004F1EF34DC48E1CB568DA1A1E", // Module Amper (perk)
	117374994: "B7FA39CC4BAD8C9445081A8B18FC52D8", // Glass Cannon (perk)
	117374997: "5F32F06A4A2006CA2637059A422323AD", // Survival Instinct (perk)
	184483950: "782800DC42783501C6F177850AC2300B", // Rurik (ship)
	184483982: "C0F4B2094FC4E520FC5EC4A6C8D794A3", // Agosta (ship)
	184484148: "0C4FFF514D18108B8273379AE8C8BD62", // Ceres (ship)
	184484170: "BBD288674F213AD81CBC0798A521E6D1", // Simargl (ship)
	184484171: "031D39F1476B9E252F43D4809B11448B", // Aion (ship)
	184484173: "2ADB13724D563163284E29954CA7B155", // Zmey (ship)
	184484177: "152FEF2542E405CA058F1E94CAE00484", // Athos (ship)
	184484180: "15B8E0194C583B63A097DFACD8090F7F", // Valcour (ship)
	184484184: "0D2C1994407382C96A66B6B8BFD056DB", // Svarog (ship)
	184484202: "60536F1F437BC4B3F0A650AAC0F62BCF", // Cerberus (ship)
}
