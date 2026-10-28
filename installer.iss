; --- CoreGuard Universal Enterprise Installer ---
#define MyAppName "CoreGuard"
#define MyAppVersion "2.1.4"
#define MyAppPublisher "CoreGuard Security"
#define MyAppURL "https://github.com/gresseir-dev/CoreGuard"
#define MyAppExeName "CoreGuard.exe"
#define MyHubExeName "coreguard_hub.exe"

[Setup]
AppId={{C682A4D1-9B58-4C3D-B274-884A81E995EF}
AppName={#MyAppName}
AppVersion={#MyAppVersion}
AppPublisher={#MyAppPublisher}
AppPublisherURL={#MyAppURL}
DefaultDirName=C:\CoreGuard
DefaultGroupName={#MyAppName}
DisableProgramGroupPage=yes
OutputBaseFilename=CoreGuard_Setup_v{#MyAppVersion}
Compression=lzma2/ultra64
SolidCompression=yes
PrivilegesRequired=admin
ArchitecturesInstallIn64BitMode=x64compatible
WizardStyle=modern

[Languages]
Name: "russian"; MessagesFile: "compiler:Languages\Russian.isl"
Name: "english"; MessagesFile: "compiler:Default.isl"

[Types]
Name: "full"; Description: "Полная установка (Агент + Центральный Хаб)"
Name: "agent_only"; Description: "Только Агент мониторинга (для клиентских ПК)"
Name: "custom"; Description: "Выборочная установка"; Flags: iscustom

[Components]
Name: "agent"; Description: "Локальный агент CoreGuard (SMART, датчики, трей)"; Types: full agent_only custom; Flags: fixed
Name: "hub"; Description: "Центральный сервер управления (CoreGuard Central NOC Hub)"; Types: full custom

[Tasks]
Name: "autostart"; Description: "Запускать CoreGuard при старте Windows"; GroupDescription: "Автозапуск:"; Components: agent
Name: "firewall"; Description: "Разрешить входящие подключения в Брандмауэре Windows"; GroupDescription: "Сеть:"

[Dirs]
Name: "{app}"; Permissions: users-full

[Files]
; Основные исполняемые файлы
Source: "CoreGuard.exe"; DestDir: "{app}"; Flags: ignoreversion; Components: agent
Source: "coreguard_hub.exe"; DestDir: "{app}"; Flags: ignoreversion; Components: hub
; Конфигурационные шаблоны и документация
Source: "CoreGuard_Brochure.pdf"; DestDir: "{app}"; Flags: isreadme; Components: agent; DestName: "Brochure.pdf"

[Icons]
Name: "{autoprograms}\{#MyAppName}\CoreGuard Agent"; Filename: "{app}\{#MyAppExeName}"; WorkingDir: "{app}"; Components: agent
Name: "{autoprograms}\{#MyAppName}\CoreGuard Central Hub"; Filename: "{app}\{#MyHubExeName}"; Components: hub
Name: "{autoprograms}\{#MyAppName}\Удалить CoreGuard"; Filename: "{uninstallexe}"

[Registry]
; Автозапуск агента
Root: HKCU; Subkey: "Software\Microsoft\Windows\CurrentVersion\Run"; ValueType: string; ValueName: "CoreGuard"; ValueData: """{app}\{#MyAppExeName}"""; Flags: uninsdeletevalue; Tasks: autostart

[Run]
; Настройка Windows Firewall через netsh при установке
Filename: "netsh"; Parameters: "advfirewall firewall add rule name=""CoreGuard Agent API"" dir=in action=allow protocol=TCP localport=48123"; Flags: runhidden; Tasks: firewall; Components: agent
Filename: "netsh"; Parameters: "advfirewall firewall add rule name=""CoreGuard NOC Hub"" dir=in action=allow protocol=TCP localport=9090"; Flags: runhidden; Tasks: firewall; Components: hub

; Запуск после завершения установки
Filename: "{app}\{#MyAppExeName}"; Description: "Запустить CoreGuard Agent"; Flags: nowait postinstall skipifsilent; Components: agent
Filename: "{app}\{#MyHubExeName}"; Description: "Запустить CoreGuard Central Hub"; Flags: nowait postinstall skipifsilent unchecked; Components: hub

Filename: "http://127.0.0.1:9090"; Description: "Открыть CoreGuard Central в браузере"; Flags: shellexec nowait postinstall runasoriginaluser; Components: hub

[UninstallRun]
; Очистка правил Firewall при удалении программы
Filename: "netsh"; Parameters: "advfirewall firewall delete rule name=""CoreGuard Agent API"""; Flags: runhidden
Filename: "netsh"; Parameters: "advfirewall firewall delete rule name=""CoreGuard NOC Hub"""; Flags: runhidden
Filename: "taskkill"; Parameters: "/F /IM {#MyAppExeName}"; Flags: runhidden
Filename: "taskkill"; Parameters: "/F /IM {#MyHubExeName}"; Flags: runhidden




