$ErrorActionPreference = "Stop"
Set-Location "C:\CoreGuard"
$setup = Get-ChildItem -Path "C:\CoreGuard" -Filter "*Setup*.exe" -Recurse | Sort-Object LastWriteTime -Descending | Select-Object -First 1
if (-not $setup) { throw "Установщик не найден! Сначала выполните .\build.ps1" }
$ver = Read-Host "Введите тег версии для GitHub Release (например, v2.3.0)"
gh release create $ver $setup.FullName --title "CoreGuard Enterprise $ver (GPLv3)" --notes "Native Windows IOCTL SMART (NVMe/SATA), silent tray agent, Central Hub, GNU GPLv3."