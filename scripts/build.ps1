$ErrorActionPreference = "Stop"
Set-StrictMode -Version Latest

$Root = Split-Path -Parent $PSScriptRoot
$Dist = Join-Path $Root "dist"
$Cache = Join-Path ([System.IO.Path]::GetTempPath()) "proxypool-go-build-$PID"
$Main = Join-Path $Root "cmd\proxypool"
$Readme = Join-Path $Root "README.md"
$ConfigExample = Join-Path $Root "configs\config.example.json"

New-Item -ItemType Directory -Force -Path $Dist | Out-Null
New-Item -ItemType Directory -Force -Path $Cache | Out-Null

$OldCGO = $env:CGO_ENABLED
$OldGOOS = $env:GOOS
$OldGOARCH = $env:GOARCH
$OldGOCACHE = $env:GOCACHE

$Targets = @(
  @{ Name = "windows-amd64"; GOOS = "windows"; GOARCH = "amd64"; Bin = "proxypool.exe"; Archive = "proxypool-windows-amd64.zip" },
  @{ Name = "windows-arm64"; GOOS = "windows"; GOARCH = "arm64"; Bin = "proxypool.exe"; Archive = "proxypool-windows-arm64.zip" },
  @{ Name = "linux-amd64"; GOOS = "linux"; GOARCH = "amd64"; Bin = "proxypool"; Archive = "proxypool-linux-amd64.tar.gz" },
  @{ Name = "linux-arm64"; GOOS = "linux"; GOARCH = "arm64"; Bin = "proxypool"; Archive = "proxypool-linux-arm64.tar.gz" },
  @{ Name = "darwin-amd64"; GOOS = "darwin"; GOARCH = "amd64"; Bin = "proxypool"; Archive = "proxypool-darwin-amd64.tar.gz" },
  @{ Name = "darwin-arm64"; GOOS = "darwin"; GOARCH = "arm64"; Bin = "proxypool"; Archive = "proxypool-darwin-arm64.tar.gz" }
)

try {
  foreach ($Target in $Targets) {
    $env:CGO_ENABLED = "0"
    $env:GOOS = $Target.GOOS
    $env:GOARCH = $Target.GOARCH
    $env:GOCACHE = $Cache

    $PackageDir = Join-Path $Dist "proxypool-$($Target.Name)"
    if (Test-Path -LiteralPath $PackageDir) {
      Remove-Item -LiteralPath $PackageDir -Recurse -Force
    }
    New-Item -ItemType Directory -Force -Path $PackageDir | Out-Null
    New-Item -ItemType Directory -Force -Path (Join-Path $PackageDir "configs") | Out-Null

    $Out = Join-Path $PackageDir $Target.Bin
    Write-Host "Building $($Target.Name) -> $Out"
    go build -trimpath -buildvcs=false -ldflags "-s -w" -o $Out $Main
    if ($LASTEXITCODE -ne 0) {
      throw "go build failed for $($Target.Name)"
    }

    Copy-Item -LiteralPath $ConfigExample -Destination (Join-Path $PackageDir "configs\config.example.json")
    Copy-Item -LiteralPath $Readme -Destination (Join-Path $PackageDir "README.md")

    $Archive = Join-Path $Dist $Target.Archive
    if (Test-Path -LiteralPath $Archive) {
      Remove-Item -LiteralPath $Archive -Force
    }
    Write-Host "Packing $($Target.Name) -> $Archive"
    if ($Target.GOOS -eq "windows") {
      Compress-Archive -Path $PackageDir -DestinationPath $Archive -Force
    } else {
      tar -czf $Archive -C $Dist "proxypool-$($Target.Name)"
    }
  }

  $ChecksumPath = Join-Path $Dist "SHA256SUMS.txt"
  $Archives = Get-ChildItem -LiteralPath $Dist -File | Where-Object { $_.Name -like "*.zip" -or $_.Name -like "*.tar.gz" } | Sort-Object Name
  $Lines = foreach ($Archive in $Archives) {
    $Hash = Get-FileHash -Algorithm SHA256 -LiteralPath $Archive.FullName
    "$($Hash.Hash.ToLowerInvariant())  $($Archive.Name)"
  }
  Set-Content -LiteralPath $ChecksumPath -Value $Lines -Encoding ASCII
  Write-Host "Checksums -> $ChecksumPath"
} finally {
  $env:CGO_ENABLED = $OldCGO
  $env:GOOS = $OldGOOS
  $env:GOARCH = $OldGOARCH
  $env:GOCACHE = $OldGOCACHE
  Remove-Item -LiteralPath $Cache -Recurse -Force -ErrorAction SilentlyContinue
}
