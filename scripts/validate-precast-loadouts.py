#!/usr/bin/env python3
"""Validate the server's ship roster against the client's COOKED precast loadouts.

Ground truth: data/loadouts/PrecastLoadouts_cooked.jsonl, produced from the
client's own .uasset files by:

    dotnet bpdump/bin/Debug/net8.0/bpdump.dll --loadouts DreadGame/Content \
        > data/loadouts/PrecastLoadouts_cooked.jsonl

Each blueprint names its hull, weapons, abilities, officer perks and the
previous-tier loadout by ASSET PATH; paths are resolved to ids through the
client's ItemIDRegister. The server side is mmogbrain/base_ship_loadouts_gen.go.

Checks, per player-facing tiered loadout (Precast/T1..T5):
  roster   every cooked loadout is in the server, and nothing extra is
  identity id, tier, name
  slots    primary, secondary, abilities[0..3] in order, officer perks[0..3]
  prereq   m_previousTierLoadout == the server's tier-1 hull in the same line

Exit status is non-zero on any mismatch, so it can run in CI.
"""
import json, os, re, sys, collections

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
COOKED = os.path.join(ROOT, "data/loadouts/PrecastLoadouts_cooked.jsonl")
REGISTER = os.path.join(ROOT, "data/assets/ItemIDRegister.json")
GEN = os.path.join(ROOT, "mmogbrain/base_ship_loadouts_gen.go")
TIERED = re.compile(r"/Loadouts/Precast/T([1-5])/")
LINE = re.compile(r"VH_([A-Za-z]+?(?:Light|Medium|Heavy))_")


def walk(n):
    if isinstance(n, list):
        for v in n:
            yield from walk(v)
    elif isinstance(n, dict):
        yield n
        for v in n.values():
            if isinstance(v, (list, dict)):
                yield from walk(v)


REG = {n["Path"]: n["ItemID"] for n in walk(json.load(open(REGISTER)))
       if "ItemID" in n and "Path" in n}


def rid(path):
    """Resolve '/Game/.../X_BP.X_BP_C' to its register id (0 if unset)."""
    if not path:
        return 0
    pkg = path.split(".")[0]
    return REG.get(pkg, None)


def cooked():
    out = {}
    for line in open(COOKED):
        r = json.loads(line)
        m = TIERED.search(r["file"])
        if not m:
            continue
        perk_paths = [r.get(k) for k in ("m_officerFirstPerk", "m_officerWeaponPerk",
                                        "m_officerNavigationPerk", "m_officerEngineerPerk")]
        prev = r.get("m_previousTierLoadout")
        out[r["m_itemSystemData"]["m_itemID"]] = {
            "file": r["file"].split("/Content/")[1],
            "tier": r["m_itemSystemData"]["m_itemTier"],
            "name": r.get("m_name"),
            "line": LINE.search(r["file"]).group(1),
            "primary": rid(r.get("m_primaryWeaponClass")),
            "secondary": rid(r.get("m_secondaryWeaponClass")),
            "abilities": [rid(p) for p in r.get("m_abilities", [])],
            "perks": [rid(p) for p in perk_paths],
            "perkIds": r.get("m_perkIds"),
            "prev_path": prev,
        }
    # previous-tier loadout: resolve through the cooked set by export path
    by_pkg = {}
    for line in open(COOKED):
        r = json.loads(line)
        by_pkg[r["file"].split("/Content/")[1].replace(".uasset", "")] = r["m_itemSystemData"]["m_itemID"]
    for c in out.values():
        p = c.pop("prev_path")
        c["prev"] = by_pkg.get(p.split(".")[0].replace("/Game/", "")) if p else None
    return out


def server():
    rx = re.compile(r"\{loadoutID: (\d+), hullLine: \"(\w+)\", tier: (\d+), name: \"([^\"]*)\".*?"
                    r"primary: (-?\d+), secondary: (-?\d+), abilities: \[4\]int32\{([^}]*)\}, perks: \[4\]int32\{([^}]*)\}")
    src = open(GEN).read()
    # Base hulls only. The same file holds heroShipLoadouts, which share hull
    # lines and tiers with the base roster; letting them into by_line below
    # made a hero look like every base hull's prerequisite.
    start = src.index("var baseShipLoadouts")
    src = src[start:src.index("\n}\n", start)]
    out = {}
    for m in rx.finditer(src):
        ints = lambda s: [int(x) for x in s.split(",")]
        out[int(m.group(1))] = {"line": m.group(2), "tier": int(m.group(3)), "name": m.group(4),
                                "primary": int(m.group(5)), "secondary": int(m.group(6)),
                                "abilities": ints(m.group(7)), "perks": ints(m.group(8))}
    by_line = collections.defaultdict(dict)
    for i, s in out.items():
        by_line[s["line"]][s["tier"]] = i
    for i, s in out.items():
        s["prev"] = by_line[s["line"]].get(s["tier"] - 1)
    return out


# Server hulls with no cooked blueprint in our extraction, and why each is kept.
# An entry here must be explained; it is not a way to silence a mismatch.
KNOWN_UNCOOKED = {
    # Brutus, AssaultLight T5. The client's own ItemIDRegister lists it at
    # /Game/Generic/Loadouts/Precast/T5/VH_AssaultLight_PrecastLoadout_T5_BP, so
    # the client knows the id, but that .uasset is absent from DreadGame/Content.
    # Its slots come from the community reference and every one resolves through
    # the register into the right category -- unverifiable against cooked data,
    # but no evidence against it either. Re-check if the extraction is redone.
    33489299,
}


