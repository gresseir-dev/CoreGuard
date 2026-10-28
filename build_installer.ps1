Write-Host "=== [1/3] Сборка исполняемых файлов Go ===" -ForegroundColor Cyan

# 1. Агент (Windows GUI без всплывающей консоли)
go build -ldflags="-H windowsgui -s -w" -o CoreGuard.exe .
if ($LASTEXITCODE -ne 0) { Write-Error "Ошибка сборки CoreGuard.exe"; exit 1 }

# 2. Хаб
go build -ldflags="-s -w" -o coreguard_hub.exe ./cmd/hub
if ($LASTEXITCODE -ne 0) { Write-Error "Ошибка сборки coreguard_hub.exe"; exit 1 }

Write-Host "Бинарники собраны успешно." -ForegroundColor Green

Write-Host "=== [2/3] Поиск компилятора Inno Setup ===" -ForegroundColor Cyan
$isccPaths = @(
    "C:\Program Files (x86)\Inno Setup 6\ISCC.exe",
    "C:\Program Files\Inno Setup 6\ISCC.exe",
    "${env:LOCALAPPDATA}\Programs\Inno Setup 6\ISCC.exe"
)

$iscc = $isccPaths | Where-Object { Test-Path $_ } | Select-Object -First 1

if (-not $iscc) {
    Write-Host "Компилятор ISCC.exe не найден по стандартным путям!" -ForegroundColor Yellow
    Write-Host "Установите Inno Setup через: winget install JRSoftware.InnoSetup" -ForegroundColor Yellow
    exit 1
}

Write-Host "Компилятор найден: $iscc" -ForegroundColor Green
Write-Host "=== [3/3] Компиляция единого инсталлятора ===" -ForegroundColor Cyan

& $iscc "installer.iss"

if ($LASTEXITCODE -eq 0) {
    $outExe = Get-ChildItem "Output\CoreGuard_Setup_*.exe" | Sort-Object LastWriteTime -Descending | Select-Object -First 1
    Write-Host "`nГотово! Инсталлятор успешно собран:" -ForegroundColor Green
    Write-Host $outExe.FullName -ForegroundColor Yellow
}
