#define MyAppName "CoreGuard Enterprise"
#define MyAppVersion "2.1.5"
#define MyAppPublisher "CoreGuard"
#define MyAppExeName "CoreGuard.exe"
#define MyHubExeName "coreguard_hub.exe"
#define MyServiceName "CoreGuardHub"

[Setup]
AppId={{D1A47B2E-8A33-4F71-B892-3C4D11E89A02}
AppName={#MyAppName}
AppVersion={#MyAppVersion}
AppPublisher={#MyAppPublisher}
DefaultDirName={autopf64}\{#MyAppName}
DefaultGroupName={#MyAppName}
OutputDir=Output
OutputBaseFilename=CoreGuard_Setup_v{#MyAppVersion}
Compression=lzma2/max
SolidCompression=yes
PrivilegesRequired=admin
ArchitecturesInstallIn64BitMode=x64
CloseApplications=yes
RestartApplications=no

[Languages]
Name: "russian"; MessagesFile: "compiler:Languages\Russian.isl"

[Tasks]
Name: "desktopicon"; Description: "{cm:CreateDesktopIcon}"; GroupDescription: "{cm:AdditionalIcons}"

[Files]
Source: "CoreGuard.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "coreguard_hub.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "config.json"; DestDir: "{app}"; Flags: onlyifdoesntexist
Source: "hub_config.json"; DestDir: "{app}"; Flags: onlyifdoesntexist
Source: "nodes.json"; DestDir: "{app}"; Flags: onlyifdoesntexist
Source: "README.md"; DestDir: "{app}"; Flags: ignoreversion
Source: "README_EN.md"; DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{group}\CoreGuard Agent"; Filename: "{app}\{#MyAppExeName}"
Name: "{autodesktop}\CoreGuard Agent"; Filename: "{app}\{#MyAppExeName}"; Tasks: desktopicon
Name: "{group}\Веб-панель CoreGuard Hub"; Filename: "http://localhost:9090"
Name: "{autodesktop}\Веб-панель CoreGuard Hub"; Filename: "http://localhost:9090"; Tasks: desktopicon

[Run]
; Принудительно гасим старые зависшие процессы перед стартом
Filename: "{cmd}"; Parameters: "/c taskkill /f /im {#MyAppExeName} 2>nul || exit 0"; Flags: runhidden; StatusMsg: "Очистка процессов агента..."

; Регистрация и запуск системной службы Hub
Filename: "{sys}\sc.exe"; Parameters: "create {#MyServiceName} binPath= ""{app}\{#MyHubExeName}"" start= auto DisplayName= ""CoreGuard Central Hub"""; Flags: runhidden; StatusMsg: "Настройка службы CoreGuardHub..."
Filename: "{sys}\sc.exe"; Parameters: "start {#MyServiceName}"; Flags: runhidden; StatusMsg: "Запуск службы CoreGuardHub..."

; Автоматический запуск агента с повышенными привилегиями администратора
Filename: "{app}\{#MyAppExeName}"; Flags: nowait postinstall; Description: "Запустить CoreGuard Agent от имени администратора"

; Открытие веб-панели мониторинга в браузере
Filename: "http://localhost:9090"; Flags: shellexec runmaximized postinstall; Description: "Открыть веб-панель CoreGuard (http://localhost:9090)"

[UninstallRun]
Set-Location "C:\coreguard"

$issContent = Get-Content "installer.iss" -Raw -Encoding UTF8

$newUninstallRun = @'
[UninstallRun]
Set-Location "C:\coreguard"

$issContent = Get-Content "installer.iss" -Raw -Encoding UTF8

$newUninstallRun = @'
[UninstallRun]
; 1. Синхронная остановка службы (net stop ожидает реального перехода в STOPPED)
Filename: "{sys}\net.exe"; Parameters: "stop {#MyServiceName}"; Flags: runhidden

; 2. Принудительное завершение процессов, если они заблокировали файлы
Filename: "{cmd}"; Parameters: "/c taskkill /f /im {#MyHubExeName} /im {#MyAppExeName} 2>nul || exit 0"; Flags: runhidden

; 3. Окончательное удаление службы из реестра SCM
Filename: "{sys}\sc.exe"; Parameters: "delete {#MyServiceName}"; Flags: runhidden
'@

# Заменяем старую секцию [UninstallRun] новой
if ($issContent -match '\[UninstallRun\][\s\S]*') {
    $issContent = $issContent -replace '\[UninstallRun\][\s\S]*', $newUninstallRun
} else {
    $issContent = $issContent + "`r`n`r`n" + $newUninstallRun
}

Set-Content -Path "installer.iss" -Value $issContent -Encoding UTF8