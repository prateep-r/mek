# Install mek on Windows from GitHub Releases, verifying the SHA-256 checksum.
#
#   irm https://raw.githubusercontent.com/prateep-r/mek/main/install.ps1 | iex
#
# Options (environment variables, as for install.sh):
#   $env:MEK_VERSION = 'v0.16.0'         install a specific version (default: latest)
#   $env:MEK_INSTALL_DIR = 'C:\tools'    install location (default: %LOCALAPPDATA%\Programs\mek, no admin)
#   $env:MEK_DOWNLOAD_URL = 'https://...'  download from a mirror instead of GitHub Releases
#   $env:MEK_NO_MODIFY_PATH = '1'        don't add the install location to your PATH
$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue' # Invoke-WebRequest is slow with a progress bar

$repo = 'prateep-r/mek'
$dir = if ($env:MEK_INSTALL_DIR) { $env:MEK_INSTALL_DIR } else { Join-Path $env:LOCALAPPDATA 'Programs\mek' }
if ($env:MEK_DOWNLOAD_URL) {
    $base = $env:MEK_DOWNLOAD_URL.TrimEnd('/')
} elseif ($env:MEK_VERSION) {
    $base = "https://github.com/$repo/releases/download/$env:MEK_VERSION"
} else {
    $base = "https://github.com/$repo/releases/latest/download"
}

$arch = switch ($env:PROCESSOR_ARCHITECTURE) {
    'AMD64' { 'amd64' }
    'ARM64' { 'arm64' }
    default { throw "mek: unsupported architecture: $env:PROCESSOR_ARCHITECTURE" }
}
$file = "mek_windows_$arch.zip"

$tmp = Join-Path ([IO.Path]::GetTempPath()) ("mek-" + [guid]::NewGuid())
New-Item -ItemType Directory -Path $tmp | Out-Null
try {
    Write-Host "downloading $file ..."
    Invoke-WebRequest -UseBasicParsing "$base/$file" -OutFile (Join-Path $tmp $file)
    Invoke-WebRequest -UseBasicParsing "$base/checksums.txt" -OutFile (Join-Path $tmp 'checksums.txt')

    $line = Get-Content (Join-Path $tmp 'checksums.txt') | Where-Object { ($_ -split '\s+')[1] -eq $file }
    if (-not $line) { throw "mek: $file is not listed in checksums.txt" }
    $want = ($line -split '\s+')[0].ToLower()
    $got = (Get-FileHash (Join-Path $tmp $file) -Algorithm SHA256).Hash.ToLower()
    if ($got -ne $want) { throw "mek: checksum mismatch for $file - not installing" }

    Expand-Archive -Path (Join-Path $tmp $file) -DestinationPath (Join-Path $tmp 'x') -Force
    New-Item -ItemType Directory -Force -Path $dir | Out-Null
    $exe = Join-Path $dir 'mek.exe'
    if (Test-Path $exe) { Move-Item -Force $exe "$exe.old" } # a running mek can be renamed, not overwritten
    Copy-Item (Join-Path $tmp 'x\mek.exe') $exe
    Remove-Item -Force "$exe.old" -ErrorAction SilentlyContinue
    Write-Host "installed $exe"

    $userPath = [Environment]::GetEnvironmentVariable('Path', 'User')
    if ($env:MEK_NO_MODIFY_PATH -ne '1' -and (($userPath -split ';') -notcontains $dir)) {
        $parts = @($userPath -split ';' | Where-Object { $_ }) + $dir
        [Environment]::SetEnvironmentVariable('Path', ($parts -join ';'), 'User')
        Write-Host "added $dir to your PATH - open a new terminal to use mek"
    }
    & $exe version
} finally {
    Remove-Item -Recurse -Force $tmp -ErrorAction SilentlyContinue
}
