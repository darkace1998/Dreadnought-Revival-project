# bpdump

Reads the client's cooked `.uasset` files with
[UAssetAPI](https://github.com/atenfyr/UAssetAPI). Used to produce the ground
truth the ship roster is validated against:

```bash
# UAssetAPI must be checked out BESIDE this repo (the csproj points at
# ../../../UAssetAPI). Known-good commit: 33ef77e.
git clone https://github.com/atenfyr/UAssetAPI ../UAssetAPI

cd tools/bpdump && dotnet build -c Debug && cd ../..
BPDUMP="dotnet tools/bpdump/bin/Debug/net8.0/bpdump.dll"
CONTENT=../DreadGame/Content        # the extracted client Content tree

$BPDUMP --loadouts "$CONTENT" > data/loadouts/PrecastLoadouts_cooked.jsonl
# "*.uasset", NOT "*_HeroLoadout_BP.uasset": Phoenix is spelled
# _Heroloadout_BP and Linux globs are case-sensitive.
$BPDUMP --loadouts "$CONTENT/Generic/Loadouts/Hero" "*.uasset" \
    > data/loadouts/HeroLoadouts_cooked.jsonl

python3 scripts/validate-precast-loadouts.py
```

`--loadouts` writes one JSON object per blueprint: the `Default__` export's
top-level properties, with every object reference **resolved to its asset
path**. (`--props` prints bare import indexes, which do not say which weapon or
ability a slot holds.)

Other modes: `--props <file> [filter]`, `--scan <dir> <terms...>`,
`--names <dir> <terms...>`, `--setbool ...`, and a bytecode disassembly when
given just a file.
