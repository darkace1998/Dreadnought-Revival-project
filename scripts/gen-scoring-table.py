#!/usr/bin/env python3
"""Generate data/scoring/ScoringTable.json -- the ORIGINAL scoring table.

Source: the community "Dreadnought Datamine" workbook (operator upload,
2026-10-07), sheets "old Scoring 1.11.1" and "old Scoring 1.12.0". Their
columns are exactly the fields the battle server's scoring-row parser reads
(0x2A7A030: EventName, NameForPlayer, GameModes, SummaryCategory,
EventVisibility, Parameters, EventScore, EventXP, EventCredits, RibbonName,
RibbonDesc, EventsForRibbon, RibbonScore, RibbonXP, RibbonCredits), and the
per-mode value strings ("TDM(65) : TE(110) : IVN(30)") are the format its
value parser takes (0x42DF40 drops spaces, then reads "<MODE>(n)").

The workbook's own note: the live game used 1.12.0 scoring for Conquest (TER)
and 1.11.1 for everything else. So: every 1.11.1 row, plus the TER values of
the 1.12.0 rows, plus the rows only 1.12.0 has.

GUESS: TurboTDM (host code TURBO_TDM) is newer than both tables and has no
values; it gets TDM's.

Usage: gen-scoring-table.py <datamine.xlsx> [out.json]
"""
import json
import os
import re
import sys
import xml.etree.ElementTree as ET
import zipfile

NS = {"m": "http://schemas.openxmlformats.org/spreadsheetml/2006/main",
      "r": "http://schemas.openxmlformats.org/officeDocument/2006/relationships"}

# Datamine event names that differ from EYScoringEventID. The enum order of the
# damage events (IncomingRawDamage, ...ThisLife, ...ThisLife2,
# RecentIncomingRawDamageXCloseEnemies) matches the table's order, and the
# creep kills are named by their own assist rows' ribbons (Fighter / Assault
# Ship / Command Ship Battle Assistance = AITargetL/M/HAssist).
ENUM_NAME = {
    "FighterKill": "AITargetL",
    "AssaultShipKill": "AITargetM",
    "CommandShipKill": "AITargetH",
    "MarksMan": "Marksman",
    "SUPPRESSION": "Suppression",
    "MostValuablePlayer": "MVP",
    "DAMAGE TAKEN": "IncomingRawDamage",
    "DAMAGE_TAKEN_DURING_LIFE": "IncomingRawDamageThisLife",
    "DAMAGE_TAKEN_DURING_LIFE2": "IncomingRawDamageThisLife2",
    "BERSERKER": "RecentIncomingRawDamageXCloseEnemies",
    "Contributor": "TerritoryContribution",
    "TerritoryCapturePointDefended": "TerritoryProtectCP",
}

VALUE_FIELDS = ["EventScore", "EventXP", "EventCredits", "RibbonScore", "RibbonXP", "RibbonCredits"]


def load_sheets(path):
    z = zipfile.ZipFile(path)
    strings = []
    if "xl/sharedStrings.xml" in z.namelist():
        for si in ET.fromstring(z.read("xl/sharedStrings.xml")).findall("m:si", NS):
            strings.append("".join(t.text or "" for t in si.iter("{%s}t" % NS["m"])))
    wb = ET.fromstring(z.read("xl/workbook.xml"))
    rels = {r.get("Id"): r.get("Target") for r in ET.fromstring(z.read("xl/_rels/workbook.xml.rels"))}
    out = {}
    for s in wb.find("m:sheets", NS):
        target = rels[s.get("{%s}id" % NS["r"])]
        target = target if target.startswith("xl/") else "xl/" + target.lstrip("/")
        rows = []
        for row in ET.fromstring(z.read(target)).iter("{%s}row" % NS["m"]):
            cells = {}
            for c in row.findall("m:c", NS):
                col = re.match(r"[A-Z]+", c.get("r")).group(0)
                v = c.find("m:v", NS)
                if c.get("t") == "inlineStr":
                    cells[col] = "".join(t.text or "" for t in c.iter("{%s}t" % NS["m"]))
                elif v is not None:
                    cells[col] = strings[int(v.text)] if c.get("t") == "s" else v.text
            rows.append(cells)
        out[s.get("name")] = rows
    return out


