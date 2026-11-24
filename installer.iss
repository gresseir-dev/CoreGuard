; =====================================================================
; CoreGuard Enterprise Installer Script (Inno Setup 6)
; Product: CoreGuard Enterprise v2.1.5
; =====================================================================

#define MyAppName "CoreGuard Enterprise"
#define MyAppVersion "2.3.26"
#define MyAppPublisher "CoreGuard"
#define MyAppExeName "CoreGuard.exe"; Components: agent
#define MyHubExeName "coreguard_hub.exe"; Components: hub
#define MyServiceName "CoreGuardHub"; Components: hub

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
ArchitecturesInstallIn64BitMode=x64compatible
CloseApplications=yes
RestartApplications=no
SetupIconFile=coreguard.ico
UninstallDisplayIcon={app}\CoreGuard.exe

[Languages]
Name: "russian"; MessagesFile: "compiler:Languages\Russian.isl"

[Tasks]
Name: "desktopicon"; Description: "{cm:CreateDesktopIcon}"; GroupDescription: "{cm:AdditionalIcons}"

[Types]
Name: "agentonly"; Description: "Клиентский ПК (Только агент мониторинга CoreGuard)"
Name: "full"; Description: "Главный сервер администратора (Агент + Центральный Хаб)"
Name: "custom"; Description: "Выборочная установка"; Flags: iscustom

[Components]
Name: "agent"; Description: "CoreGuard Agent (Мониторинг железа, Native SMART и локальный дашборд)"; Types: agentonly full custom; Flags: fixed
Name: "hub"; Description: "CoreGuard Central Hub (Веб-сервер централизованного мониторинга всех ПК)"; Types: full

[Files]
Source: "PRODUCT_BROCHURE.pdf"; DestDir: "{app}"; Flags: ignoreversion
; Исполняемые файлы
Source: "CoreGuard.exe"; DestDir: "{app}"; Flags: ignoreversion; Components: agent
Source: "coreguard_hub.exe"; DestDir: "{app}"; Flags: ignoreversion; Components: hub
; Конфигурационные файлы (не перезаписываются, если уже существуют)
Source: "config.json"; DestDir: "{app}"; Flags: onlyifdoesntexist
Source: "releases\*"; DestDir: "{app}\releases"; Flags: ignoreversion recursesubdirs createallsubdirs; Components: hub
; Документация в дистрибутиве
Source: "README.md"; DestDir: "{app}"; Flags: ignoreversion
Source: "C:\CoreGuard\LICENSE"; DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{group}\Описание и презентация (PDF)"; Filename: "{app}\PRODUCT_BROCHURE.pdf"
Name: "{group}\CoreGuard Agent"; Filename: "{app}\{#MyAppExeName}"
Name: "{autodesktop}\CoreGuard Agent"; Filename: "{app}\{#MyAppExeName}"; Tasks: desktopicon
Name: "{group}\Веб-панель CoreGuard Hub"; Filename: "http://localhost:9090"; Components: hub
Name: "{autodesktop}\Веб-панель CoreGuard Hub"; Filename: "http://localhost:9090"; Tasks: desktopicon; Components: hub

[Run]
Filename: "schtasks.exe"; Parameters: "/Create /TN ""CoreGuardAgent"" /TR ""\""{app}\CoreGuard.exe\"""" /SC ONLOGON /RL HIGHEST /F"; Flags: runhidden; Components: agent
Filename: "{sys}\netsh.exe"; Parameters: "advfirewall firewall add rule name=""CoreGuard Agent TCP 48123"" dir=in action=allow protocol=TCP localport=48123 profile=any"; Flags: runhidden; Components: agent
; 1. Принудительная зачистка зависших процессов перед регистрацией и стартом
Filename: "{cmd}"; Parameters: "/c taskkill /f /im {#MyAppExeName} /im {#MyHubExeName} 2>nul || exit 0"; Flags: runhidden; StatusMsg: "Проверка и освобождение портов..."

; 2. Регистрация хаба как системной службы Windows
Filename: "{sys}\sc.exe"; Parameters: "create {#MyServiceName} binPath= ""{app}\{#MyHubExeName}"" start= auto DisplayName= ""CoreGuard Central Hub"""; Flags: runhidden; StatusMsg: "Регистрация службы CoreGuardHub..."; Components: hub

; 3. Запуск службы хаба
Filename: "{sys}\sc.exe"; Parameters: "start {#MyServiceName}"; Flags: runhidden; StatusMsg: "Запуск службы CoreGuardHub..."; Components: hub

; 4. Запуск агента с правами администратора
Filename: "{app}\{#MyAppExeName}"; Flags: nowait postinstall; Description: "Запустить CoreGuard Agent"

; 5. Автоматическое открытие веб-панели мониторинга в браузере
Filename: "http://localhost:9090"; Flags: shellexec runmaximized postinstall; Description: "Открыть панель мониторинга Hub (http://localhost:9090)"; Components: hub

