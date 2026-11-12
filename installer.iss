; =====================================================================
; CoreGuard Enterprise Installer Script (Inno Setup 6)
; Product: CoreGuard Enterprise v2.1.5
; =====================================================================

#define MyAppName "CoreGuard Enterprise"
#define MyAppVersion "2.2.4"
#define MyAppPublisher "CoreGuard"
#define MyAppExeName "CoreGuard.exe"
#define MyHubExeName "coreguard_hub.exe"
#define MyServiceName "CoreGuardHub"

[Setup]
AppId={{D1A47B2E-8A33-4F71-B892-3C4D11E89A02}
AppName={#MyAppName}
AppVersion={#MyAppVersion}
AppPublisher={#MyAppPublisher}
; Установка строго в 64-битный каталог Program Files
DefaultDirName={autopf64}\{#MyAppName}
DefaultGroupName={#MyAppName}
OutputDir=Output
OutputBaseFilename=CoreGuard_Setup_v{#MyAppVersion}_x64
Compression=lzma2/max
SolidCompression=yes
; Обязательный запрос прав администратора (UAC)
PrivilegesRequired=admin
PrivilegesRequiredOverridesAllowed=dialog
ArchitecturesInstallIn64BitMode=x64
CloseApplications=yes
RestartApplications=no

[Languages]
Name: "russian"; MessagesFile: "compiler:Languages\Russian.isl"

[Tasks]
Name: "desktopicon"; Description: "{cm:CreateDesktopIcon}"; GroupDescription: "{cm:AdditionalIcons}"

[Files]
Source: "PRODUCT_BROCHURE.pdf"; DestDir: "{app}"; Flags: ignoreversion
; Исполняемые файлы
Source: "CoreGuard.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "coreguard_hub.exe"; DestDir: "{app}"; Flags: ignoreversion
; Конфигурационные файлы (не перезаписываются, если уже существуют)
Source: "config.json"; DestDir: "{app}"; Flags: onlyifdoesntexist
Source: "hub_config.json"; DestDir: "{app}"; Flags: onlyifdoesntexist
Source: "nodes.json"; DestDir: "{app}"; Flags: onlyifdoesntexist
; Документация в дистрибутиве
Source: "README.md"; DestDir: "{app}"; Flags: ignoreversion
Source: "README_EN.md"; DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{group}\Описание и презентация (PDF)"; Filename: "{app}\PRODUCT_BROCHURE.pdf"
Name: "{group}\CoreGuard Agent"; Filename: "{app}\{#MyAppExeName}"
Name: "{autodesktop}\CoreGuard Agent"; Filename: "{app}\{#MyAppExeName}"; Tasks: desktopicon
Name: "{group}\Веб-панель CoreGuard Hub"; Filename: "http://localhost:9090"
Name: "{autodesktop}\Веб-панель CoreGuard Hub"; Filename: "http://localhost:9090"; Tasks: desktopicon

[Run]
Filename: "schtasks.exe"; Parameters: "/Create /TN ""CoreGuardAgent"" /TR ""\""{app}\CoreGuard.exe\"""" /SC ONLOGON /RL HIGHEST /F"; Flags: runhidden
; 1. Принудительная зачистка зависших процессов перед регистрацией и стартом
Filename: "{cmd}"; Parameters: "/c taskkill /f /im {#MyAppExeName} /im {#MyHubExeName} 2>nul || exit 0"; Flags: runhidden; StatusMsg: "Проверка и освобождение портов..."

; 2. Регистрация хаба как системной службы Windows
Filename: "{sys}\sc.exe"; Parameters: "create {#MyServiceName} binPath= ""{app}\{#MyHubExeName}"" start= auto DisplayName= ""CoreGuard Central Hub"""; Flags: runhidden; StatusMsg: "Регистрация службы CoreGuardHub..."

; 3. Запуск службы хаба
Filename: "{sys}\sc.exe"; Parameters: "start {#MyServiceName}"; Flags: runhidden; StatusMsg: "Запуск службы CoreGuardHub..."

; 4. Запуск агента с правами администратора
Filename: "{app}\{#MyAppExeName}"; Flags: nowait postinstall; Description: "Запустить CoreGuard Agent"

; 5. Автоматическое открытие веб-панели мониторинга в браузере
Filename: "http://localhost:9090"; Flags: shellexec runmaximized postinstall; Description: "Открыть панель мониторинга Hub (http://localhost:9090)"

[UninstallRun]
Filename: "schtasks.exe"; Parameters: "/Delete /TN ""CoreGuardAgent"" /F"; Flags: runhidden
; 1. Синхронная остановка службы (net stop ожидает фактического завершения процесса)
Filename: "{sys}\net.exe"; Parameters: "stop {#MyServiceName}"; Flags: runhidden

; 2. Принудительное завершение процессов, если они не успели выгрузиться
Filename: "{cmd}"; Parameters: "/c taskkill /f /im {#MyHubExeName} /im {#MyAppExeName} 2>nul || exit 0"; Flags: runhidden

; 3. Удаление службы из системного реестра SCM
Filename: "{sys}\sc.exe"; Parameters: "delete {#MyServiceName}"; Flags: runhidden
[UninstallDelete]
Type: files; Name: "{app}\hub_events.json"
Type: files; Name: "{app}\history.json"
Type: files; Name: "{app}\coreguard.lock"