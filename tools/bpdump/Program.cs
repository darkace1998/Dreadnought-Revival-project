using UAssetAPI;
using UAssetAPI.UnrealTypes;
using UAssetAPI.ExportTypes;
using UAssetAPI.Kismet;
using UAssetAPI.PropertyTypes.Objects;
using UAssetAPI.PropertyTypes.Structs;

if (args.Length < 1)
{
    Console.WriteLine("usage: bpdump <path.uasset> [search term]");
    Console.WriteLine("       bpdump --scan <dir> <term1> [term2 ...]  (all terms must appear)");
    Console.WriteLine("       bpdump --names <dir> <term1> [term2 ...]");
    Console.WriteLine("       bpdump --loadouts <dir> [pattern]  (JSONL, default *PrecastLoadout_BP.uasset)");
    Console.WriteLine("       bpdump --setbool <path.uasset> <Default__ExportName> <PropertyName> <true|false>");
    return;
}

if (args[0] == "--setbool")
{
    string path = args[1];
    string exportName = args[2];
    string propName = args[3];
    bool value = args[4] == "true";

    var asset = new UAsset(path, EngineVersion.VER_UE4_13);
    NormalExport? target = null;
    foreach (var export in asset.Exports)
    {
        if (export.ObjectName.ToString() == exportName && export is NormalExport ne)
        {
            target = ne;
            break;
        }
    }
    if (target == null)
    {
        Console.WriteLine($"export '{exportName}' not found");
        return;
    }

    BoolPropertyData? existing = null;
    foreach (var prop in target.Data)
    {
        if (prop.Name.ToString() == propName && prop is BoolPropertyData bpd)
        {
            existing = bpd;
            break;
        }
    }

    if (existing != null)
    {
        Console.WriteLine($"found existing {propName} = {existing.Value}, setting to {value}");
        existing.Value = value;
    }
    else
    {
        Console.WriteLine($"{propName} not present as an explicit property, adding new BoolPropertyData = {value}");
        var newProp = new BoolPropertyData(new FName(asset, propName)) { Value = value };
        target.Data.Add(newProp);
    }

    asset.Write(path);
    Console.WriteLine("wrote " + path);
    return;
}

if (args[0] == "--loadouts")
{
    // One JSON object per line for every *PrecastLoadout_BP.uasset under <dir>:
    // the Default__ export's top-level properties, with every object reference
    // RESOLVED to its asset path (the --props mode prints bare import indexes,
    // which say nothing about which weapon or ability a slot holds).
    string dir = args[1];
    string pattern = args.Length > 2 ? args[2] : "*PrecastLoadout_BP.uasset";
    foreach (var f in Directory.EnumerateFiles(dir, pattern, SearchOption.AllDirectories).OrderBy(x => x))
    {
        UAsset a;
        try { a = new UAsset(f, EngineVersion.VER_UE4_13); }
        catch (Exception e) { Console.WriteLine(Newtonsoft.Json.JsonConvert.SerializeObject(new { file = f, error = e.Message })); continue; }
        // Normalise to "DreadGame/Content/<rest>" so the dump does not depend on
        // the directory it was run from (consumers split on "/Content/").
        string norm = f.Replace('\\', '/');
        int ci = norm.LastIndexOf("/Content/");
        string fileKey = ci >= 0 ? "DreadGame" + norm.Substring(ci) : norm;
        var row = new Dictionary<string, object?> { ["file"] = fileKey };
        foreach (var ex in a.Exports)
        {
            if (ex is not NormalExport ne || !ne.ObjectName.ToString().StartsWith("Default__")) continue;
            row["export"] = ne.ObjectName.ToString();
            foreach (var prop in ne.Data) row[prop.Name.ToString()] = Flat(prop, a);
        }
        Console.WriteLine(Newtonsoft.Json.JsonConvert.SerializeObject(row));
    }
    return;
}

object? Flat(PropertyData prop, UAsset a)
{
    switch (prop)
    {
        case ObjectPropertyData op: return Resolve(op.Value, a);
        case ArrayPropertyData ap: return ap.Value.Select(v => Flat(v, a)).ToList();
        case StructPropertyData sp:
            var d = new Dictionary<string, object?>();
            foreach (var c in sp.Value) d[c.Name.ToString()] = Flat(c, a);
            return d;
        case IntPropertyData ip: return ip.Value;
        case BoolPropertyData bp: return bp.Value;
        case FloatPropertyData fp: return fp.Value;
        case BytePropertyData byp: try { return byp.Value; } catch { return byp.ToString(); }
        default:
            try { return prop.RawValue?.ToString(); } catch { return null; }
    }
}

string? Resolve(FPackageIndex idx, UAsset a)
{
    if (idx == null || idx.Index == 0) return null;
    if (idx.IsImport())
    {
        var imp = idx.ToImport(a);
        var parts = new List<string> { imp.ObjectName.ToString() };
        var outer = imp.OuterIndex;
        while (outer != null && outer.Index != 0 && outer.IsImport())
        {
            var o = outer.ToImport(a);
            parts.Insert(0, o.ObjectName.ToString());
            outer = o.OuterIndex;
        }
        return string.Join(".", parts);
    }
    return "export:" + idx.ToExport(a).ObjectName.ToString();
}

