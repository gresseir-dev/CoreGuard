# CoreGuard Enterprise

**Lightweight, Open-Source Hardware & Storage Health Monitoring Suite for Windows (GPLv3)**

**CoreGuard Enterprise** is a zero-dependency monitoring suite written in Go for system administrators, field engineers, and Managed Service Providers (MSPs). It queries NVMe and SATA SMART diagnostics directly via native Windows kernel IOCTL calls, tracks CPU/motherboard thermals, triggers emergency backup scripts, and aggregates fleet status into a central web dashboard (**CoreGuard Central Hub**).

---

## Key Features

### Monitoring Agent (CoreGuard.exe)
* **Native Windows IOCTL SMART:** Direct communication with NVMe and SATA storage drivers without external CLI dependencies (smartctl.exe is not required), providing full SMART attribute tables, wear-level metrics, power-on hours, and total bytes written.
* **Per-Drive Controls:** Individual temperature thresholds per physical drive and a one-click **Ignore Drive** toggle to exclude specific drives from alerts and central hub summaries.
* **System Thermals & Volume Tracking:** Live monitoring of CPU/motherboard temperatures and critical free space thresholds on drive C: with interactive SVG trend charts.
* **MSP Branding & Automated Response:** Displays IT support provider contacts (phone, Telegram, email), blocking critical alert popups, and automated .bat/.ps1 emergency backup script execution on impending drive failure.
* **Silent System Tray Agent:** Runs silently in the background (-H=windowsgui) with a bilingual (EN/RU) local web dashboard at http://127.0.0.1:48123/ and optional Basic Auth + IP Whitelist for LAN access.

### Central Server (coreguard_hub.exe)
* **Centralized NOC Dashboard (:9090):** Runs as a native Windows Service (CoreGuardHub), aggregating fleet health, storage status, and incident logs.
* **Remote Branding Sync:** Pushes client organization names and IT support contact cards directly to monitored endpoints.
* **Telegram Alerting:** Instant notifications on critical SSD/HDD wear, overheating, or offline nodes.

### Enterprise Installer (Inno Setup 6)
* Deployment profiles for **Client Workstation (Agent only)** or **Admin Server (Agent + Central Hub)**.
* **Uninstall Configuration Retention:** Interactive uninstall prompt preserves config.json, hub_config.json, and nodes.json by default for seamless upgrades and testing.

---

## Build from Source

`powershell
go build -ldflags="-s -w -H windowsgui" -o CoreGuard.exe .
go build -ldflags="-s -w -H windowsgui" -o coreguard_hub.exe ./cmd/hub
.\build.ps1
`",
  ",
  

Licensed under the **GNU General Public License v3.0 (GPL-3.0)**. See [LICENSE](LICENSE).