[UninstallRun]
Filename: "schtasks.exe"; Parameters: "/Delete /TN ""CoreGuardAgent"" /F"; Flags: runhidden; Components: agent; RunOnceId: "cg_uninst_1"
Filename: "{sys}\netsh.exe"; Parameters: "advfirewall firewall delete rule name=""CoreGuard Agent TCP 48123"""; Flags: runhidden; Components: agent; RunOnceId: "cg_uninst_fw"
; 1. Синхронная остановка службы (net stop ожидает фактического завершения процесса)
Filename: "{sys}\net.exe"; Parameters: "stop {#MyServiceName}"; Flags: runhidden; RunOnceId: "cg_uninst_2"

; 2. Принудительное завершение процессов, если они не успели выгрузиться
Filename: "{cmd}"; Parameters: "/c taskkill /f /im {#MyHubExeName} /im {#MyAppExeName} 2>nul || exit 0"; Flags: runhidden; RunOnceId: "cg_uninst_3"

; 3. Удаление службы из системного реестра SCM
Filename: "{sys}\sc.exe"; Parameters: "delete {#MyServiceName}"; Flags: runhidden; RunOnceId: "cg_uninst_4"
[UninstallDelete]
Type: files; Name: "{app}\hub_events.json"
Type: files; Name: "{app}\history.json"
Type: files; Name: "{app}\coreguard.lock"
[Code]
procedure CurStepChanged(CurStep: TSetupStep);
begin
  if CurStep = ssPostInstall then
  begin
    { Резерв под пост-установочные действия }
  end;
end;

function AskKeepSettingsOnUninstall(): Boolean;
var
  Form: TSetupForm;
  InfoLabel: TNewStaticText;
  KeepCheck: TNewCheckBox;
  OkBtn, CancelBtn: TNewButton;
begin
  Result := True;
  if UninstallSilent then
    Exit;

  Form := CreateCustomForm(ScaleX(430), ScaleY(175), False, False);
  try
    Form.ClientWidth := ScaleX(430);
    Form.ClientHeight := ScaleY(175);
    Form.Caption := 'Удаление CoreGuard Enterprise';
    Form.BorderStyle := bsDialog;
    Form.Position := poScreenCenter;

    InfoLabel := TNewStaticText.Create(Form);
    InfoLabel.Parent := Form;
    InfoLabel.Left := ScaleX(20);
    InfoLabel.Top := ScaleY(16);
    InfoLabel.Width := ScaleX(390);
    InfoLabel.Height := ScaleY(46);
    InfoLabel.AutoSize := False;
    InfoLabel.WordWrap := True;
    InfoLabel.Caption := 'Выберите, нужно ли сохранить конфигурацию перед удалением программы (удобно при обновлении или тестировании):';

    KeepCheck := TNewCheckBox.Create(Form);
    KeepCheck.Parent := Form;
    KeepCheck.Left := ScaleX(20);
    KeepCheck.Top := ScaleY(72);
    KeepCheck.Width := ScaleX(390);
    KeepCheck.Height := ScaleY(36);
    KeepCheck.Caption := 'Сохранить реквизиты организации, контакты IT и настройки Хаба';
    KeepCheck.Checked := True;

    OkBtn := TNewButton.Create(Form);
    OkBtn.Parent := Form;
    OkBtn.Left := ScaleX(234);
    OkBtn.Top := ScaleY(128);
    OkBtn.Width := ScaleX(85);
    OkBtn.Height := ScaleY(26);
    OkBtn.Caption := 'Продолжить';
    OkBtn.ModalResult := mrOk;
    OkBtn.Default := True;

    CancelBtn := TNewButton.Create(Form);
    CancelBtn.Parent := Form;
    CancelBtn.Left := ScaleX(326);
    CancelBtn.Top := ScaleY(128);
    CancelBtn.Width := ScaleX(85);
    CancelBtn.Height := ScaleY(26);
    CancelBtn.Caption := 'Отмена';
    CancelBtn.ModalResult := mrCancel;
    CancelBtn.Cancel := True;

    if Form.ShowModal = mrOk then
      Result := KeepCheck.Checked
    else
      Abort;
  finally
    Form.Free;
  end;
end;

procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
var
  AppDir: String;
  KeepCfg: Boolean;
begin
  if CurUninstallStep = usUninstall then
  begin
    KeepCfg := AskKeepSettingsOnUninstall();
    AppDir := ExpandConstant('{app}');
    if not KeepCfg then
    begin
      DeleteFile(AddBackslash(AppDir) + 'config.json');
      DeleteFile(AddBackslash(AppDir) + 'hub_config.json');
      DeleteFile(AddBackslash(AppDir) + 'nodes.json');
    end;
  end
  else if CurUninstallStep = usPostUninstall then
  begin
    AppDir := ExpandConstant('{app}');
    RemoveDir(AppDir);
  end;
end;