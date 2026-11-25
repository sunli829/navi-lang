# Installs navi, or upgrades it to the latest release (Windows):
#
#   irm https://raw.githubusercontent.com/sunli829/navi-lang/main/install.ps1 | iex
#   & ([scriptblock]::Create((irm .../install.ps1))) v0.0.5     (a given release)
#
# The release's package (bin\navi.exe and lib\std) goes to $env:NAVI_HOME
# (%USERPROFILE%\.navi by default), replacing what is there; its SHA-256 is
# checked against the release's SHA256SUMS; its bin is added to the user's
# PATH.

param([string]$Version = "")

$ErrorActionPreference = "Stop"
$ProgressPreference = "SilentlyContinue"
$repo = "https://github.com/sunli829/navi-lang"
$navihome = if ($env:NAVI_HOME) { $env:NAVI_HOME } else { Join-Path $env:USERPROFILE ".navi" }

if (-not [Environment]::Is64BitOperatingSystem) { throw "navi install: no release for 32-bit Windows" }
$arch = $env:PROCESSOR_ARCHITECTURE
if ($arch -ne "AMD64" -and $arch -ne "ARM64") { throw "navi install: no release for $arch" }
# (Windows on Arm runs the x64 package.)

if ($Version -eq "") {
    # The latest release: where /releases/latest redirects (.../tag/vX).
    $r = Invoke-WebRequest -Uri "$repo/releases/latest" -Method Head -UseBasicParsing
    $url = if ($r.BaseResponse.ResponseUri) { $r.BaseResponse.ResponseUri.AbsoluteUri } else { $r.BaseResponse.RequestMessage.RequestUri.AbsoluteUri }
    $Version = $url.Split("/")[-1]
    if (-not $Version.StartsWith("v")) { throw "navi install: no release found at $repo" }
}

$pkg = "navi-$Version-windows-x64"
$tmp = Join-Path ([IO.Path]::GetTempPath()) ("navi-install-" + [Guid]::NewGuid())
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
    Write-Host "Downloading $pkg"
    $zip = Join-Path $tmp "$pkg.zip"
    $sums = Join-Path $tmp "SHA256SUMS"
    try {
        Invoke-WebRequest -Uri "$repo/releases/download/$Version/$pkg.zip" -OutFile $zip -UseBasicParsing
        Invoke-WebRequest -Uri "$repo/releases/download/$Version/SHA256SUMS" -OutFile $sums -UseBasicParsing
    } catch {
        throw "navi install: no package $pkg in release $Version"
    }
    $want = ""
    foreach ($line in Get-Content $sums) {
        $f = $line -split "\s+"
        if ($f.Length -ge 2 -and $f[1] -eq "$pkg.zip") { $want = $f[0].ToLower() }
    }
    $got = (Get-FileHash -Algorithm SHA256 $zip).Hash.ToLower()
    if ($want -eq "" -or $want -ne $got) { throw "navi install: checksum mismatch for $pkg.zip" }
    Expand-Archive -Path $zip -DestinationPath $tmp -Force

    # The new bin and lib replace the old ones (an upgrade leaves no old std).
    New-Item -ItemType Directory -Force -Path $navihome | Out-Null
    foreach ($d in @("bin", "lib")) {
        $dst = Join-Path $navihome $d
        if (Test-Path $dst) { Remove-Item -Recurse -Force $dst }
        Move-Item (Join-Path (Join-Path $tmp $pkg) $d) $dst
    }
} finally {
    Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}
Write-Host "navi $Version installed in $navihome"

$bin = Join-Path $navihome "bin"
$path = [Environment]::GetEnvironmentVariable("Path", "User")
if (-not $path) { $path = "" }
if (-not ($path.Split(";") -contains $bin)) {
    [Environment]::SetEnvironmentVariable("Path", ($bin + ";" + $path).TrimEnd(";"), "User")
    Write-Host "Added $bin to your PATH (new terminals see it)"
}
