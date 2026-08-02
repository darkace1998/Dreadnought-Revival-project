// Decompile AND disassemble a list of RVAs in one headless run.
//
// DecompileTarget handles a single RVA and only emits C; the loadout work needs
// several functions plus the raw instructions (field offsets survive in the
// disassembly even when the decompiler invents locals). Args are RVAs in hex,
// with or without a 0x prefix.
import ghidra.app.script.GhidraScript;
import ghidra.app.decompiler.DecompInterface;
import ghidra.app.decompiler.DecompileResults;
import ghidra.program.model.address.Address;
import ghidra.program.model.listing.Function;
import ghidra.program.model.listing.Instruction;
import ghidra.program.model.listing.InstructionIterator;
import java.io.PrintWriter;

public class DumpFuncs extends GhidraScript {
    @Override
    public void run() throws Exception {
        String[] args = getScriptArgs();
        if (args.length == 0) {
            println("Usage: DumpFuncs <rva_hex> [<rva_hex> ...]");
            return;
        }

        long imageBase = currentProgram.getImageBase().getOffset();
        DecompInterface iface = new DecompInterface();
        iface.openProgram(currentProgram);

        for (String raw : args) {
            String rvaStr = raw.replace("0x", "").replace("0X", "").trim();
            if (rvaStr.isEmpty()) continue;
            long rva = Long.parseUnsignedLong(rvaStr, 16);
            Address addr = currentProgram.getImageBase().add(rva);

            Function func = getFunctionContaining(addr);
            // Override with GHIDRA_DUMP_DIR; defaults to ./ghidra_output.
            String dir = System.getenv("GHIDRA_DUMP_DIR");
            if (dir == null || dir.isEmpty()) dir = "ghidra_output";
            new java.io.File(dir).mkdirs();
            String outPath = dir + java.io.File.separator + "dump_"
                             + rvaStr.toUpperCase() + ".txt";
            PrintWriter pw = new PrintWriter(outPath);

            if (func == null) {
                // Not yet a recognised function - disassemble forward anyway so a
                // missing entry never silently yields an empty answer.
                pw.println("!! No function contains RVA 0x" + rvaStr);
                pw.close();
                println("RVA 0x" + rvaStr + ": NO FUNCTION");
                continue;
            }

            pw.println("=== " + func.getName() + " @ RVA 0x" + rvaStr
                       + " (entry 0x" + Long.toHexString(
                             func.getEntryPoint().getOffset() - imageBase)
                       + ", size " + func.getBody().getNumAddresses() + ") ===");
            pw.println();

            DecompileResults res = iface.decompileFunction(func, 180, monitor);
            if (res != null && res.getDecompiledFunction() != null) {
                pw.println("---- DECOMPILED ----");
                pw.println(res.getDecompiledFunction().getC());
            } else {
                pw.println("---- DECOMPILE FAILED ----");
            }

            pw.println();
            pw.println("---- DISASSEMBLY ----");
            InstructionIterator it =
                currentProgram.getListing().getInstructions(func.getBody(), true);
            while (it.hasNext()) {
                Instruction inst = it.next();
                pw.println(String.format("0x%X: %s",
                    inst.getAddress().getOffset() - imageBase, inst.toString()));
            }
            pw.close();
            println("RVA 0x" + rvaStr + " -> " + outPath);
        }
        iface.dispose();
    }
}
