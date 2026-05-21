# hosts-redirect.ps1 — Add Greybox API domain redirects to Windows hosts file.
# Run this on EACH Windows client that will connect to the private server.
#
# Usage (run as Administrator in PowerShell):
#   .\hosts-redirect.ps1 -ServerIP 192.168.1.100
#
# To remove the entries later:
#   .\hosts-redirect.ps1 -Remove
#
# To install the CA certificate (also run as Administrator):
#   .\hosts-redirect.ps1 -ServerIP 192.168.1.100 -InstallCert C:\path\to\ca.crt

param(
    [string]$ServerIP = "127.0.0.1",
    [switch]$Remove,
    [string]$InstallCert = ""
)

$HostsFile = "$env:SystemRoot\System32\drivers\etc\hosts"
$Marker    = "# Dreadnought Private Server"

$Domains = @(
    "profile-api.prod.greybox.sixfoot.live",
    "legacyapi.prod.greybox.sixfoot.live",
    "mmog.greybox.sixfoot.live",
    "firmament.prod.greybox.sixfoot.live",
    "masterserver.local",
    "gamemanager.local"
)

# --- Require Administrator ---
$currentUser = [Security.Principal.WindowsIdentity]::GetCurrent()
$principal   = [Security.Principal.WindowsPrincipal]$currentUser
if (-not $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)) {
    Write-Error "This script must be run as Administrator. Right-click PowerShell → Run as Administrator."
    exit 1
}

# --- Remove mode ---
if ($Remove) {
    Write-Host "[*] Removing Dreadnought private server redirects from $HostsFile"
    $lines = Get-Content $HostsFile
    # Keep only lines that are NOT our marker and do NOT contain any of our domains
    $newLines = $lines | Where-Object {
        $line = $_
        if ($line -match [regex]::Escape($Marker)) { return $false }
        foreach ($d in $Domains) {
            if ($line -match [regex]::Escape($d)) { return $false }
        }
        return $true
    }
    Set-Content -Path $HostsFile -Value $newLines -Encoding ASCII
    Write-Host "[OK] Redirects removed. All other hosts entries preserved."

    # Flush DNS cache
    ipconfig /flushdns | Out-Null
    Write-Host "[OK] DNS cache flushed."
    exit 0
}

# --- Add mode ---
$existing = Get-Content $HostsFile -Raw
if ($existing -match [regex]::Escape($Marker)) {
    Write-Warning "Redirects already present in $HostsFile"
    Write-Warning "Run with -Remove first if you want to update the server IP."
    exit 0
}

Write-Host "[*] Adding Dreadnought private server redirects to $HostsFile"
Write-Host "    Server IP: $ServerIP"

$newEntries = @("")
$newEntries += $Marker
foreach ($d in $Domains) {
    $newEntries += "$ServerIP`t$d"
}

Add-Content -Path $HostsFile -Value ($newEntries -join "`r`n") -Encoding ASCII

Write-Host "[OK] Hosts file updated."

# Flush DNS cache so changes take effect immediately
ipconfig /flushdns | Out-Null
Write-Host "[OK] DNS cache flushed."

Write-Host ""
Write-Host "Domains now pointing to $ServerIP :"
foreach ($d in $Domains) {
    Write-Host "    $d"
}

# --- Install CA cert (optional) ---
if ($InstallCert -ne "") {
    if (-not (Test-Path $InstallCert)) {
        Write-Error "Certificate file not found: $InstallCert"
        exit 1
    }
    Write-Host ""
    # Resolve to absolute path — .NET constructors don't accept relative paths
    $certAbsPath = (Resolve-Path $InstallCert).Path
    Write-Host "[*] Installing CA certificate: $certAbsPath"

    # Method 1: certutil.exe — most reliable for machine-wide trust, works without .NET quirks
    $certutilResult = & certutil.exe -addstore "Root" $certAbsPath 2>&1
    if ($LASTEXITCODE -eq 0) {
        Write-Host "[OK] CA installed via certutil (machine Root store)."
    } else {
        Write-Warning "certutil output: $certutilResult"

        # Method 2: .NET X509Store fallback
        try {
            $store = New-Object System.Security.Cryptography.X509Certificates.X509Store(
                [System.Security.Cryptography.X509Certificates.StoreName]::Root,
                [System.Security.Cryptography.X509Certificates.StoreLocation]::LocalMachine
            )
            $store.Open([System.Security.Cryptography.X509Certificates.OpenFlags]::ReadWrite)
            $cert = New-Object System.Security.Cryptography.X509Certificates.X509Certificate2($certAbsPath)
            $store.Add($cert)
            $store.Close()
            Write-Host "[OK] CA installed via X509Store (LocalMachine\Root)."
        } catch {
            Write-Error "Both cert install methods failed. Install manually: certmgr.msc > Trusted Root CAs > Import > $certAbsPath"
        }
    }

    Write-Host "[OK] CA certificate installed. Restart your launcher/browser to pick it up."
}

Write-Host ""
Write-Host "============================================================"
Write-Host "  Setup complete! Steps to finish:"
Write-Host "============================================================"
Write-Host "  1. Copy ca.crt from the server to this machine"
Write-Host "  2. Install it: .\hosts-redirect.ps1 -ServerIP $ServerIP -InstallCert .\ca.crt"
Write-Host "     OR manually: certmgr.msc > Trusted Root Certification Authorities > Import"
Write-Host "  3. Launch Dreadnought normally — it will connect to the private server"
Write-Host ""
