# Dreadnought WER Proxy Diagnostics

This tool builds a benign app-local `wer.dll` shim for Dreadnought diagnostics. It mirrors the community patch loading pattern by loading an optional sibling `Dreadnought.dll` on process attach, then forwards the WER calls used by the client to the real `C:\Windows\System32\wer.dll`.

The DLL logs immediately when it is loaded, so lack of a log means Windows did not load this app-local `wer.dll`.

## Build

```bash
bash tools/wer-proxy/build.sh
```

The output is written to `bin/wer-proxy/wer.dll`.

## Use

Use `bin/wer-proxy/wer.dll` as the app-local `wer.dll` beside `DreadGame-Win64-Shipping.exe`. If a `Dreadnought.dll` client mod is also present beside it, the shim loads it automatically. Remove the app-local `wer.dll` to disable the shim.

The proxy writes these files beside the DLL:

- `dreadnought_wer_proxy.log`: load, companion-DLL, and WER call log.
- `dreadnought_wer_diagnostics.txt`: small crash-report attachment with process paths and launch state.

Interpretation:

- No `dreadnought_wer_proxy.log`: this `wer.dll` was not loaded. Check that it is beside `DreadGame-Win64-Shipping.exe`, not only beside the launcher.
- `companion Dreadnought.dll not found`: the shim loaded correctly, but no client mod DLL was present.
- `LoadLibraryExW(Dreadnought.dll) failed`: the shim loaded correctly, but the companion DLL or one of its dependencies failed to load; use the logged Windows error code.
- `LoadLibraryExW(Dreadnought.dll) succeeded`: the shim loaded the companion client mod.

Only these WER exports are implemented because those are the imports observed in the client binary:

- `WerReportCreate`
- `WerReportSetParameter`
- `WerReportAddFile`
- `WerReportSubmit`
