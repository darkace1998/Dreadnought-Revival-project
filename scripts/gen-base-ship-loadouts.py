#!/usr/bin/env python3
"""Generate mmogbrain/base_ship_loadouts_gen.go from the community loadout reference.

The reference (docs/reference/community-loadout-reference.txt) lists every
working loadout with its tier, class, manufacturer, blueprint path and its
primary/secondary weapon, four modules and four briefings. It is community
material, so nothing from it is emitted unless the client's own ItemIDRegister
confirms the path: every one of the 914 slot paths resolves, and the resolved
ids land in exactly the category their slot implies (weapons 5, abilities 4,
perks 6).

Names are NOT taken from this document where the client names the item --
ItemIDConversionTable wins, because the reference names some items after their
blueprint filename (its "Skagerrak" is the game's Huscarl).

Usage: python3 scripts/gen-base-ship-loadouts.py
"""

import json
import os
import re
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
REFERENCE = os.path.join(ROOT, "docs/reference/community-loadout-reference.txt")
REGISTER = os.path.join(ROOT, "data/assets/ItemIDRegister.json")
CONVERSION = os.path.join(ROOT, "data/assets/ItemIDConversionTable.json")
OUTPUT = os.path.join(ROOT, "mmogbrain/base_ship_loadouts_gen.go")

HEADER = re.compile(r"^(T\d)\s+(.+?),\s*(.+?):\s*(/Game/\S+)\s*$")
SLOT = re.compile(r"^\s+(PW|SW|M[1-4]|B[1-4]):\s*(.*?)\s*$")
HULL = re.compile(r"VH_([A-Za-z]+?)(Light|Medium|Heavy)_")

# Slot kind -> the ItemIDTable category its id must belong to. A mismatch means
# the reference points at the wrong asset, so the entry is rejected.
SLOT_CATEGORY = {"P": 5, "S": 5, "M": 4, "B": 6}

MANUFACTURERS = {
    "Akula Vector": "AkulaVektor",
    "Jupiter Arms": "JupiterArms",
    "Oberon": "Oberon",
}


def walk(node):
    if isinstance(node, list):
        for value in node:
            yield from walk(value)
    elif isinstance(node, dict):
        yield node
        for value in node.values():
            if isinstance(value, (list, dict)):
                yield from walk(value)


def load_register():
    with open(REGISTER, encoding="utf-8") as handle:
        raw = json.load(handle)
    return {n["Path"]: n["ItemID"] for n in walk(raw) if "ItemID" in n and "Path" in n}


def load_names():
    with open(CONVERSION, encoding="utf-8") as handle:
        raw = json.load(handle)
    names = {}
    for node in walk(raw):
        if "NewItemID" not in node:
            continue
        name = node.get("Name", "").replace(" ", " ").strip()
        if name:
            names.setdefault(int(node["NewItemID"]), name)
    return names


def normalize(path):
    """Turn a reference slot value into a register path, or None if empty."""
    if not path:
        return None
    path = path.strip().strip('"')
    if path.lower() in ("n/a", "none", ""):
        return None
    path = re.sub(r"\.\d+$", "", path)  # trailing ".0", ".36", ... is not part of the path
    if not path.startswith("/Game/"):
        path = "/Game/Generic/" + path.lstrip("/")
    return path


def parse():
    entries, current = [], None
    with open(REFERENCE, encoding="utf-8", errors="replace") as handle:
        for line in handle:
            header = HEADER.match(line.rstrip("\n"))
            if header:
                tier, midclass, name, path = header.groups()
                current = {
                    "tier": int(tier[1:]),
                    "midclass": midclass.strip(),
                    "name": name.strip(),
                    "path": path.strip(),
                    "slots": {},
                }
                entries.append(current)
                continue
            slot = SLOT.match(line.rstrip("\n"))
            if slot and current is not None:
                current["slots"][slot.group(1)] = slot.group(2)
    return entries


