# CoreGuard Enterprise v2.1.5

Enterprise infrastructure and workstation telemetry monitoring suite.

## Components
1. **CoreGuard Agent (CoreGuard.exe)** — Lightweight background telemetry collector (default port: `48123`).
2. **CoreGuard Central Hub (coreguard_hub.exe)** — Hybrid Central Hub (Enterprise NOC) operating either as a standalone console utility or as a native Windows Service (default port: `9090`).

## Quick Start
- **Console Mode:** Run `coreguard_hub.exe`. Access the dashboard at `http://localhost:9090`.
- **Windows Service Mode:** Run `CoreGuard_Setup_v2.1.5.exe` with Administrator privileges. The `CoreGuardHub` service is registered and set to start automatically.

## Configuration Files
- `config.json` — Local agent parameters.
- `hub_config.json` — Central hub thresholds, alerting rules, and telemetry intervals.
- `nodes.json` — Managed node registry.