if (args[0] == "--props")
{
    // Dump the serialized properties of every non-function export, so data
    // assets (YUIData and friends) can be read, not just Blueprint bytecode.
    string path = args[1];
    string? filter = args.Length > 2 ? args[2] : null;
    var a = new UAsset(path, EngineVersion.VER_UE4_13);
    KismetSerializer.asset = a;
    for (int i = 0; i < a.Exports.Count; i++)
    {
        if (a.Exports[i] is not NormalExport ne) continue;
        var text = new System.Text.StringBuilder();
        Dump(ne.Data, 1, text);
        string body = text.ToString();
        if (filter != null && !body.Contains(filter, StringComparison.OrdinalIgnoreCase)
            && !ne.ObjectName.ToString().Contains(filter, StringComparison.OrdinalIgnoreCase)) continue;
        Console.WriteLine($"==== EXPORT {i} {ne.ObjectName} ({ne.GetExportClassType()}) ====");
        Console.Write(body);
        Console.WriteLine();
    }
    return;
}

void Dump(IList<PropertyData> props, int depth, System.Text.StringBuilder sb)
{
    string pad = new string(' ', depth * 2);
    foreach (var prop in props)
    {
        switch (prop)
        {
            case StructPropertyData sp:
                sb.AppendLine($"{pad}{sp.Name} ({sp.StructType}) {{");
                Dump(sp.Value, depth + 1, sb);
                sb.AppendLine($"{pad}}}");
                break;
            case ArrayPropertyData ap:
                sb.AppendLine($"{pad}{ap.Name} [{ap.Value.Length}] {{");
                Dump(ap.Value, depth + 1, sb);
                sb.AppendLine($"{pad}}}");
                break;
            case MapPropertyData mp:
                sb.AppendLine($"{pad}{mp.Name} (map, {mp.Value.Count} entries)");
                break;
            default:
                string v;
                try { v = prop.RawValue?.ToString() ?? prop.ToString() ?? ""; }
                catch { v = "<unreadable>"; }
                if (string.IsNullOrEmpty(v)) { try { v = prop.ToString() ?? ""; } catch { v = ""; } }
                sb.AppendLine($"{pad}{prop.Name} = {v}   [{prop.PropertyType}]");
                break;
        }
    }
}

if (args[0] == "--names")
{
    string dir = args[1];
    string[] terms = args.Skip(2).ToArray();
    var files = Directory.EnumerateFiles(dir, "*.uasset", SearchOption.AllDirectories).ToList();
    int done = 0;
    foreach (var f in files)
    {
        done++;
        if (done % 2000 == 0) Console.Error.WriteLine($"...{done}/{files.Count}");
        UAsset a;
        try { a = new UAsset(f, EngineVersion.VER_UE4_13); KismetSerializer.asset = a; }
        catch { continue; }
        try
        {
            var names = a.GetNameMapIndexList().Select(n => n.ToString()).ToList();
            foreach (var t in terms)
            {
                foreach (var n in names)
                {
                    if (n.Contains(t, StringComparison.OrdinalIgnoreCase))
                    {
                        Console.WriteLine($"NAME-MATCH: {f} :: {n}");
                    }
                }
            }
        }
        catch { continue; }
    }
    return;
}

if (args[0] == "--scan")
{
    string dir = args[1];
    string[] terms = args.Skip(2).ToArray();
    var files = Directory.EnumerateFiles(dir, "*.uasset", SearchOption.AllDirectories).ToList();
    int done = 0;
    foreach (var f in files)
    {
        done++;
        if (done % 1000 == 0) Console.Error.WriteLine($"...{done}/{files.Count}");
        UAsset a;
        try { a = new UAsset(f, EngineVersion.VER_UE4_13); KismetSerializer.asset = a; }
        catch { continue; }

        foreach (var export in a.Exports)
        {
            if (export is not FunctionExport fe || fe.ScriptBytecode == null) continue;
            string dis;
            try { dis = KismetSerializer.SerializeScript(fe.ScriptBytecode).ToString(); }
            catch { continue; }
            if (terms.All(t => dis.Contains(t, StringComparison.OrdinalIgnoreCase)))
            {
                Console.WriteLine($"MATCH: {f} :: {export.ObjectName}");
            }
        }
    }
    return;
}

{
    string path = args[0];
    string? term = args.Length > 1 ? args[1] : null;

    var asset = new UAsset(path, EngineVersion.VER_UE4_13);
    KismetSerializer.asset = asset;

    for (int i = 0; i < asset.Exports.Count; i++)
    {
        var export = asset.Exports[i];
        if (export is FunctionExport fe)
        {
            string dis;
            try
            {
                dis = fe.ScriptBytecode != null
                    ? KismetSerializer.SerializeScript(fe.ScriptBytecode).ToString()
                    : "";
            }
            catch (Exception ex)
            {
                dis = "<disasm error: " + ex.Message + ">";
            }
            if (term == null || export.ObjectName.ToString().Contains(term, StringComparison.OrdinalIgnoreCase) || dis.Contains(term, StringComparison.OrdinalIgnoreCase))
            {
                Console.WriteLine($"==== FUNCTION {export.ObjectName} (export {i}) ====");
                Console.WriteLine(dis);
                Console.WriteLine();
            }
        }
    }
}