def table(rows):
    header = next(r for r in rows if r.get("A") == "EventName")
    cols = {v: k for k, v in header.items()}
    out = []
    for r in rows[rows.index(header) + 1:]:
        if not r.get("A"):
            continue
        out.append({name: (r.get(col) or "").strip() for name, col in cols.items()})
    return out


def segments(value):
    """'TDM(65) : TM (10)' -> [('TDM', '65'), ('TM', '10')]; a plain number -> []."""
    return re.findall(r"([A-Za-z_]+)\s*\(\s*(-?[\d.]+)\s*\)", value or "")


def number(v):
    v = (v or "").strip()
    if not v:
        return 0
    return int(float(v))


def modes(value):
    return [m for m in (value or "").replace(" ", "").split(":") if m]


def main():
    if len(sys.argv) < 2:
        sys.exit(__doc__)
    sheets = load_sheets(sys.argv[1])
    base = table(sheets["old Scoring 1.11.1"])
    newer = {r["EventName"]: r for r in table(sheets["old Scoring 1.12.0"])}
    by_name = {r["EventName"]: r for r in base}

    # 1.12.0's Conquest values onto the 1.11.1 rows, and its new rows.
    for name, r12 in newer.items():
        if "TER" not in modes(r12["GameModes"]):
            if name not in by_name:
                base.append(dict(r12))
                by_name[name] = base[-1]
            continue
        row = by_name.get(name)
        if row is None:
            base.append(dict(r12))
            by_name[name] = base[-1]
            continue
        for f in VALUE_FIELDS:
            ter = [v for m, v in segments(r12[f]) if m == "TER"]
            if ter:
                row[f] = (row[f] + " : " if row[f] else "") + "TER(%s)" % ter[0]
        if "TER" not in modes(row["GameModes"]):
            row["GameModes"] = (row["GameModes"] + " : " if row["GameModes"] else "") + "TER"

    # GUESS: TurboTDM scores as TDM.
    for row in base:
        for f in VALUE_FIELDS:
            tdm = [v for m, v in segments(row[f]) if m == "TDM"]
            if tdm and "TURBO_TDM(" not in row[f].replace(" ", ""):
                row[f] += " : TURBO_TDM(%s)" % tdm[0]
        if "TDM" in modes(row["GameModes"]):
            row["GameModes"] += " : TURBO_TDM"

    out = []
    for row in base:
        out.append({
            "EventName": row["EventName"],
            "Enum": ENUM_NAME.get(row["EventName"], row["EventName"]),
            "EventVisibility": number(row.get("EventVisibility")),
            "NameForPlayer": row.get("NameForPlayer", ""),
            "EventScore": row.get("EventScore", ""),
            "EventXP": row.get("EventXP", ""),
            "EventCredits": row.get("EventCredits", ""),
            "Parameters": row.get("Parameters", ""),
            "EventsForRibbon": number(row.get("EventsForRibbon")),
            "RibbonName": row.get("RibbonName", ""),
            "RibbonDesc": row.get("RibbonDesc", ""),
            "RibbonScore": row.get("RibbonScore", ""),
            "RibbonXP": row.get("RibbonXP", ""),
            "RibbonCredits": row.get("RibbonCredits", ""),
            "SummaryCategory": row.get("SummaryCategory", ""),
            "GameModes": row.get("GameModes", ""),
        })
    dest = sys.argv[2] if len(sys.argv) > 2 else os.path.join(
        os.path.dirname(os.path.abspath(__file__)), "..", "data", "scoring", "ScoringTable.json")
    os.makedirs(os.path.dirname(dest), exist_ok=True)
    with open(dest, "w") as f:
        json.dump({"source": "Dreadnought Datamine: old Scoring 1.11.1 + 1.12.0 TER (TURBO_TDM = TDM, GUESS)",
                   "rows": out}, f, indent=1, ensure_ascii=False)
        f.write("\n")
    print("%d rows -> %s" % (len(out), dest))


if __name__ == "__main__":
    main()
