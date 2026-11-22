# CoreGuard Enterprise

**Lightweight, Open-Source Hardware & Storage Health Monitoring Suite for Windows (GPLv3)**

CoreGuard Enterprise is a zero-dependency monitoring suite written in Go for system administrators and IT infrastructure engineers. It reads NVMe/SATA SMART diagnostics directly via Windows kernel IOCTL calls, monitors thermal sensors, and aggregates fleet status into a central NOC dashboard.

## Features
- **Native Windows IOCTL SMART:** Direct communication with NVMe and SATA storage drivers without external CLI tools (no smartctl.exe required).
- **Accurate SSD/HDD Health:** Tracks wear level (Percentage Used, Available Spare, SSD Life Left), temperature, power-on hours, data written, and full SMART attributes inside the local dashboard.
- **Silent System Tray Agent:** Runs silently in background (-H=windowsgui) with live thermal charts.
- **CoreGuard Central Hub:** Centralized web console (:9090) with node health overview, incident log, and Telegram alerts.

## Build
```powershell
go build -ldflags="-H=windowsgui" -o CoreGuard.exe .
go build -ldflags="-H=windowsgui" -o coreguard_hub.exe ./cmd/hub
.\build.ps1
```

## License
Licensed under the **GNU General Public License v3.0 (GPL-3.0)**. See [LICENSE](LICENSE).