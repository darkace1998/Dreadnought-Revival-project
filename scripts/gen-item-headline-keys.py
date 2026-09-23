#!/usr/bin/env python3
"""Generate data/assets/ItemHeadlineKeys.json: the localization key that NAMES
each weapon and ability, for every item a ship can research.

The store's offer "name" is a localization KEY the client resolves against its
own string tables (FUN_142a80350); an empty or unknown one renders
"<DNT> Empty Name in Json en" / "<DNT>[[NotFound]]". The per-ship research
offers had no key at all.

Sources, in order of authority:

  1. The item's own blueprint: m_itemUIData.m_headline, read from the cooked
     assets into data/loadouts/ItemHeadlines_cooked.jsonl by

         dotnet tools/bpdump/bin/Debug/net8.0/bpdump.dll --loadouts \\
             DreadGame/Content/Generic/Abilities "*_BP.uasset"
         (and the same for Generic/Weapons), keeping file/m_itemID/m_headline.

     This is the key the game itself uses, tier included: the T0 Tempest
     Missiles blueprint names "Tempest Missiles N", where the older name-matched
     table in mmogbrain/market_localization_keys.go says "... I".

  2. For research items whose blueprint did not dump (27 of 471 on 2026-09-24):
     the module preview table names each row "<ships> <item name>", so the
     longest suffix that is a localized string names the item. Used only when
     that text maps to one key -- or, for 3 items, to several keys carrying the
     SAME English text, where the lowest key is taken (they are interchangeable
     in English; other languages were not checked).

Every key written resolves in the English tables; the script refuses otherwise.

Usage: DN_CLIENT_CONTENT=/path/to/DreadGame/Content python3 scripts/gen-item-headline-keys.py
"""

import collections
import glob
import json
import os
import struct
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
CONTENT = os.environ.get("DN_CLIENT_CONTENT", "/root/projects/DreadGame/Content")
HEADLINES = os.path.join(ROOT, "data/loadouts/ItemHeadlines_cooked.jsonl")
PREVIEW = os.path.join(ROOT, "data/datatables/UI/Module_data_table_v01.json")
OUTPUT = os.path.join(ROOT, "data/assets/ItemHeadlineKeys.json")


def fstring(b, o):
    n = struct.unpack_from("<i", b, o)[0]
    o += 4
    if n == 0:
        return "", o
    if n < 0:  # UTF-16LE, length in characters including the terminator
        n = -n
        return b[o:o + 2 * n].decode("utf-16le").rstrip("\0"), o + 2 * n
    return b[o:o + n].decode("latin1").rstrip("\0"), o + n


def parse_locres(path):
    """UE 4.13 (legacy, magic-less) .locres: namespaces -> key, source hash, text."""
    b = open(path, "rb").read()
    if b[:16] == bytes.fromhex("0E147475674A03FC4A15909DC3377F1B"):
        sys.exit(f"{path}: versioned locres format, not supported")
    out, o = {}, 0
    namespaces = struct.unpack_from("<I", b, o)[0]
    o += 4
    for _ in range(namespaces):
        _, o = fstring(b, o)
        keys = struct.unpack_from("<I", b, o)[0]
        o += 4
        for _ in range(keys):
            key, o = fstring(b, o)
            o += 4  # source string hash
            text, o = fstring(b, o)
            out[key] = text
    return out


def base_id(item_id):
    return (item_id & ~0xFF0000) | 0xFF0000


def main():
    english = {}
    for path in sorted(glob.glob(os.path.join(CONTENT, "Localization/DreadGame/en/*.locres"))):
        english.update(parse_locres(path))
    if not english:
        sys.exit(f"no English .locres under {CONTENT}; set DN_CLIENT_CONTENT")

    keys, sources = {}, {}
    for line in open(HEADLINES):
        row = json.loads(line)
        if english.get(row["m_headline"]):
            keys[int(row["m_itemID"])] = row["m_headline"]
            sources[int(row["m_itemID"])] = "blueprint"

    by_text = collections.defaultdict(set)
    for key, text in english.items():
        by_text[text].add(key)
    names = collections.defaultdict(set)
    for item_id, row in json.load(open(PREVIEW))["rows"].items():
        names[base_id(int(item_id))].add(row["itemName"])

    unresolved = []
    for base, row_names in sorted(names.items()):
        if base in keys:
            continue
        texts = set()
        for name in row_names:
            words = name.split()
            for i in range(len(words)):
                if " ".join(words[i:]) in by_text:
                    texts.add(" ".join(words[i:]))
                    break
        if len(texts) != 1:
            unresolved.append((base, sorted(row_names)))
            continue
        candidates = sorted(by_text[texts.pop()])
        keys[base] = candidates[0]
        sources[base] = "preview-name" if len(candidates) == 1 else "preview-name-equal-text"

    missing = [b for b in names if b not in keys]
    if unresolved or missing:
        sys.exit(f"unresolved research items: {unresolved or missing}")

    out = {str(k): {"key": keys[k], "text": english[keys[k]], "source": sources[k]} for k in sorted(keys)}
    with open(OUTPUT, "w") as f:
        json.dump(out, f, indent=1, sort_keys=True)
        f.write("\n")
    counts = collections.Counter(sources.values())
    print(f"wrote {len(out)} keys to {OUTPUT}: {dict(counts)}; "
          f"research-item bases covered {sum(1 for b in names if b in keys)}/{len(names)}")


if __name__ == "__main__":
    main()
