package main

// techTreeHullParents is the game's own ship unlock tree: the hull each ship
// is researched from. Transcribed from the operator's screenshots of the
// original tech tree (Jupiter Arms, Akula Vektor, Oberon), 2026-09-28; the
// five parents that sat on shared lines in the screenshots (Koschei, Blud,
// Fulgora, Gravis, Vindicta) were confirmed by the operator.
//
// The tree branches ACROSS classes within a manufacturer (Agosta -> Dover is
// Assault -> Scout, Otranto -> Jutland is Assault -> Dreadnought), which is why
// the old rule -- T(n) requires T(n-1) of its own line -- left 11 lines with no
// prerequisite at all, and why a class-based guess was wrong (deployed and
// reverted the same day).
//
// The roots have no entry: Agosta (Jupiter), Rurik and Simargl (Akula),
// Cerberus (Oberon). Every parent is one tier below its child, so the ClassId
// walk (techTreeHullClassID) can never loop.
var techTreeHullParents = map[int32]int32{
	// Jupiter Arms: Agosta 33489262.
	33489267: 33489262, // Dover <- Agosta
	33489265: 33489262, // Trafalgar <- Agosta
	33489276: 33489267, // Machias <- Dover
	33489281: 33489267, // Palos <- Dover
	33489278: 33489265, // Ballista <- Trafalgar
	33489272: 33489265, // Otranto <- Trafalgar
	33489290: 33489276, // Valcour <- Machias
	33489296: 33489281, // Harwich <- Palos
	33489292: 33489278, // Onager <- Ballista
	33489285: 33489272, // Vigo <- Otranto
	33489286: 33489272, // Jutland <- Otranto
	33489305: 33489290, // Nevis <- Valcour
	33489311: 33489296, // Cattaro <- Harwich
	33489307: 33489292, // Grenada <- Onager
	33489300: 33489285, // Athos <- Vigo
	33489301: 33489286, // Monarch <- Jutland

	// Akula Vektor: Rurik 33489263, Simargl 33489423.
	33489269: 33489263, // Tugarin <- Rurik
	33489266: 33489423, // Nav <- Simargl
	33489275: 33489269, // Kreshnik <- Tugarin
	33489280: 33489269, // Vucari <- Tugarin
	33489271: 33489266, // Dola <- Nav
	33489274: 33489266, // Chernobog <- Nav
	33489289: 33489275, // Stribog <- Kreshnik
	33489295: 33489271, // Koschei <- Dola (confirmed)
	33489294: 33489280, // Murometz <- Vucari
	33489283: 33489271, // Blud <- Dola (confirmed)
	33489288: 33489274, // Voronezh <- Chernobog
	33489304: 33489289, // Netron <- Stribog
	33489310: 33489295, // Ohkta <- Koschei
	33489309: 33489294, // Svarog <- Murometz
	33489298: 33489283, // Gora <- Blud
	33489303: 33489288, // Zmey <- Voronezh

	// Oberon: Cerberus 33489264.
	33489270: 33489264, // Orcus <- Cerberus
	33489268: 33489264, // Furia <- Cerberus
	33489277: 33489268, // Fulgora <- Furia (confirmed)
	33489282: 33489270, // Ceres <- Orcus
	33489279: 33489268, // Virtus <- Furia
	33489273: 33489270, // Gravis <- Orcus (confirmed)
	33489291: 33489277, // Medusa <- Fulgora
	33489297: 33489282, // Aion <- Ceres
	33489293: 33489279, // Nox <- Virtus
	33489284: 33489277, // Vindicta <- Fulgora (confirmed)
	33489287: 33489273, // Lorica <- Gravis
	33489306: 33489291, // Mithras <- Medusa
	33489312: 33489297, // Feronia <- Aion
	33489308: 33489293, // Stabia <- Nox
	33489299: 33489284, // Brutus <- Vindicta
	33489302: 33489287, // Invictus <- Lorica
}

// techTreeRootHulls are the ships each manufacturer's tree starts from.
var techTreeRootHulls = map[int32]bool{
	33489262: true, // Agosta
	33489263: true, // Rurik
	33489423: true, // Simargl
	33489264: true, // Cerberus
}