def main():
    register = load_register()
    names = load_names()
    entries = parse()

    rows, rejected = [], []
    for entry in entries:
        if "/Precast/" not in entry["path"]:
            continue  # hero loadouts break the hull-line rules; they stay hand-kept
        hull = HULL.search(entry["path"].split("/")[-1])
        loadout_id = register.get(entry["path"])
        if hull is None or loadout_id is None:
            rejected.append((entry["name"], "loadout path not in ItemIDRegister"))
            continue

        resolved, bad = {}, None
        for slot, value in entry["slots"].items():
            path = normalize(value)
            if path is None:
                continue
            item_id = register.get(path)
            if item_id is None:
                bad = f"{slot} path {path} not in ItemIDRegister"
                break
            category = (item_id >> 24) & 0xFF
            if category != SLOT_CATEGORY[slot[0]]:
                bad = f"{slot} id {item_id} is category {category}, want {SLOT_CATEGORY[slot[0]]}"
                break
            resolved[slot] = item_id
        if bad:
            rejected.append((entry["name"], bad))
            continue

        rows.append(
            {
                "id": loadout_id,
                "line": hull.group(1) + hull.group(2),
                "tier": entry["tier"],
                "name": names.get(loadout_id, entry["name"]),
                "named_by_client": loadout_id in names,
                "manufacturer": next(
                    (v for k, v in MANUFACTURERS.items() if entry["midclass"].startswith(k)), ""
                ),
                "primary": resolved.get("PW", 0),
                "secondary": resolved.get("SW", 0),
                "abilities": [resolved.get(f"M{i}", 0) for i in range(1, 5)],
                "perks": [resolved.get(f"B{i}", 0) for i in range(1, 5)],
            }
        )

    if rejected:
        print(f"rejected {len(rejected)} entries:", file=sys.stderr)
        for name, why in rejected:
            print(f"  {name}: {why}", file=sys.stderr)

    rows.sort(key=lambda r: (r["line"], r["tier"]))
    from_client = sum(1 for r in rows if r["named_by_client"])

    out = []
    out.append("// Code generated by scripts/gen-base-ship-loadouts.py. DO NOT EDIT.")
    out.append("//")
    out.append("// The base (non-hero) ship roster: every tiered precast loadout the community")
    out.append("// reference in docs/reference/ covers, with the weapons, abilities and perks it")
    out.append("// equips. Nothing here is transcribed on trust -- the generator resolves every")
    out.append("// path through the client's own ItemIDRegister and rejects any slot whose id")
    out.append("// does not land in the category the slot implies (weapon 5, ability 4, perk 6).")
    out.append("//")
    out.append(f"// {len(rows)} hulls across {len({r['line'] for r in rows})} hull lines.")
    out.append(f"// Names: {from_client} come from ItemIDConversionTable, {len(rows) - from_client} from the reference")
    out.append("// (the client's table does not cover them). Where the two disagree the client")
    out.append("// wins, because the reference names some items after their blueprint filename.")
    out.append("")
    out.append("package main")
    out.append("")
    out.append("// baseShipLoadout is one hull in the tech tree and what it flies with.")
    out.append("type baseShipLoadout struct {")
    out.append("\tloadoutID   int32")
    out.append("\thullLine    string // <Class><Size>, e.g. AssaultMedium")
    out.append("\ttier        int32")
    out.append("\tname        string")
    out.append("\tclientNamed bool // false = name came from the community reference")
    out.append("\tprimary     int32")
    out.append("\tsecondary   int32")
    out.append("\tabilities   [4]int32")
    out.append("\tperks       [4]int32")
    out.append("}")
    out.append("")
    out.append("var baseShipLoadouts = []baseShipLoadout{")
    for r in rows:
        out.append(
            "\t{loadoutID: %d, hullLine: %s, tier: %d, name: %s, clientNamed: %s, "
            "primary: %d, secondary: %d, abilities: [4]int32{%s}, perks: [4]int32{%s}},"
            % (
                r["id"],
                json.dumps(r["line"]),
                r["tier"],
                json.dumps(r["name"]),
                "true" if r["named_by_client"] else "false",
                r["primary"],
                r["secondary"],
                ", ".join(str(a) for a in r["abilities"]),
                ", ".join(str(p) for p in r["perks"]),
            )
        )
    out.append("}")
    out.append("")

    with open(OUTPUT, "w", encoding="utf-8") as handle:
        handle.write("\n".join(out))
    print(f"wrote {OUTPUT}: {len(rows)} hulls, {from_client} client-named")


if __name__ == "__main__":
    main()
