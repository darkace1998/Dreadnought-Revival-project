# Host a map headlessly and report whether the process survived.
#
# One run is one data point and this game is non-deterministic -- see SKILL.md.
# Run this at least 6 times per configuration before believing anything.
#
#   .\bisect.ps1 -Inert 1
#   .\bisect.ps1 -RvaMax 40
#   1..6 | ForEach-Object { .\bisect.ps1 -RvaMax 40 -Port (7790 + $_) }
param(
  [string]$HooksMax = "",
  [string]$HooksOff = "",
  [string]$RvaMax = "",
  [string]$RvaOff = "",
  [string]$ForceTraveled = "",
  [string]$ServerFlags = "",
  [string]$NoServerCallbacks = "",
  [string]$Inert = "",
  [string]$InitMax = "",
  [string]$NoVeh = "",
  [string]$NoSetTimer = "",
  [string]$NoPatches = "",
  [string]$NoGcPatch = "",
  [int]$Port = 7790,
  [int]$TimeoutSec = 90,
  [string]$Map = "/Game/Maps/MP/Amirani/MP_Amirani_P"
)

# Override with DREADGAME_EXE.
$exe = $env:DREADGAME_EXE
if (-not $exe) {
  $exe = "D:\Dreadnought\DreadnoughtFullGame\Dreadnought\DreadGame\DreadGame\Binaries\Win64\DreadGame-Win64-Shipping.exe"
}
if (-not (Test-Path $exe)) {
  Write-Error "executable not found: $exe (set DREADGAME_EXE)"
  exit 1
}
$bin = Split-Path -Parent $exe
$log = "$bin\dread_mod_log.txt"

# Clear the environment for this process so an unset switch really means unset,
# rather than inheriting whatever the last run left behind.
foreach ($v in 'DN_HOOKS_MAX','DN_HOOKS_OFF','DN_RVA_MAX','DN_RVA_OFF',
                'DN_NO_GCPATCH','DN_NO_PATCHES','DN_NO_SETTIMER','DN_NO_VEH',
                'DN_INIT_MAX','DN_INERT','DN_NO_SERVER_CALLBACKS',
                'DN_SERVERFLAGS','DN_FORCE_TRAVELED') {
  Remove-Item "env:$v" -ErrorAction SilentlyContinue
}
if ($HooksMax -ne "")          { $env:DN_HOOKS_MAX = $HooksMax }
if ($HooksOff -ne "")          { $env:DN_HOOKS_OFF = $HooksOff }
if ($RvaMax -ne "")            { $env:DN_RVA_MAX = $RvaMax }
if ($RvaOff -ne "")            { $env:DN_RVA_OFF = $RvaOff }
if ($NoGcPatch -ne "")         { $env:DN_NO_GCPATCH = $NoGcPatch }
if ($NoPatches -ne "")         { $env:DN_NO_PATCHES = $NoPatches }
if ($NoSetTimer -ne "")        { $env:DN_NO_SETTIMER = $NoSetTimer }
if ($NoVeh -ne "")             { $env:DN_NO_VEH = $NoVeh }
if ($InitMax -ne "")           { $env:DN_INIT_MAX = $InitMax }
if ($Inert -ne "")             { $env:DN_INERT = $Inert }
if ($NoServerCallbacks -ne "") { $env:DN_NO_SERVER_CALLBACKS = $NoServerCallbacks }
if ($ServerFlags -ne "")       { $env:DN_SERVERFLAGS = $ServerFlags }
if ($ForceTraveled -ne "")     { $env:DN_FORCE_TRAVELED = $ForceTraveled }

$label = "rvaMax=$(if($RvaMax -eq ''){'(all)'}else{$RvaMax}) rvaOff=$(if($RvaOff -eq ''){'(none)'}else{$RvaOff}) inert=$(if($Inert -eq ''){'0'}else{$Inert})"
Write-Output "=== RUN: $label  port=$Port ==="

if (Test-Path $log) { Remove-Item $log -Force -ErrorAction SilentlyContinue }

$argList = @(
  "$Map`?listen",
  "-server", "-port=$Port", "-maxplayers=10",
  "-nullrhi", "-unattended", "-nosplash", "-nop4", "-nosound", "-noeac", "-NoSteam", "-log"
)

$proc = Start-Process -FilePath $exe -ArgumentList $argList -WorkingDirectory $bin -PassThru
$start = Get-Date
$verdict = "TIMEOUT"

# Bound UDP on the listen port is the success signal: it means the map finished
# loading and the server is accepting players. Usually about 7 seconds.
while (((Get-Date) - $start).TotalSeconds -lt $TimeoutSec) {
  $alive = Get-Process -Id $proc.Id -ErrorAction SilentlyContinue
  if (-not $alive) { $verdict = "CRASHED"; break }
  $ep = Get-NetUDPEndpoint -OwningProcess $proc.Id -ErrorAction SilentlyContinue |
        Where-Object { $_.LocalPort -eq $Port }
  if ($ep) { $verdict = "SURVIVED"; break }
  Start-Sleep -Milliseconds 750
}

$elapsed = [math]::Round(((Get-Date) - $start).TotalSeconds, 1)
Get-Process -Id $proc.Id -ErrorAction SilentlyContinue | Stop-Process -Force -ErrorAction SilentlyContinue

Write-Output "VERDICT: $verdict after ${elapsed}s"

if (Test-Path $log) {
  $created = (Select-String -Path $log -Pattern '^\[BISECT\] rva #\d+ create:' -ErrorAction SilentlyContinue).Count
  $rvaSkip = (Select-String -Path $log -Pattern '^\[BISECT\] rva #\d+ SKIP'    -ErrorAction SilentlyContinue).Count
  Write-Output "rva hooks: $created created, $rvaSkip skipped"
  $fatal = Select-String -Path $log -Pattern 'Fatal error|failed to route' -ErrorAction SilentlyContinue | Select-Object -First 2
  if ($fatal) { $fatal | ForEach-Object { "  " + $_.Line.Trim() } }
}
