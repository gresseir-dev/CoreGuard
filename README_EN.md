# CoreGuard Enterprise

**CoreGuard Enterprise** is a centralized monitoring and fleet management solution for **Windows** workstations and servers, tracking drive health (**S.M.A.R.T.**), **CPU** and **motherboard/drive temperatures**, free system drive space, and incident history.

Current release: **v2.3.31** • License: **GNU GPLv3** • Repository: [github.com/gresseir-dev/CoreGuard](https://github.com/gresseir-dev/CoreGuard)

---

## Key Features

* **Central Hub & Agent Architecture:**
  * **CoreGuard Central Hub** (web dashboard on port 9090) polls distributed agents over HTTP with **Basic Auth** (default port 48123), tracking node states (HEALTHY, WARNING, CRITICAL) and alert events.
  * **Automatic Localhost Provisioning (local-pc):** when starting with an empty node list, the Hub provisions 127.0.0.1:48123 and syncs credentials/port from an adjacent config.json when available, while protecting the local node during configuration saves.
* **Unified OTA Release & Agent Updates via GitHub Releases:**
  * From the **📦 Релиз (Release)** menu or update banner, administrators can check GitHub Releases and use a **single release update action** to pull the latest CoreGuard.exe into eleases/ (verified by **SHA-256**) and update coreguard_hub.exe with automatic CoreGuardHub service restart when a newer Hub binary is available.
  * One-click single-node or fleet-wide agent rollout over the network with safe old-PID wait and clean restart.
* **Refined Dashboard & Alerting:**
  * Clean two-row node card headers (node name and status badge on top; group, endpoint, version pill, and unread incident indicator below), grid/list views, incident log, and **Telegram** notifications.
  * Built-in **Quick Help** and **About** dialogs in the release menu, plus MSP / client organization branding.
* **Streamlined Windows Installer:**
  * Supports installing the **Agent**, **Central Hub** (CoreGuardHub), or both, with the desktop shortcut task unchecked by default for cleaner server and workstation deployments.

---

## Quick Start

1. Download CoreGuard_Setup_v2.3.31_x64.exe from [GitHub Releases](https://github.com/gresseir-dev/CoreGuard/releases) and run as Administrator.
2. Select the desired components (**Agent**, **Central Hub**, or both).
3. Open the Central Hub dashboard at http://localhost:9090.
4. Open **⚙ Настройки & Узлы** to register remote agents (<host>:48123), configure Telegram alerts, and set organization branding.

---

## Building from Source

\\\powershell
Set-Location "C:\CoreGuard"
.\build.ps1
\\\

This builds the agent (CoreGuard.exe), Central Hub (coreguard_hub.exe), populates eleases/, and packages the installer.