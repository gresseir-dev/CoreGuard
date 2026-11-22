// CoreGuard Enterprise - Native Windows SMART Bridge (GPLv3)
package main

import (
    "encoding/json"
    "fmt"
    "net/http"
    "strings"
    "sync"
    "time"
    "storageguard/internal/smart"
)

type PhysicalProbeResult struct {
    TemperatureC       int
    PowerOnHours       uint64
    WearPercent        int
    AvailableSpare     int
    MediaErrors        uint64
    PendingSectors     uint64
    ReallocatedSectors uint64
    IsNVMe             bool
}

var (
    smartCacheMu   sync.Mutex
    smartCacheTime time.Time
    smartCacheData []smart.DriveHealth
)

func getCachedSmartDrives() []smart.DriveHealth {
    smartCacheMu.Lock()
    defer smartCacheMu.Unlock()
    if time.Since(smartCacheTime) > 3*time.Second || len(smartCacheData) == 0 {
        smartCacheData = smart.ScanAllDrives()
        smartCacheTime = time.Now()
    }
    return smartCacheData
}

func ProbePhysicalStorage(deviceID int, friendlyName string) (*PhysicalProbeResult, error) {
    drives := getCachedSmartDrives()
    var matched *smart.DriveHealth
    for i := range drives {
        if drives[i].Index == deviceID {
            matched = &drives[i]
            break
        }
        if friendlyName != "" && strings.Contains(strings.ToLower(drives[i].Model), strings.ToLower(strings.TrimSpace(friendlyName))) {
            matched = &drives[i]
            break
        }
    }
    if matched == nil {
        return nil, fmt.Errorf("drive not found in native smart: %d", deviceID)
    }

    wear := 100 - matched.HealthPercent
    if wear < 0 { wear = 0 }
    if wear > 100 { wear = 100 }

    res := &PhysicalProbeResult{
        TemperatureC:   matched.TemperatureC,
        PowerOnHours:   matched.PowerOnHours,
        WearPercent:    wear,
        AvailableSpare: 100,
        IsNVMe:         matched.Protocol == "NVMe",
    }

    for _, a := range matched.Attributes {
        if matched.Protocol == "NVMe" {
            switch a.ID {
            case 3:
                res.AvailableSpare = int(a.Raw)
            case 14:
                res.MediaErrors = a.Raw
            }
        } else {
            switch a.ID {
            case 0x05:
                res.ReallocatedSectors = a.Raw
            case 0xC5, 0xC6:
                res.PendingSectors += a.Raw
            }
        }
    }
    return res, nil
}

func handleNativeSmartAPI(w http.ResponseWriter, r *http.Request) {
    w.Header().Set("Content-Type", "application/json; charset=utf-8")
    json.NewEncoder(w).Encode(smart.ScanAllDrives())
}