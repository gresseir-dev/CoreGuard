# CoreGuard Enterprise

**CoreGuard Enterprise** — система мониторинга состояния накопителей (**S.M.A.R.T.**), температур (**ЦП**, **системная плата**, **накопители**), свободного места на системном разделе и централизованного управления парком ПК и серверов на базе **Windows** (Windows Server и рабочие станции).

**CoreGuard Enterprise** is a centralized hardware health, **S.M.A.R.T.** storage diagnostics, thermal monitoring (**CPU**, **motherboard**, **drives**), and fleet management platform for **Windows** workstations and servers.

Актуальная версия / Current Release: **v2.5.5** • Лицензия / License: **GNU GPLv3** • Репозиторий / Repository: [github.com/gresseir-dev/CoreGuard](https://github.com/gresseir-dev/CoreGuard)

---

## Русский — Архитектура и возможности

### Системные службы Windows и клиент трея
1. **CoreGuardHub ('coreguard_hub.exe')** — центральный сервер управления и оркестрации (веб-панель по умолчанию на порту '9090'). Опрашивает агентов по HTTP с Basic Auth (порт '48123'), агрегирует статусы ('HEALTHY', 'WARNING', 'CRITICAL'), управляет журналом инцидентов и кеширует релизы с GitHub.
2. **CoreGuardAgent ('CoreGuard.exe')** — фоновая системная служба мониторинга Windows (работает в Сессии 0 с момента загрузки ОС, ещё до входа пользователя в систему). При OTA-обновлении перезапускается средствами Windows SCM Recovery ('sc failure' + 'os.Exit(1)').
3. **Клиент системного трея ('CoreGuard.exe --tray')** — лёгкий интерфейс в сессии пользователя (автозапуск через 'HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Run'). Синхронизируется со службой по 'http://127.0.0.1:<порт>/api/data', отображает состояние дисков во всплывающей подсказке, открывает локальный дашборд в один клик и автоматически восстанавливается после обновления службы.

### Основные возможности
- **Единое OTA-обновление через GitHub Releases:** проверка выпусков в меню **📦 Релиз**, загрузка пакетов с проверкой **SHA-256**, обновление самого Хаба и массовая или точечная раскатка обновлений на агентов.
- **Интерфейс и оповещения:** карточки узлов с отображением версии и статуса сбоев, режим списка/сетки, интеграция с **Telegram** и настройка брендинга MSP / обслуживаемой организации.
- **Чистый дистрибутив Windows:** инсталлятор 'CoreGuard_Setup_v2.5.5_x64.exe' устанавливает только основные компоненты (**Агент** и **Центральный Хаб**).

### Опциональные утилиты ('utils/')
Дополнительные инструменты вынесены из основного дистрибутива в каталог 'utils/' (и прикладываются отдельно к релизам GitHub):
- **'utils/coreguard_scanner.exe'** — консольный сетевой сканер подсети для быстрого обнаружения рабочих станций, серверов и развёрнутых агентов CoreGuard по порту '48123'.
- **'utils/coreguard_msp_setup.exe'** — утилита подготовки и генерации профилей MSP / корпоративного брендинга ('msp_profile.json') для массового развёртывания инсталлятора на парке клиентов.

---

## English — Architecture & Features

### Windows Services & System Tray Client
1. **CoreGuardHub ('coreguard_hub.exe')** — central management server and web dashboard (default port '9090'). Polls agents over HTTP with Basic Auth (port '48123'), aggregates health states ('HEALTHY', 'WARNING', 'CRITICAL'), tracks incidents, and caches GitHub release packages.
2. **CoreGuardAgent ('CoreGuard.exe')** — background Windows Service running in Session 0 from system boot (before user logon). Performs S.M.A.R.T., thermal, and disk capacity checks and leverages native Windows SCM Recovery ('sc failure' + 'os.Exit(1)') for reliable self-updates.
3. **System Tray Client ('CoreGuard.exe --tray')** — lightweight user-session tray applet registered via 'HKLM\SOFTWARE\Microsoft\Windows\CurrentVersion\Run'. Polls the local service at 'http://127.0.0.1:<port>/api/data', displays live drive health tooltips, opens the local dashboard, and automatically restarts after service updates.

### Key Features
- **One-Click OTA Updates via GitHub Releases:** check releases from the **📦 Release** menu, verify **SHA-256** checksums, self-update the Hub service, and push updates to single or multiple remote agents.
- **Clean Windows Installer:** 'CoreGuard_Setup_v2.5.5_x64.exe' installs only the core components (**Agent** and **Central Hub**).

### Optional Utilities ('utils/')
Standalone administrative tools are separated from the main installer into the 'utils/' directory (and attached separately to GitHub Releases):
- **'utils/coreguard_scanner.exe'** — CLI network subnet scanner for discovering Windows hosts and active CoreGuard agents on port '48123'.
- **'utils/coreguard_msp_setup.exe'** — MSP / enterprise branding profile generator ('msp_profile.json') for automated multi-client installer deployments.

---

## Быстрый старт / Quick Start

1. Скачайте установщик 'CoreGuard_Setup_v2.5.5_x64.exe' со страницы [Releases](https://github.com/gresseir-dev/CoreGuard/releases) и запустите от имени Администратора.
2. Выберите нужные компоненты (**Агент**, **Центральный Хаб** или оба на одном сервере).
3. Откройте веб-панель Центрального Хаба: 'http://localhost:9090'.

## История изменений / Changelog

Полный журнал изменений доступен в файле [CHANGELOG.md](CHANGELOG.md).
