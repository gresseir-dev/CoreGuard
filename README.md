======================================================================
                         STORAGEGUARD (EN)
======================================================================
Platform: Windows 10 / 11 / Windows Server 2016+
Language: Go 1.21+
License:  Proprietary (gresseir-dev)

OVERVIEW
----------------------------------------------------------------------
StorageGuard is a high-reliability hardware telemetry and emergency
response system designed for Windows. Built specifically to protect
critical databases (1C:Enterprise, MS SQL, PostgreSQL) from silent SSD
wear-out, overheating, and sudden storage failure.

Unlike standard monitoring tools, StorageGuard bypasses buggy WMI/OS
storage layers using direct low-level SCSI pass-through and NVMe protocol
queries, providing immediate reactive automation when hardware
degradation is detected.


KEY FEATURES
----------------------------------------------------------------------
* Direct Hardware Polling: Bypasses Windows driver limitations (such as
  standard WMI counters returning 0 or $null on Intel 660p/760p NVMe
  drives) by reading NVMe Log Page 0x02 directly.
* Hardware Agnostic Engine (smartprobe): Vendor-specific decoding
  profiles for Intel/Solidigm, Samsung V-NAND, Western Digital/SanDisk,
  and generic JEDEC NVMe/ATA.
* Trigger-Based Emergency Backup: Initiates VSS-based emergency database
  evacuation scripts immediately when critical health indicators degrade,
  rather than relying strictly on fixed schedules.
* Decoupled System Health Telemetry: Independent tracking for system
  drive C: capacity (visualized via a dynamic donut chart with
  customizable % threshold) and CPU/motherboard temperatures.
* Built-in Windows Tray Agent: Lightweight WinAPI tray integration,
  silent background execution, single-instance mutex guard, and native
  notifications.
* Dual-Zone Web Dashboard: Embedded local UI (localhost:48123) and a
  secure remote network dashboard protected by HTTP Basic Authentication
  and CIDR IP whitelisting.


ARCHITECTURE & EDITIONS
----------------------------------------------------------------------
Feature                    | FREE        | PRO             | ENTERPRISE
---------------------------+-------------+-----------------+------------------
Polling Interval           | 120 minutes | 5 - 120 minutes | 1 - 120 minutes
Direct Hardware Engine     | Yes         | Yes             | Yes
Local Dashboard            | Yes         | Yes             | Yes
Custom Thresholds          | Fixed       | Configurable    | Configurable
Emergency Script Execution | No          | Automatic       | Automatic
Remote Network Dashboard   | No          | Auth + Whitelist| Multi-host Hub
Central Fleet Aggregation  | No          | No              | Central Ingress


BUILDING FROM SOURCE
----------------------------------------------------------------------
Prerequisites:
  - Windows 10 / 11 / Server 2016+
  - Go 1.21 or newer
  - Administrative privileges (required for \\.\PhysicalDriveX)

Build Command:
  go build -ldflags="-H windowsgui -s -w" -o StorageGuard.exe main.go smartprobe.go

Run in Test Mode:
  StorageGuard.exe -test


SECURITY & PRIVACY
----------------------------------------------------------------------
StorageGuard executes locally. Telemetry logs and configuration remain
within the local host boundary unless Enterprise fleet aggregation or
network remote access is explicitly enabled by the administrator.
Cryptographic license validation is implemented offline via Ed25519
digital signatures.

Copyright (c) gresseir-dev. All rights reserved.
======================================================================
