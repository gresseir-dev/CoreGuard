param(
    [switch]$SkipGoBuild,
    [string]$SetVersion = ""
)

$ErrorActionPreference = "Stop"
# Временный бэкап рабочих дев-конфигов на время сборки публичного инсталлятора
$cleanBackupDir = Join-Path $env:TEMP "coreguard_clean_config_backup"
New-Item -ItemType Directory -Path $cleanBackupDir -Force | Out-Null
foreach ($cf in @("config.json", "hub_config.json", "nodes.json")) {
    if (Test-Path $cf) { Copy-Item $cf (Join-Path $cleanBackupDir $cf) -Force }
}
try {
    '{"mode":"FREE","poll_interval_min":5,"remote_access_enabled":true,"remote_port":48123,"auth_user":"admin","cpu_threshold":70,"mb_threshold":50,"space_threshold_pct":10,"language":"RU","popup_alert_enabled":true,"popup_repeat_min":60}' | Set-Content "config.json" -Encoding UTF8
    '{"language":"ru","nodes":[],"telegram":{"enabled":false,"notify_on_crit":true,"notify_on_offline":true}}' | Set-Content "hub_config.json" -Encoding UTF8
    '[]' | Set-Content "nodes.json" -Encoding UTF8
Set-Location "C:\CoreGuard"

# 0. Останавливаем работающие экземпляры и службу, чтобы освободить .exe и .lock
Write-Host "[0/3] Stopping active CoreGuard processes and service..." -ForegroundColor DarkGray
Stop-Service -Name "CoreGuardHub" -ErrorAction SilentlyContinue
Stop-Process -Name "CoreGuard", "coreguard_hub" -Force -ErrorAction SilentlyContinue
Start-Sleep -Milliseconds 800

$issPath = "C:\CoreGuard\installer.iss"
if (-not (Test-Path $issPath)) { throw "File installer.iss not found!" }

$iss = (Get-Content $issPath -Raw -Encoding UTF8) -replace "`r`n", "`n"

# 1. Вычисляем новую версию (инкремент патча X.Y.Z -> X.Y.(Z+1))
if ($SetVersion -ne "") {
    $newVersion = $SetVersion
} elseif ($iss -match '#define\s+MyAppVersion\s+"(\d+)\.(\d+)\.(\d+)"') {
    $major = [int]$matches[1]
    $minor = [int]$matches[2]
    $patch = [int]$matches[3] + 1
    $newVersion = "$major.$minor.$patch"
} else {
    $newVersion = "2.2.1"
}

Write-Host "=== Building version: v$newVersion (x64) ===" -ForegroundColor Cyan

# Обновляем версию и шаблон имени файла с суффиксом _x64 в installer.iss
$iss = $iss -replace '#define\s+MyAppVersion\s+"[^"]+"', "#define MyAppVersion `"$newVersion`""
$iss = $iss -replace 'OutputBaseFilename=CoreGuard_Setup_v\{#MyAppVersion\}(_x64)?', 'OutputBaseFilename=CoreGuard_Setup_v{#MyAppVersion}_x64'

[System.IO.File]::WriteAllText($issPath, $iss, (New-Object System.Text.UTF8Encoding $false))
if (Test-Path "C:\CoreGuard\main.go") {
    $mCode = [System.IO.File]::ReadAllText("C:\CoreGuard\main.go", [System.Text.Encoding]::UTF8)
    $mCode = $mCode -replace 'const AgentVersion = "[^"]+"', "const AgentVersion = `"$newVersion`""
    [System.IO.File]::WriteAllText("C:\CoreGuard\main.go", $mCode, (New-Object System.Text.UTF8Encoding $false))
}

# 2. Зачистка тестовых логов и блокировок перед сборкой
$cleanTargets = @(
    "C:\CoreGuard\hub_events.json",
    "C:\CoreGuard\cmd\hub\hub_events.json",
    "C:\CoreGuard\history.json",
    "C:\CoreGuard\coreguard.lock"
)
foreach ($item in $cleanTargets) {
    if (Test-Path $item) {
        Remove-Item $item -Force -ErrorAction SilentlyContinue
    }
}

