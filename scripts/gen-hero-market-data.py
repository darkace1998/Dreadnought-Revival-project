#!/usr/bin/env python3
"""Generate the Market's hero-ship text and images from the client's own data.

The Market is fed in the original store's ("Aviary") format: an offer's
"name" and "description" are objects keyed by locale ({"en": ..., "de": ...}),
and its pictures are URLs. The game ships both halves of that for every hero
ship, just not in that shape:

  - text: each hero loadout blueprint names localization keys
    (m_itemUIData.m_headline / m_subline / m_description), resolved here in
    every language the client ships (Content/Localization/DreadGame/<lang>/
    DreadGame.locres);
  - picture: m_itemUIData.m_highResIcon, a cooked Texture2D (PF_DXT5 or
    PF_DXT1, one inline mip), decoded here to PNG.

Also resolves the few Market texts that are not hero blueprints (STRINGS), such
as the one original bundle description the client still carries.

Writes data/assets/HeroMarketData.json and data/market-images/<icon>.png.
Pure stdlib.

    python3 scripts/gen-hero-market-data.py [/path/to/DreadGame]
"""
import json
import os
import struct
import sys
import zlib

REPO = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
GAME = sys.argv[1] if len(sys.argv) > 1 else os.path.join(os.path.dirname(REPO), "DreadGame")
LOC = os.path.join(GAME, "Content", "Localization", "DreadGame")
HEROES = os.path.join(REPO, "data", "loadouts", "HeroLoadouts_cooked.jsonl")
OUT_JSON = os.path.join(REPO, "data", "assets", "HeroMarketData.json")
OUT_IMG = os.path.join(REPO, "data", "market-images")
VANITY = os.path.join(REPO, "data", "vanity", "VanityItems_cooked.jsonl")
OUT_VANITY_TEXT = os.path.join(REPO, "data", "vanity", "VanityTexts.json")
OUT_TEMPLATES = os.path.join(REPO, "data", "vanity", "CaptainTemplates.json")
TEMPLATES = {g: os.path.join(GAME, "Content", "Generic", "Captain", "CharacterTemplates", "Captain_Template_%s_01.uasset" % g)
             for g in ("Male", "Female")}


# Market texts outside the hero blueprints, by localization key.
STRINGS = {
    # "Kick-start your career as a mercenary captain. ... The Vanguard Bundle
    # contains everything a new recruit needs to become a legend."
    "VanguardBundleDescription": "C0E97B7A405588EF5A18D0BAFD3D5349",
}


def read_fstring(d, o):
    n = struct.unpack_from("<i", d, o)[0]
    o += 4
    if n == 0:
        return "", o
    if n < 0:
        return d[o:o - 2 * n].decode("utf-16le").rstrip("\0"), o - 2 * n
    return d[o:o + n].decode("latin1").rstrip("\0"), o + n


def parse_locres(path):
    """UE 4.13 .locres (no magic): namespaces -> keys -> (hash, string)."""
    d = open(path, "rb").read()
    o = 0
    out = {}
    (namespaces,) = struct.unpack_from("<I", d, o)
    o += 4
    for _ in range(namespaces):
        ns, o = read_fstring(d, o)
        (keys,) = struct.unpack_from("<I", d, o)
        o += 4
        for _ in range(keys):
            key, o = read_fstring(d, o)
            o += 4  # source string hash
            text, o = read_fstring(d, o)
            if ns == "":
                out[key] = text
    return out


def rgb565(c):
    r, g, b = (c >> 11) & 31, (c >> 5) & 63, c & 31
    return (r << 3 | r >> 2, g << 2 | g >> 4, b << 3 | b >> 2)


