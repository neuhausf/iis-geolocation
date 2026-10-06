# Lädt die aktuellen DB-IP-Lite-Datenbanken (CC BY 4.0) nach internal\geodata\.
# Aufruf: powershell -ExecutionPolicy Bypass -File tools\fetch-db.ps1 [-City]
param([switch]$City)
$ErrorActionPreference = "Stop"
$dir = Join-Path $PSScriptRoot "..\internal\geodata"
$kinds = @("country", "asn"); if ($City) { $kinds += "city" }
foreach ($kind in $kinds) {
  $ok = $false
  foreach ($d in @((Get-Date).ToUniversalTime(), (Get-Date).ToUniversalTime().AddMonths(-1))) {
    $url = "https://download.db-ip.com/free/dbip-$kind-lite-$($d.ToString('yyyy-MM')).mmdb.gz"
    try {
      Invoke-WebRequest -Uri $url -OutFile (Join-Path $dir "dbip-$kind-lite.mmdb.gz") -UseBasicParsing
      Write-Host "geladen: $url"; $ok = $true; break
    } catch { }
  }
  if (-not $ok) { throw "Download fehlgeschlagen: $kind" }
}
