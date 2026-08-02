#!/usr/bin/env python3
"""Generate data/assets/HullNames.json from the client's own precast loadout blueprints.

Why this exists
---------------

`ItemIDConversionTable` is the server's naming authority and mostly deserves to
be: resolving through it killed a set of invented, hand-written names. But it is
an OldItemID -> NewItemID translation, and its `Name` column is the name from the
OLDER build. For any hull that was renamed between builds you therefore get the
current id paired with the LEGACY name, and an audit against the table cannot
surface it -- the table is internally consistent, it is just describing a
different build.

Four hulls are affected, cross-checked independently against a live client by the
client-side half of this project (AGENT-CHAT.md, C5) and confirmed here:

    ScoutLight  T3   table says Lerwick   client shows Machias
    ScoutLight  T5   table says Bakar     client shows Nevis
    AssaultHeavy T3  table says Kama      client shows Dola
    ScoutHeavy  T4   table says Perun     client shows Stribog

The fix does not need the conversion table at all: every precast loadout
blueprint carries its own display name, and that blueprint is what the client
loads. This reads it out of the assets you already have.

Where the name lives in the asset
---------------------------------

Each blueprint's default object holds a run of FText properties, serialised as

    <int32 len><namespace\\0><int32 len><key GUID\\0><int32 len><source string\\0>

with an empty namespace and a 32-hex key. The first two in a precast loadout are
always the display name and the hull subclass, in that order, followed by the
description. That ordering is checked, not assumed -- see VALIDATION below.

Usage
-----

    DREADGAME_CONTENT=/path/to/extracted/DreadGame/Content \\
        python3 scripts/gen-hull-names.py

No game content is committed by this script beyond the 52 hull names and their
subclasses, which is the same category of small factual list as the other files
in data/assets/.

VALIDATION
----------

The extraction is order-based, which is exactly the kind of thing that breaks
silently, so two independent checks run over every asset and any failure aborts
the whole generation rather than writing a partly-wrong file:

1. The second FText must be one of the five hull subclasses the game has.
2. It must agree with the class in the asset's own FILENAME -- VH_Scout* is a
   Corvette, VH_Assault* a Destroyer, and so on. If the FText order were wrong
   this check could not pass, because the strings that would land there instead
   (descriptions, tooltip labels) are nothing like a subclass.

Both held for all 52 hulls when this was written.
"""

import json
import os
import re
import struct
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
OUTPUT = os.path.join(ROOT, "data/assets/HullNames.json")

# The subclass the game gives each ship class. Used as a cross-check, not as
# data: the value written out is the one read from the asset.
SUBCLASS_FOR_CLASS = {
    "Scout": "Corvette",
    "Assault": "Destroyer",
    "Dreadnought": "Dreadnought",
    "Sniper": "Artillery Cruiser",
    "Support": "Tactical Cruiser",
}

# VH_<Class><Size>_T<n>_PrecastLoadout_BP, plus the one hull that puts its tier
# after "PrecastLoadout" instead of before it (AssaultLight T5). Matching only
# the usual shape has silently dropped that hull before.
ASSET_NAME = re.compile(
    r"^VH_(Scout|Assault|Dreadnought|Sniper|Support)(Light|Medium|Heavy)"
    r"(?:_T(\d))?_PrecastLoadout(?:_T(\d))?_BP$"
)

GAME_PATH_PREFIX = "/Game/Generic/Loadouts/Precast"


def content_root():
    root = os.environ.get("DREADGAME_CONTENT", "")
    for arg in sys.argv[1:]:
        if arg.startswith("--content="):
            root = arg.split("=", 1)[1]
    if not root:
        sys.exit(
            "Set DREADGAME_CONTENT to the extracted client's Content directory, e.g.\n"
            "  DREADGAME_CONTENT=/path/to/DreadGame/Content python3 scripts/gen-hull-names.py"
        )
    precast = os.path.join(root, "Generic/Loadouts/Precast")
    if not os.path.isdir(precast):
        sys.exit("No Generic/Loadouts/Precast under %s -- is that the Content directory?" % root)
    return precast


def ftexts(data, limit=2):
    """Return the first `limit` FText source strings in an asset.

    Anchored on the 32-hex key preceded by its own int32 length (33 = 32 chars
    plus the null terminator), which is specific enough not to collide with
    ordinary strings in these assets.
    """
    out = []
    for match in re.finditer(rb"[0-9A-F]{32}\x00", data):
        start = match.start()
        if start < 4 or struct.unpack_from("<i", data, start - 4)[0] != 33:
            continue
        after = match.end()
        if after + 4 > len(data):
            continue
        length = struct.unpack_from("<i", data, after)[0]
        if not 0 < length < 8000:
            continue
        out.append(data[after + 4 : after + 4 + length - 1].decode("utf-8", "replace"))
        if len(out) >= limit:
            break
    return out


def main():
    precast = content_root()
    hulls = []
    problems = []

    for tier_dir in sorted(os.listdir(precast)):
        if not re.fullmatch(r"T\d", tier_dir):
            continue  # Havoc, PVE, Special, TM, Tutorial are not player hulls
        tier_path = os.path.join(precast, tier_dir)
        for filename in sorted(os.listdir(tier_path)):
            if not filename.endswith(".uasset"):
                continue
            base = filename[: -len(".uasset")]
            match = ASSET_NAME.match(base)
            if match is None:
                problems.append("unrecognised asset name: %s/%s" % (tier_dir, base))
                continue
            ship_class, size, tier_a, tier_b = match.groups()
            tier = tier_a or tier_b or tier_dir[1:]

            with open(os.path.join(tier_path, filename), "rb") as handle:
                texts = ftexts(handle.read())
            if len(texts) < 2:
                problems.append("%s: fewer than two FTexts" % base)
                continue
            name, subclass = texts[0].strip(), texts[1].strip()

            expected = SUBCLASS_FOR_CLASS[ship_class]
            if subclass != expected:
                problems.append(
                    "%s: subclass %r does not match the %s class (expected %r) -- "
                    "the FText order may have changed" % (base, subclass, ship_class, expected)
                )
                continue
            if not name or len(name) > 40:
                problems.append("%s: implausible display name %r" % (base, name))
                continue

            hulls.append(
                {
                    "asset": "%s/%s/%s" % (GAME_PATH_PREFIX, tier_dir, base),
                    "name": name,
                    "subclass": subclass,
                    "class": ship_class,
                    "size": size,
                    "tier": int(tier),
                }
            )

    if problems:
        sys.stderr.write("Refusing to write %s:\n" % OUTPUT)
        for problem in problems:
            sys.stderr.write("  %s\n" % problem)
        sys.exit(1)

    hulls.sort(key=lambda h: h["asset"])
    with open(OUTPUT, "w", encoding="utf-8") as handle:
        json.dump(
            {
                "_comment": (
                    "Generated by scripts/gen-hull-names.py from the client's precast "
                    "loadout blueprints. Do not edit by hand. These names override "
                    "ItemIDConversionTable, whose Name column carries the previous "
                    "build's name for any hull that was renamed."
                ),
                "hulls": hulls,
                "hull_count": len(hulls),
            },
            handle,
            indent=1,
            ensure_ascii=False,
        )
        handle.write("\n")
    print("wrote %s (%d hulls)" % (OUTPUT, len(hulls)))


if __name__ == "__main__":
    main()