def color_block(b, opaque_four):
    c0, c1, bits = struct.unpack_from("<HHI", b)
    p0, p1 = rgb565(c0), rgb565(c1)
    if c0 > c1 or opaque_four:
        p2 = tuple((2 * x + y) // 3 for x, y in zip(p0, p1))
        p3 = tuple((x + 2 * y) // 3 for x, y in zip(p0, p1))
        pal, alpha = [p0, p1, p2, p3], [255] * 4
    else:
        pal = [p0, p1, tuple((x + y) // 2 for x, y in zip(p0, p1)), (0, 0, 0)]
        alpha = [255, 255, 255, 0]
    return [(pal[(bits >> 2 * i) & 3], alpha[(bits >> 2 * i) & 3]) for i in range(16)]


def alpha_block(b):
    a0, a1 = b[0], b[1]
    bits = int.from_bytes(b[2:8], "little")
    if a0 > a1:
        pal = [a0, a1] + [((7 - i) * a0 + i * a1) // 7 for i in range(1, 7)]
    else:
        pal = [a0, a1] + [((5 - i) * a0 + i * a1) // 5 for i in range(1, 5)] + [0, 255]
    return [pal[(bits >> 3 * i) & 7] for i in range(16)]


def decode_texture(path):
    """Width, height, RGBA rows of a cooked UI Texture2D (one inline mip)."""
    d = open(path, "rb").read()
    for fmt, block in ((b"PF_DXT5", 16), (b"PF_DXT1", 8)):
        tag = struct.pack("<i", len(fmt) + 1) + fmt + b"\0"
        # The name table spells the format the same way; the platform data's
        # copy is the last one.
        i = d.rfind(tag)
        if i < 0:
            continue
        w, h = struct.unpack_from("<ii", d, i - 12)
        o = i + len(tag)
        o += 4  # first mip
        (mips,) = struct.unpack_from("<i", d, o)
        o += 4
        if mips < 1:
            return None
        o += 4  # bCooked
        _flags, count, _size, offset = struct.unpack_from("<IiiQ", d, o)
        data = d[offset:offset + count]
        bw, bh = (w + 3) // 4, (h + 3) // 4
        if len(data) < bw * bh * block:
            return None
        px = [bytearray(w * 4) for _ in range(h)]
        for by in range(bh):
            for bx in range(bw):
                b = data[(by * bw + bx) * block:(by * bw + bx + 1) * block]
                if block == 16:
                    alphas = alpha_block(b[:8])
                    colors = color_block(b[8:], True)
                    texels = [(c, a) for (c, _), a in zip(colors, alphas)]
                else:
                    texels = color_block(b, False)
                for t, (c, a) in enumerate(texels):
                    x, y = bx * 4 + t % 4, by * 4 + t // 4
                    if x < w and y < h:
                        px[y][x * 4:x * 4 + 4] = bytes((*c, a))
        return w, h, px
    return None


def write_png(path, w, h, rows):
    raw = b"".join(b"\0" + bytes(r) for r in rows)

    def chunk(kind, body):
        return struct.pack(">I", len(body)) + kind + body + struct.pack(">I", zlib.crc32(kind + body))

    png = b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", struct.pack(">IIBBBBB", w, h, 8, 6, 0, 0, 0))
    png += chunk(b"IDAT", zlib.compress(raw, 9)) + chunk(b"IEND", b"")
    open(path, "wb").write(png)


def asset_file(object_path):
    # /Game/Generic/UI/ships/Tiers/UI_X.UI_X -> <GAME>/Content/Generic/UI/ships/Tiers/UI_X.uasset
    package = object_path.split(".")[0]
    return os.path.join(GAME, "Content", package[len("/Game/"):] + ".uasset"), os.path.basename(package)


def main():
    languages = sorted(l for l in os.listdir(LOC) if os.path.isfile(os.path.join(LOC, l, "DreadGame.locres")))
    tables = {l: parse_locres(os.path.join(LOC, l, "DreadGame.locres")) for l in languages}

    def localized(key):
        texts = {l: tables[l][key] for l in languages if tables[l].get(key)}
        return texts or None

    os.makedirs(OUT_IMG, exist_ok=True)
    heroes = {}
    for line in open(HEROES):
        row = json.loads(line)
        item_id = row.get("m_itemSystemData", {}).get("m_itemID")
        ui = row.get("m_itemUIData") or {}
        if not item_id:
            continue
        entry = {}
        for field, key in (("name", "m_headline"), ("subline", "m_subline"), ("description", "m_description")):
            text = localized(ui.get(key, ""))
            if text:
                entry[field] = text
        icon = ui.get("m_highResIcon") or ui.get("m_icon")
        if icon:
            path, name = asset_file(icon)
            out = os.path.join(OUT_IMG, name + ".png")
            if os.path.exists(out):
                entry["image"] = name + ".png"
            elif os.path.exists(path):
                decoded = decode_texture(path)
                if decoded:
                    write_png(out, *decoded)
                    entry["image"] = name + ".png"
        heroes[str(item_id)] = entry
        print(item_id, entry.get("name", {}).get("en"), entry.get("image"))
    strings = {name: localized(key) for name, key in STRINGS.items() if localized(key)}
    write_vanity(localized)
    with open(OUT_JSON, "w") as f:
        json.dump({"heroes": heroes, "strings": strings}, f, ensure_ascii=False, indent=1, sort_keys=True)
    print(len(heroes), "heroes ->", OUT_JSON)


def write_vanity(localized):
    """Cosmetics: their texts in every language, and which captain items each
    gender's template (Captain_Template_<g>_01, a YCharacterTemplate with
    m_meshes / m_materials) lets a captain wear.

    VanityTexts.json: {"<item id>": {"name": {...}, "subline": {...},
    "description": {...}}} from m_itemUIData's keys. CaptainTemplates.json:
    {"Male": [ids], "Female": [ids]} -- the template's references are imports,
    so its name table is matched against the cosmetics' export names.
    """
    rows = [json.loads(l) for l in open(VANITY)]
    texts = {}
    by_name = {}
    for row in rows:
        item_id = row.get("m_itemSystemData", {}).get("m_itemID")
        if not item_id:
            continue
        by_name.setdefault(row["export"], []).append(item_id)
        ui = row.get("m_itemUIData") or {}
        entry = {}
        for field, key in (("name", "m_headline"), ("subline", "m_subline"), ("description", "m_description")):
            text = localized(ui.get(key, ""))
            if text:
                entry[field] = text
        if entry:
            texts[str(item_id)] = entry
    with open(OUT_VANITY_TEXT, "w") as f:
        json.dump(texts, f, ensure_ascii=False, sort_keys=True)
    templates = {}
    for gender, path in TEMPLATES.items():
        data = open(path, "rb").read()
        names = set()
        word = bytearray()
        for b in data:
            if 32 < b < 127:
                word.append(b)
                continue
            if len(word) > 3:
                names.add(word.decode())
            word = bytearray()
        ids = sorted({i for n in names for i in by_name.get(n, []) if 50 <= (i >> 24) <= 55})
        templates[gender] = ids
    with open(OUT_TEMPLATES, "w") as f:
        json.dump(templates, f, indent=1, sort_keys=True)
    print(len(texts), "cosmetic texts ->", OUT_VANITY_TEXT)
    print({g: len(v) for g, v in templates.items()}, "template items ->", OUT_TEMPLATES)


if __name__ == "__main__":
    main()