# 3. Компиляция Go-бинарников
if (-not $SkipGoBuild) {
    Write-Host "[1/3] Compiling CoreGuard.exe (x64)..." -ForegroundColor Yellow
    $env:GOARCH = "amd64"
    $env:GOOS = "windows"
    go build -ldflags="-s -w -H windowsgui" -o "C:\CoreGuard\CoreGuard.exe" .
if (Test-Path "C:\CoreGuardBackup\main.go") {
    Push-Location "C:\CoreGuardBackup"
# [EXCLUDED] coreguard_backup.exe
    Pop-Location
}
    if ($LASTEXITCODE -ne 0) { throw "Agent build failed!" }

    $relDir = "C:\CoreGuard\releases"
    New-Item -ItemType Directory -Path $relDir -Force | Out-Null
    Copy-Item "C:\CoreGuard\CoreGuard.exe" (Join-Path $relDir "CoreGuard.exe") -Force
    $sha = (Get-FileHash (Join-Path $relDir "CoreGuard.exe") -Algorithm SHA256).Hash.ToLower()
    $manifestObj = [ordered]@{
        version  = $newVersion
        sha256   = $sha
        built_at = (Get-Date -Format "yyyy-MM-dd HH:mm:ss")
    }
    $manifest = $manifestObj | ConvertTo-Json -Compress
    [System.IO.File]::WriteAllText((Join-Path $relDir "manifest.json"), $manifest, (New-Object System.Text.UTF8Encoding $false))
    Write-Host "[OK] Updated releases/CoreGuard.exe (v$newVersion, SHA256: $sha)" -ForegroundColor Green

    Write-Host "[2/3] Compiling coreguard_hub.exe (x64)..." -ForegroundColor Yellow
    Push-Location "C:\CoreGuard\cmd\hub"
$issContentForHub = [System.IO.File]::ReadAllText("C:\CoreGuard\installer.iss", [System.Text.Encoding]::UTF8)
if ($issContentForHub -match '#define MyAppVersion "([^"]+)"') {
    $hubSyncVer = $Matches[1]
    $hmPath = "C:\CoreGuard\cmd\hub\main.go"
    $hmText = [System.IO.File]::ReadAllText($hmPath, [System.Text.Encoding]::UTF8)
    $hmText = [regex]::Replace($hmText, 'const HubVersion = "[^"]+"', "const HubVersion = `"$hubSyncVer`"")
    [System.IO.File]::WriteAllText($hmPath, $hmText, (New-Object System.Text.UTF8Encoding $false))
}
    go build -o "C:\CoreGuard\coreguard_hub.exe" .
    $hubExit = $LASTEXITCODE
    Pop-Location
    if ($hubExit -ne 0) { throw "Hub build failed!" }
}

# 4. Компиляция инсталлятора Inno Setup
Write-Host "[3/3] Building installer CoreGuard_Setup_v${newVersion}_x64.exe..." -ForegroundColor Yellow
$iscc = @(
    "C:\Program Files (x86)\Inno Setup 6\ISCC.exe",
    "C:\Program Files\Inno Setup 6\ISCC.exe",
    "$env:LOCALAPPDATA\Programs\Inno Setup 6\ISCC.exe"
) | Where-Object { Test-Path $_ } | Select-Object -First 1

if (-not $iscc) { throw "ISCC.exe not found!" }

& $iscc "installer.iss"
if ($LASTEXITCODE -ne 0) { throw "Inno Setup compile failed!" }

$setupPath = "C:\CoreGuard\Output\CoreGuard_Setup_v${newVersion}_x64.exe"

# 5. Формирование релизного ZIP-архива с указанием архитектуры x64
$distDir = "C:\coreguard_release_v${newVersion}_x64"
if (Test-Path $distDir) { Remove-Item $distDir -Recurse -Force }
New-Item -ItemType Directory -Path $distDir | Out-Null

$releaseFiles = @(
    "PRODUCT_BROCHURE.pdf",
    "CoreGuard.exe",
    "coreguard_hub.exe",
    "config.json",
    "nodes.json",
    "README.md",
    "README_EN.md",
    $setupPath
)
foreach ($f in $releaseFiles) {
    if (Test-Path $f) { Copy-Item $f -Destination $distDir }
}

if (Test-Path "C:\CoreGuard\releases") {
    Copy-Item "C:\CoreGuard\releases" -Destination (Join-Path $distDir "releases") -Recurse -Force
}

$zipPath = "C:\CoreGuard\CoreGuard_Enterprise_v${newVersion}_x64_Release.zip"
if (Test-Path $zipPath) { Remove-Item $zipPath -Force }
Compress-Archive -Path "$distDir\*" -DestinationPath $zipPath
Remove-Item $distDir -Recurse -Force

Write-Host "`n[DONE] Build v$newVersion (x64) completed:" -ForegroundColor Green
Get-Item $setupPath, $zipPath | Select-Object Name, Length, LastWriteTime
} finally {
    foreach ($cf in @("config.json", "hub_config.json", "nodes.json")) {
        $bak = Join-Path $cleanBackupDir $cf
        if (Test-Path $bak) { Copy-Item $bak $cf -Force }
    }
    Remove-Item $cleanBackupDir -Recurse -Force -ErrorAction SilentlyContinue
}