def main():
    c, s = cooked(), server()
    problems = []
    for i in sorted(set(c) - set(s)):
        problems.append(f"MISSING  {i} {c[i]['name']} T{c[i]['tier']} {c[i]['line']}  ({c[i]['file']})")
    for i in sorted(KNOWN_UNCOOKED & set(s)):
        print(f"KNOWN    {i} {s[i]['name']} T{s[i]['tier']} {s[i]['line']}  (register-confirmed, no cooked .uasset)")
    for i in sorted(set(s) - set(c) - KNOWN_UNCOOKED):
        problems.append(f"EXTRA    {i} {s[i]['name']} T{s[i]['tier']} {s[i]['line']}  (no cooked tiered loadout)")
    unresolved = []
    for i in sorted(set(c) & set(s)):
        a, b = c[i], s[i]
        tag = f"{i} {a['name']} T{a['tier']}"
        for k in ("tier", "line", "name", "primary", "secondary", "abilities", "prev"):
            if a[k] != b[k]:
                problems.append(f"DIFF     {tag:28} {k:10} cooked={a[k]} server={b[k]}")
        cp = [p or 0 for p in a["perks"]]
        if cp != b["perks"]:
            problems.append(f"DIFF     {tag:28} perks      cooked={cp} server={b['perks']}")
        for k in ("primary", "secondary"):
            if a[k] is None:
                unresolved.append(f"{tag} {k}")
        if None in a["abilities"] or None in a["perks"]:
            unresolved.append(f"{tag} abilities/perks")
    print(f"cooked tiered loadouts: {len(c)}   server hulls: {len(s)}   compared: {len(set(c)&set(s))}")
    for u in unresolved:
        print("UNRESOLVED", u)
    for p in problems:
        print(p)
    print(f"\n{len(problems)} mismatches, {len(unresolved)} unresolved paths")
    return 1 if problems or unresolved else 0


HERO_COOKED = os.path.join(ROOT, "data/loadouts/HeroLoadouts_cooked.jsonl")
HERO_RX = re.compile(r"\{loadoutID: (\d+), hullLine: \"(\w+)\", tier: (\d+), name: \"([^\"]*)\".*?"
                     r"primary: (-?\d+), secondary: (-?\d+), abilities: \[4\]int32\{([^}]*)\}, perks: \[4\]int32\{([^}]*)\}")


def heroes():
    """Hero ships: same slot checks as the base roster, against *_HeroLoadout_BP.

    Produced by: bpdump --loadouts DreadGame/Content/Generic/Loadouts/Hero
    "*.uasset" > data/loadouts/HeroLoadouts_cooked.jsonl
    (NOT *_HeroLoadout_BP: Phoenix is spelled _Heroloadout_BP and Linux globs are
    case-sensitive, so that pattern silently drops it.)
    """
    if not os.path.exists(HERO_COOKED):
        print("hero check skipped: no", HERO_COOKED)
        return 0
    cooked = {}
    for line in open(HERO_COOKED):
        r = json.loads(line)
        perk_paths = [r.get(k) for k in ("m_officerFirstPerk", "m_officerWeaponPerk",
                                        "m_officerNavigationPerk", "m_officerEngineerPerk")]
        cooked[r["m_itemSystemData"]["m_itemID"]] = {
            "name": r.get("m_name"), "tier": r["m_itemSystemData"]["m_itemTier"],
            "primary": rid(r.get("m_primaryWeaponClass")),
            "secondary": rid(r.get("m_secondaryWeaponClass")),
            "abilities": [rid(p) for p in r.get("m_abilities", [])],
            "perks": [rid(p) or 0 for p in perk_paths],
            "file": r["file"].split("/Content/")[1],
        }
    src = open(GEN).read()
    src = src[src.index("var heroShipLoadouts"):]
    server = {}
    for m in HERO_RX.finditer(src):
        ints = lambda t: [int(x) for x in t.split(",")]
        server[int(m.group(1))] = {"name": m.group(4), "tier": int(m.group(3)),
                                   "primary": int(m.group(5)), "secondary": int(m.group(6)),
                                   "abilities": ints(m.group(7)), "perks": ints(m.group(8))}
    problems = []
    for i in sorted(set(cooked) - set(server)):
        problems.append(f"HERO MISSING {i} {cooked[i]['name']}  ({cooked[i]['file']})")
    for i in sorted(set(server) - set(cooked)):
        problems.append(f"HERO EXTRA   {i} {server[i]['name']}  (no cooked hero loadout)")
    for i in sorted(set(cooked) & set(server)):
        a, b = cooked[i], server[i]
        for k in ("tier", "primary", "secondary", "abilities", "perks"):
            if a[k] != b[k]:
                problems.append(f"HERO DIFF    {i} {a['name']:22} {k:10} cooked={a[k]} server={b[k]}")
        if a["name"] != b["name"]:
            problems.append(f"HERO NAME    {i} cooked={a['name']!r} server={b['name']!r}")
    print(f"\ncooked hero loadouts: {len(cooked)}   server heroes: {len(server)}   compared: {len(set(cooked)&set(server))}")
    for p in problems:
        print(p)
    print(f"{len(problems)} hero mismatches")
    return 1 if problems else 0


if __name__ == "__main__":
    base = main()
    hero = heroes()
    sys.exit(base or hero)
