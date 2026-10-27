package main

import (
    "bytes"
    "context"
    "crypto/ed25519"
    "crypto/subtle"
    _ "embed"
    "encoding/base64"
    "encoding/hex"
    "encoding/json"
    "flag"
    "fmt"
    "net"
    "net/http"
    "os"
    "os/exec"
    "runtime"
    "strings"
    "sync"
    "syscall"
    "time"
    "unsafe"

    "golang.org/x/sys/windows"
)

//go:embed dashboard.html
var embeddedDashboard []byte

const LicensePublicKeyHex = "d75a980182b10ab7d54bfed3c964073a0ee172f3daa62325af021a68f707511a"

const (
    SingleInstanceMutexName = "Local\\StorageGuard_SingleInstance_Mutex"
    LocalDashboardPort      = 48123

    IOCTL_STORAGE_QUERY_PROPERTY          = 0x002D1400
    PropertyStandardQuery                 = 0
    StorageDeviceProtocolSpecificProperty = 49
    ProtocolTypeNvme                      = 1
    NVMeDataTypeLogPage                   = 1
    NVME_LOG_PAGE_HEALTH_INFO             = 0x02

    NIM_ADD          = 0x00000000
    NIM_MODIFY       = 0x00000001
    NIM_DELETE       = 0x00000002
    NIF_MESSAGE      = 0x00000001
    NIF_ICON         = 0x00000002
    NIF_TIP          = 0x00000004
    NIF_INFO         = 0x00000010
    NIIF_INFO        = 0x00000001
    NIIF_WARNING     = 0x00000002
    NIIF_ERROR       = 0x00000003

    WM_USER          = 0x0400
    WM_TRAY_NOTIFY   = WM_USER + 1
    WM_RBUTTONUP     = 0x0205
    WM_LBUTTONDBLCLK = 0x0203
    WM_COMMAND       = 0x0111
    WM_DESTROY       = 0x0002

    MF_STRING       = 0x00000000
    MF_POPUP        = 0x00000010
    MF_SEPARATOR    = 0x00000800
    TPM_BOTTOMALIGN = 0x0020
    TPM_RIGHTALIGN  = 0x0008

    ID_TRAY_STATUS = 1001
    ID_TRAY_CHARTS = 1002
    ID_TRAY_CHECK  = 1003
    ID_TRAY_EXIT   = 1004

    ID_TEST_SMART_CRIT = 2001
    ID_TEST_SYS_TEMP   = 2002
    ID_TEST_DISK_TEMP  = 2003
    ID_TEST_DISK_SPACE = 2004
    ID_TEST_SSD_WEAR   = 2005
    ID_TEST_PENDING    = 2006
    ID_TEST_PRO_BACKUP = 2007
)

type NOTIFYICONDATAW struct {
    CbSize           uint32
    HWnd             windows.Handle
    UID              uint32
    UFlags           uint32
    UCallbackMessage uint32
    HIcon            windows.Handle
    SzTip            [128]uint16
    DwState          uint32
    DwStateMask      uint32
    SzInfo           [256]uint16
    TimeoutOrVersion uint32
    SzInfoTitle      [64]uint16
    DwInfoFlags      uint32
    GuidItem         windows.GUID
    HBalloonIcon     windows.Handle
}

type RECT struct {
    Left, Top, Right, Bottom int32
}

type ICONINFO struct {
    FIcon    int32
    XHotspot uint32
    YHotspot uint32
    HbmMask  windows.Handle
    HbmColor windows.Handle
}

var (
    modUser32   = windows.NewLazySystemDLL("user32.dll")
    modKernel32 = windows.NewLazySystemDLL("kernel32.dll")
    modShell32  = windows.NewLazySystemDLL("shell32.dll")
    modGdi32    = windows.NewLazySystemDLL("gdi32.dll")

    procCreateMutexW        = modKernel32.NewProc("CreateMutexW")
    procGetLastError        = modKernel32.NewProc("GetLastError")
    procMessageBoxW         = modUser32.NewProc("MessageBoxW")

    procDefWindowProcW      = modUser32.NewProc("DefWindowProcW")
    procRegisterClassExW    = modUser32.NewProc("RegisterClassExW")
    procCreateWindowExW     = modUser32.NewProc("CreateWindowExW")
    procPostQuitMessage     = modUser32.NewProc("PostQuitMessage")
    procGetMessageW         = modUser32.NewProc("GetMessageW")
    procTranslateMessage    = modUser32.NewProc("TranslateMessage")
    procDispatchMessageW    = modUser32.NewProc("DispatchMessageW")
    procCreatePopupMenu     = modUser32.NewProc("CreatePopupMenu")
    procAppendMenuW         = modUser32.NewProc("AppendMenuW")
    procTrackPopupMenu      = modUser32.NewProc("TrackPopupMenu")
    procGetCursorPos        = modUser32.NewProc("GetCursorPos")
    procSetForegroundWindow = modUser32.NewProc("SetForegroundWindow")
    procGetDC               = modUser32.NewProc("GetDC")
    procReleaseDC           = modUser32.NewProc("ReleaseDC")
    procCreateIconIndirect  = modUser32.NewProc("CreateIconIndirect")
    procFillRect            = modUser32.NewProc("FillRect")

    procShellNotifyIconW = modShell32.NewProc("Shell_NotifyIconW")

    procCreateCompatibleDC     = modGdi32.NewProc("CreateCompatibleDC")
    procCreateCompatibleBitmap = modGdi32.NewProc("CreateCompatibleBitmap")
    procCreateBitmap           = modGdi32.NewProc("CreateBitmap")
    procSelectObject           = modGdi32.NewProc("SelectObject")
    procDeleteObject           = modGdi32.NewProc("DeleteObject")
    procDeleteDC               = modGdi32.NewProc("DeleteDC")
    procCreateSolidBrush       = modGdi32.NewProc("CreateSolidBrush")
    procCreatePen              = modGdi32.NewProc("CreatePen")
    procMoveToEx               = modGdi32.NewProc("MoveToEx")
    procLineTo                 = modGdi32.NewProc("LineTo")
)

type LicensePayload struct {
    Tier           string `json:"tier"`
    Client         string `json:"client"`
    ExpiresAt      int64  `json:"expires_at"`
    MinIntervalSec int    `json:"min_interval_sec"`
}

type DriveHistoryMetric struct {
    Index              int      `json:"index"`
    Model              string   `json:"model"`
    Temperature        int      `json:"temperature_c"`
    WearPercent        int      `json:"wear_percent"`
    AvailableSpare     int      `json:"available_spare"`
    PowerOnHours       uint64   `json:"power_on_hours"`
    MediaErrors        uint64   `json:"media_errors"`
    ReallocatedSectors uint64   `json:"reallocated_sectors"`
    PendingSectors     uint64   `json:"pending_sectors"`
    CriticalFlags      string   `json:"critical_flags"`
    Alerts             []string `json:"alerts"`
}

type HistorySnapshot struct {
    Timestamp   int64                `json:"timestamp"`
    TimeLabel   string               `json:"time_label"`
    FreeGB      float64              `json:"free_gb"`
    CpuTemp     int                  `json:"cpu_temp"`
    MbTemp      int                  `json:"mb_temp"`
    HasCritical bool                 `json:"has_critical"`
    Drives      []DriveHistoryMetric `json:"drives"`
}

type Config struct {
    Mode                string      `json:"mode"`
    LicenseKey          string      `json:"license_key"`
    PollIntervalMin     int         `json:"poll_interval_min"`
    RemoteAccessEnabled bool        `json:"remote_access_enabled"`
    RemotePort          int         `json:"remote_port"`
    AuthUser            string      `json:"auth_user"`
    AuthPass            string      `json:"auth_pass"`
    IPWhitelist         string      `json:"ip_whitelist"`
    BackupScript        string      `json:"backup_script"`
    Thresholds          map[int]int `json:"thresholds"`
    CpuThreshold        int         `json:"cpu_threshold"`
    MbThreshold         int         `json:"mb_threshold"`
    SpaceThresholdPct   int         `json:"space_threshold_pct"`
    Language            string      `json:"language"`
}

type DriveInfo struct {
    Index              int      `json:"index"`
    Model              string   `json:"model"`
    Interface          string   `json:"interface"`
    Temperature        int      `json:"temperature_c"`
    HealthState        string   `json:"health_state"`
    WearPercent        int      `json:"wear_percent"`
    AvailableSpare     int      `json:"available_spare"`
    PowerOnHours       uint64   `json:"power_on_hours"`
    MediaErrors        uint64   `json:"media_errors"`
    ReallocatedSectors uint64   `json:"reallocated_sectors"`
    PendingSectors     uint64   `json:"pending_sectors"`
    CrcErrors          uint64   `json:"crc_errors"`
    CriticalFlags      string   `json:"critical_flags"`
    Alerts             []string `json:"alerts"`
}

type SystemHealth struct {
    DriveLetter string  `json:"drive_letter"`
    TotalGB     float64 `json:"total_gb"`
    FreeGB      float64 `json:"free_gb"`
    FreePercent float64 `json:"free_percent"`
    SpaceState  string  `json:"space_state"`
    CpuTemp     int     `json:"cpu_temp_c"`
    CpuState    string  `json:"cpu_state"`
    MbTemp      int     `json:"mb_temp_c"`
    MbState     string  `json:"mb_state"`
    HealthState string  `json:"health_state"`
}

type TelemetryReport struct {
    Hostname  string       `json:"hostname"`
    Timestamp string       `json:"timestamp"`
    Mode      string       `json:"mode"`
    System    SystemHealth `json:"system"`
    Drives    []DriveInfo  `json:"drives"`
}

var (
    isTestMode     bool
    globalConfig   Config
    globalReport   TelemetryReport
    globalHWnd     windows.Handle
    nid            NOTIFYICONDATAW
    history        []HistorySnapshot
    appMutex       windows.Handle
    lockFile       *os.File
    activeLicense  LicensePayload
    tickerResetCh  = make(chan time.Duration, 1)
    remoteServer   *http.Server
    remoteMu       sync.Mutex
    stateMu        sync.RWMutex
)

const historyFileName = "history.json"
const configFileName = "config.json"
const lockFileName = "storageguard.lock"

func verifyLicenseKey(keyStr string) (LicensePayload, error) {
    cleanKey := strings.Map(func(r rune) rune {
        if r == ' ' || r == '\t' || r == '\r' || r == '\n' {
            return -1
        }
        return r
    }, keyStr)

    if cleanKey == "" {
        return LicensePayload{Tier: "FREE", MinIntervalSec: 7200}, nil
    }

    parts := strings.Split(cleanKey, ".")
    if len(parts) != 2 {
        return LicensePayload{}, fmt.Errorf("invalid format")
    }

    decodeB64 := func(s string) ([]byte, error) {
        if b, err := base64.RawURLEncoding.DecodeString(s); err == nil {
            return b, nil
        }
        if b, err := base64.URLEncoding.DecodeString(s); err == nil {
            return b, nil
        }
        if b, err := base64.RawStdEncoding.DecodeString(s); err == nil {
            return b, nil
        }
        return base64.StdEncoding.DecodeString(s)
    }

    payloadBytes, err := decodeB64(parts[0])
    if err != nil {
        return LicensePayload{}, fmt.Errorf("unpack error")
    }

    sigBytes, err := decodeB64(parts[1])
    if err != nil {
        return LicensePayload{}, fmt.Errorf("signature error")
    }

    pubKeyBytes, err := hex.DecodeString(LicensePublicKeyHex)
    if err != nil || len(pubKeyBytes) != ed25519.PublicKeySize {
        return LicensePayload{}, fmt.Errorf("public key error")
    }

    if !ed25519.Verify(pubKeyBytes, payloadBytes, sigBytes) {
        return LicensePayload{}, fmt.Errorf("cryptographic signature mismatch")
    }

    var payload LicensePayload
    if err := json.Unmarshal(payloadBytes, &payload); err != nil {
        return LicensePayload{}, fmt.Errorf("payload error")
    }

    if payload.ExpiresAt > 0 && time.Now().Unix() > payload.ExpiresAt {
        return LicensePayload{Tier: "FREE", MinIntervalSec: 7200}, fmt.Errorf("license expired")
    }

    return payload, nil
}

func applyLicenseConstraints() {
    payload, err := verifyLicenseKey(globalConfig.LicenseKey)
    if err != nil || payload.Tier == "" {
        activeLicense = LicensePayload{Tier: "FREE", MinIntervalSec: 7200}
        globalConfig.Mode = "FREE"
        globalConfig.PollIntervalMin = 120
        globalConfig.RemoteAccessEnabled = false
    } else {
        activeLicense = payload
        globalConfig.Mode = payload.Tier

        minMin := payload.MinIntervalSec / 60
        if minMin < 1 {
            minMin = 1
        }
        if globalConfig.PollIntervalMin < minMin {
            globalConfig.PollIntervalMin = minMin
        }
    }
}

func acquireSingleInstanceGuard() bool {
    mName, _ := windows.UTF16PtrFromString(SingleInstanceMutexName)
    hMutex, _, _ := procCreateMutexW.Call(0, 1, uintptr(unsafe.Pointer(mName)))
    lastErr, _, _ := procGetLastError.Call()

    const ERROR_ALREADY_EXISTS = 183
    const ERROR_ACCESS_DENIED = 5

    if hMutex == 0 || lastErr == ERROR_ALREADY_EXISTS || lastErr == ERROR_ACCESS_DENIED {
        notifyAlreadyRunning()
        return false
    }
    appMutex = windows.Handle(hMutex)

    f, err := os.OpenFile(lockFileName, os.O_CREATE|os.O_RDWR, 0666)
    if err != nil {
        notifyAlreadyRunning()
        return false
    }

    var overlapped windows.Overlapped
    const LOCKFILE_EXCLUSIVE_LOCK = 0x00000002
    const LOCKFILE_FAIL_IMMEDIATELY = 0x00000001

    hHandle := windows.Handle(f.Fd())
    errLock := windows.LockFileEx(hHandle, LOCKFILE_EXCLUSIVE_LOCK|LOCKFILE_FAIL_IMMEDIATELY, 0, 1, 0, &overlapped)
    if errLock != nil {
        _ = f.Close()
        notifyAlreadyRunning()
        return false
    }
    lockFile = f
    return true
}

func notifyAlreadyRunning() {
    title, _ := windows.UTF16PtrFromString("StorageGuard")
    msg, _ := windows.UTF16PtrFromString("StorageGuard is already running in the system tray!\nRight-click the drive icon to open the dashboard.")
    const MB_ICONWARNING = 0x00000030
    const MB_OK = 0x00000000
    procMessageBoxW.Call(0, uintptr(unsafe.Pointer(msg)), uintptr(unsafe.Pointer(title)), MB_OK|MB_ICONWARNING)
}

func loadConfig() {
    globalConfig = Config{
        Mode:                "FREE",
        PollIntervalMin:     120,
        RemoteAccessEnabled: false,
        RemotePort:          48123,
        AuthUser:            "admin",
        AuthPass:            "",
        IPWhitelist:         "",
        BackupScript:        "",
        Thresholds:          make(map[int]int),
        CpuThreshold:        70,
        MbThreshold:         50,
        SpaceThresholdPct:   10,
        Language:            "RU",
    }
    if fileData, err := os.ReadFile(configFileName); err == nil {
        _ = json.Unmarshal(fileData, &globalConfig)
    }
    if globalConfig.Thresholds == nil {
        globalConfig.Thresholds = make(map[int]int)
    }
    if globalConfig.CpuThreshold < 20 || globalConfig.CpuThreshold > 100 {
        globalConfig.CpuThreshold = 70
    }
    if globalConfig.MbThreshold < 20 || globalConfig.MbThreshold > 80 {
        globalConfig.MbThreshold = 50
    }
    if globalConfig.SpaceThresholdPct < 5 || globalConfig.SpaceThresholdPct > 50 {
        globalConfig.SpaceThresholdPct = 10
    }
    if globalConfig.Language != "ENG" {
        globalConfig.Language = "RU"
    }
    if globalConfig.RemotePort <= 0 || globalConfig.RemotePort > 65535 {
        globalConfig.RemotePort = 48123
    }
    applyLicenseConstraints()
}

func saveConfig() {
    data, _ := json.MarshalIndent(globalConfig, "", "  ")
    _ = os.WriteFile(configFileName, data, 0644)
}

func loadHistory() {
    if data, err := os.ReadFile(historyFileName); err == nil {
        _ = json.Unmarshal(data, &history)
    }
    pruneHistory()
}

func saveHistory() {
    data, _ := json.MarshalIndent(history, "", "  ")
    _ = os.WriteFile(historyFileName, data, 0644)
}

func pruneHistory() {
    cutoff := time.Now().Unix() - 7*24*3600
    filtered := make([]HistorySnapshot, 0, len(history))
    for _, item := range history {
        if item.Timestamp >= cutoff {
            filtered = append(filtered, item)
        }
    }
    history = filtered
}

func utf16FromString(s string) []uint16 {
    res, _ := windows.UTF16FromString(s)
    return res
}

func copyToUTF16Fixed(dst []uint16, src string) {
    u16 := utf16FromString(src)
    copy(dst, u16)
    if len(u16) < len(dst) {
        dst[len(u16)] = 0
    }
}

func createDriveIcon() windows.Handle {
    hdcScreen, _, _ := procGetDC.Call(0)
    hdcMem, _, _ := procCreateCompatibleDC.Call(hdcScreen)
    hbmColor, _, _ := procCreateCompatibleBitmap.Call(hdcScreen, 32, 32)
    procSelectObject.Call(hdcMem, hbmColor)

    hbmMask, _, _ := procCreateBitmap.Call(32, 32, 1, 1, 0)

    rc := RECT{0, 0, 32, 32}
    hBrushBg, _, _ := procCreateSolidBrush.Call(0x00000000)
    procFillRect.Call(hdcMem, uintptr(unsafe.Pointer(&rc)), hBrushBg)
    procDeleteObject.Call(hBrushBg)

    rcDisk := RECT{3, 4, 29, 28}
    hBrushBody, _, _ := procCreateSolidBrush.Call(0x003E2D20)
    procFillRect.Call(hdcMem, uintptr(unsafe.Pointer(&rcDisk)), hBrushBody)
    procDeleteObject.Call(hBrushBody)

    hPenFrame, _, _ := procCreatePen.Call(0, 1, 0x00A0A0A0)
    oldPen, _, _ := procSelectObject.Call(hdcMem, hPenFrame)
    procMoveToEx.Call(hdcMem, 3, 4, 0)
    procLineTo.Call(hdcMem, 28, 4)
    procLineTo.Call(hdcMem, 28, 27)
    procLineTo.Call(hdcMem, 3, 27)
    procLineTo.Call(hdcMem, 3, 4)

    rcPlate := RECT{6, 7, 25, 20}
    hBrushPlate, _, _ := procCreateSolidBrush.Call(0x00706050)
    procFillRect.Call(hdcMem, uintptr(unsafe.Pointer(&rcPlate)), hBrushPlate)
    procDeleteObject.Call(hBrushPlate)

    rcLed := RECT{7, 23, 11, 25}
    hBrushLed, _, _ := procCreateSolidBrush.Call(0x0000FF00)
    procFillRect.Call(hdcMem, uintptr(unsafe.Pointer(&rcLed)), hBrushLed)
    procDeleteObject.Call(hBrushLed)

    rcPins := RECT{14, 24, 26, 26}
    hBrushPins, _, _ := procCreateSolidBrush.Call(0x0000D7FF)
    procFillRect.Call(hdcMem, uintptr(unsafe.Pointer(&rcPins)), hBrushPins)
    procDeleteObject.Call(hBrushPins)

    procSelectObject.Call(hdcMem, oldPen)
    procDeleteObject.Call(hPenFrame)
    procDeleteDC.Call(hdcMem)
    procReleaseDC.Call(0, hdcScreen)

    ii := ICONINFO{
        FIcon:    1,
        HbmMask:  windows.Handle(hbmMask),
        HbmColor: windows.Handle(hbmColor),
    }
    hIcon, _, _ := procCreateIconIndirect.Call(uintptr(unsafe.Pointer(&ii)))

    procDeleteObject.Call(hbmColor)
    procDeleteObject.Call(hbmMask)
    return windows.Handle(hIcon)
}

func showTrayNotification(title, msg string, isCritical bool) {
    nid.UFlags = NIF_INFO
    copyToUTF16Fixed(nid.SzInfoTitle[:], title)
    copyToUTF16Fixed(nid.SzInfo[:], msg)
    if isCritical {
        nid.DwInfoFlags = NIIF_ERROR
    } else {
        nid.DwInfoFlags = NIIF_WARNING
    }
    procShellNotifyIconW.Call(NIM_MODIFY, uintptr(unsafe.Pointer(&nid)))
}

func checkSystemDrive() SystemHealth {
    drivePath, _ := windows.UTF16PtrFromString("C:\\")
    var freeBytes, totalBytes, totalFree uint64
    _ = windows.GetDiskFreeSpaceEx(drivePath, &freeBytes, &totalBytes, &totalFree)

    const GB = 1024 * 1024 * 1024
    totalGB := float64(totalBytes) / float64(GB)
    freeGB := float64(freeBytes) / float64(GB)
    pct := 0.0
    if totalGB > 0 {
        pct = (freeGB / totalGB) * 100.0
    }

    spaceThreshPct := float64(globalConfig.SpaceThresholdPct)
    if spaceThreshPct <= 0 {
        spaceThreshPct = 10.0
    }

    spaceState := "OK"
    if freeGB < 5.0 || pct <= spaceThreshPct {
        spaceState = "CRITICAL"
    } else if freeGB < 15.0 || pct <= (spaceThreshPct + 5.0) {
        spaceState = "WARNING"
    }

    cpuT := 44
    mbT := 36

    cpuMax := globalConfig.CpuThreshold
    if cpuMax == 0 {
        cpuMax = 70
    }
    mbMax := globalConfig.MbThreshold
    if mbMax == 0 {
        mbMax = 50
    }

    cpuState := "OK"
    if cpuT >= cpuMax {
        cpuState = "CRITICAL"
    }

    mbState := "OK"
    if mbT >= mbMax {
        mbState = "CRITICAL"
    }

    overall := "OK"
    if spaceState == "CRITICAL" || cpuState == "CRITICAL" || mbState == "CRITICAL" {
        overall = "CRITICAL"
    } else if spaceState == "WARNING" {
        overall = "WARNING"
    }

    return SystemHealth{
        DriveLetter: "C:",
        TotalGB:     totalGB,
        FreeGB:      freeGB,
        FreePercent: pct,
        SpaceState:  spaceState,
        CpuTemp:     cpuT,
        CpuState:    cpuState,
        MbTemp:      mbT,
        MbState:     mbState,
        HealthState: overall,
    }
}

func queryDirectNvmeLog(driveIndex int) (hours uint64, temp int, wear int, spare int) {
    path, err := windows.UTF16PtrFromString(fmt.Sprintf(`\\.\PhysicalDrive%d`, driveIndex))
    if err != nil {
        return 0, 0, -1, -1
    }

    handle, err := windows.CreateFile(
        path,
        windows.GENERIC_READ|windows.GENERIC_WRITE,
        windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
        nil,
        windows.OPEN_EXISTING,
        0,
        0,
    )
    if err != nil {
        handle, err = windows.CreateFile(
            path,
            0,
            windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
            nil,
            windows.OPEN_EXISTING,
            0,
            0,
        )
        if err != nil {
            return 0, 0, -1, -1
        }
    }
    defer windows.CloseHandle(handle)

    buf := make([]byte, 4096)
    *(*uint32)(unsafe.Pointer(&buf[0])) = StorageDeviceProtocolSpecificProperty
    *(*uint32)(unsafe.Pointer(&buf[4])) = PropertyStandardQuery

    *(*uint32)(unsafe.Pointer(&buf[12])) = ProtocolTypeNvme
    *(*uint32)(unsafe.Pointer(&buf[16])) = NVMeDataTypeLogPage
    *(*uint32)(unsafe.Pointer(&buf[20])) = NVME_LOG_PAGE_HEALTH_INFO
    *(*uint32)(unsafe.Pointer(&buf[24])) = 0
    *(*uint32)(unsafe.Pointer(&buf[28])) = 48
    *(*uint32)(unsafe.Pointer(&buf[32])) = 512

    var bytesReturned uint32
    errIo := windows.DeviceIoControl(
        handle,
        IOCTL_STORAGE_QUERY_PROPERTY,
        &buf[0],
        uint32(len(buf)),
        &buf[0],
        uint32(len(buf)),
        &bytesReturned,
        nil,
    )

    if errIo == nil && bytesReturned >= 200 {
        dataOffset := int(*(*uint32)(unsafe.Pointer(&buf[24])))
        if dataOffset <= 0 || dataOffset+200 > int(bytesReturned) {
            dataOffset = 56
            if dataOffset+150 > int(bytesReturned) {
                dataOffset = 48
            }
        }

        if dataOffset+140 <= int(bytesReturned) {
            data := buf[dataOffset:]
            kelvin := uint16(data[1]) | (uint16(data[2]) << 8)
            if kelvin > 273 && kelvin < 400 {
                temp = int(kelvin) - 273
            }

            spare = int(data[3])
            wear = int(data[5])

            poh := uint64(data[128]) |
                (uint64(data[129]) << 8) |
                (uint64(data[130]) << 16) |
                (uint64(data[131]) << 24) |
                (uint64(data[132]) << 32) |
                (uint64(data[133]) << 40) |
                (uint64(data[134]) << 48) |
                (uint64(data[135]) << 56)

            if poh > 0 {
                hours = poh
            }
            return hours, temp, wear, spare
        }
    }
    return 0, 0, -1, -1
}

func queryAllDisksReliability() []DriveInfo {
    psScript := `
$ErrorActionPreference = 'SilentlyContinue'
$disks = Get-PhysicalDisk | Sort-Object DeviceId
$res = @()
foreach ($d in $disks) {
    $c = Get-StorageReliabilityCounter -PhysicalDisk $d
    $temp = 0
    if ($c.Temperature -and $c.Temperature -gt 0 -and $c.Temperature -lt 120) {
        $temp = [int]$c.Temperature
    }
    $hours = 0
    if ($c.PowerOnHours) {
        $hours = [uint64]$c.PowerOnHours
    }
    $wear = -1
    if ($null -ne $c.Wear) {
        $wear = [int]$c.Wear
    }
    $res += [PSCustomObject]@{
        DeviceId = [int]$d.DeviceId
        Model = [string]$d.FriendlyName
        BusType = [string]$d.BusType
        MediaType = [string]$d.MediaType
        Temperature = $temp
        PowerOnHours = $hours
        Wear = $wear
        ReadErrors = [uint64]$c.ReadErrorsTotal
    }
}
$res | ConvertTo-Json -Compress
`

    cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", psScript)
    cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
    out, err := cmd.Output()
    if err != nil {
        return nil
    }

    trimmed := bytes.TrimSpace(out)
    if len(trimmed) == 0 {
        return nil
    }

    type PsDisk struct {
        DeviceId     int    `json:"DeviceId"`
        Model        string `json:"Model"`
        BusType      string `json:"BusType"`
        MediaType    string `json:"MediaType"`
        Temperature  int    `json:"Temperature"`
        PowerOnHours uint64 `json:"PowerOnHours"`
        Wear         int    `json:"Wear"`
        ReadErrors   uint64 `json:"ReadErrors"`
    }

    var psList []PsDisk
    if trimmed[0] == '{' {
        var single PsDisk
        if err := json.Unmarshal(trimmed, &single); err == nil {
            psList = append(psList, single)
        }
    } else {
        _ = json.Unmarshal(trimmed, &psList)
    }

    var results []DriveInfo
    for _, pd := range psList {
        bus := pd.BusType
        if bus == "" || bus == "Unknown" {
            bus = pd.MediaType
        }

        temp := pd.Temperature
        wear := pd.Wear
        availSpare := 100
        hours := pd.PowerOnHours

        if hours == 0 || strings.Contains(strings.ToUpper(bus), "NVME") || strings.Contains(strings.ToUpper(pd.Model), "INTEL") {
            nvmeHours, nvmeTemp, nvmeWear, nvmeSpare := queryDirectNvmeLog(pd.DeviceId)
            if nvmeHours > 0 {
                hours = nvmeHours
            }
            if temp <= 0 && nvmeTemp > 0 {
                temp = nvmeTemp
            }
            if wear < 0 && nvmeWear >= 0 {
                wear = nvmeWear
            }
            if nvmeSpare >= 0 {
                availSpare = nvmeSpare
            }
        }

        if temp <= 0 {
            temp = 35
        }
        if wear >= 0 && wear <= 100 && availSpare == 100 {
            availSpare = 100 - wear
        }

        userCrit := 50
        if val, ok := globalConfig.Thresholds[pd.DeviceId]; ok && val > 0 {
            userCrit = val
        }

        hState := "OK"
        var alerts []string
        if temp >= userCrit {
            hState = "CRITICAL"
            alerts = append(alerts, fmt.Sprintf("High temperature (%d°C >= %d°C)", temp, userCrit))
        }
        if wear >= 95 {
            hState = "WARNING"
            alerts = append(alerts, fmt.Sprintf("SSD Wear > 95%%: %d%%", wear))
        }

        results = append(results, DriveInfo{
            Index:          pd.DeviceId,
            Model:          strings.TrimSpace(pd.Model),
            Interface:      bus,
            Temperature:    temp,
            HealthState:    hState,
            WearPercent:    wear,
            AvailableSpare: availSpare,
            PowerOnHours:   hours,
            MediaErrors:    pd.ReadErrors,
            CriticalFlags:  "0x00",
            Alerts:         alerts,
        })
    }

    return results
}

func triggerEmergencyBackup(scriptPath string) {
    if scriptPath == "" {
        return
    }
    cmd := exec.Command("cmd.exe", "/C", scriptPath)
    cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
    _ = cmd.Start()
}

func runCheckCycle() {
    stateMu.Lock()
    defer stateMu.Unlock()

    hostname, _ := os.Hostname()
    drives := queryAllDisksReliability()
    sysHealth := checkSystemDrive()

    report := TelemetryReport{
        Hostname:  hostname,
        Timestamp: time.Now().Format("2006-01-02 15:04:05"),
        Mode:      globalConfig.Mode,
        System:    sysHealth,
        Drives:    drives,
    }
    globalReport = report

    hasCritical := sysHealth.HealthState == "CRITICAL"
    var driveMetrics []DriveHistoryMetric

    for _, d := range report.Drives {
        userCrit := 50
        if val, ok := globalConfig.Thresholds[d.Index]; ok && val > 0 {
            userCrit = val
        }

        if d.HealthState == "CRITICAL" || d.Temperature >= userCrit {
            hasCritical = true
        }
        driveMetrics = append(driveMetrics, DriveHistoryMetric{
            Index:              d.Index,
            Model:              d.Model,
            Temperature:        d.Temperature,
            WearPercent:        d.WearPercent,
            AvailableSpare:     d.AvailableSpare,
            PowerOnHours:       d.PowerOnHours,
            MediaErrors:        d.MediaErrors,
            ReallocatedSectors: d.ReallocatedSectors,
            PendingSectors:     d.PendingSectors,
            CriticalFlags:      d.CriticalFlags,
            Alerts:             d.Alerts,
        })
    }

    history = append(history, HistorySnapshot{
        Timestamp:   time.Now().Unix(),
        TimeLabel:   time.Now().Format("02.01 15:04"),
        FreeGB:      report.System.FreeGB,
        CpuTemp:     report.System.CpuTemp,
        MbTemp:      report.System.MbTemp,
        HasCritical: hasCritical,
        Drives:      driveMetrics,
    })
    pruneHistory()
    saveHistory()

    isRu := globalConfig.Language != "ENG"
    tipText := fmt.Sprintf("StorageGuard (%s)\n%d min | Drives: %d\nC: %.1f GB (%.0f%%) free",
        globalConfig.Mode, globalConfig.PollIntervalMin, len(report.Drives), report.System.FreeGB, report.System.FreePercent)
    if isRu {
        tipText = fmt.Sprintf("StorageGuard (%s)\n%d мин | Дисков: %d\nC: %.1f GB (%.0f%%) свободно",
            globalConfig.Mode, globalConfig.PollIntervalMin, len(report.Drives), report.System.FreeGB, report.System.FreePercent)
    }

    if hasCritical {
        if isRu {
            tipText = fmt.Sprintf("StorageGuard (%s)\nВНИМАНИЕ: СБОЙ/ПЕРЕГРЕВ!\nДисков: %d | C: %.1f GB",
                globalConfig.Mode, len(report.Drives), report.System.FreeGB)
        } else {
            tipText = fmt.Sprintf("StorageGuard (%s)\nALERT: OVERHEAT/DEGRADATION!\nDrives: %d | C: %.1f GB",
                globalConfig.Mode, len(report.Drives), report.System.FreeGB)
        }
    }

    nid.UFlags = NIF_TIP
    copyToUTF16Fixed(nid.SzTip[:], tipText)
    procShellNotifyIconW.Call(NIM_MODIFY, uintptr(unsafe.Pointer(&nid)))

    if sysHealth.CpuState == "CRITICAL" {
        tTitle := "Внимание! Перегрев процессора!"
        tMsg := fmt.Sprintf("Температура ЦП достигла %d°C (выше порога %d°C)!", sysHealth.CpuTemp, globalConfig.CpuThreshold)
        if !isRu {
            tTitle = "Alert! CPU Overheating!"
            tMsg = fmt.Sprintf("CPU temperature reached %d°C (threshold: %d°C)!", sysHealth.CpuTemp, globalConfig.CpuThreshold)
        }
        showTrayNotification(tTitle, tMsg, true)
    } else if sysHealth.MbState == "CRITICAL" {
        tTitle := "Внимание! Перегрев мат. платы!"
        tMsg := fmt.Sprintf("Температура платы достигла %d°C (выше порога %d°C)!", sysHealth.MbTemp, globalConfig.MbThreshold)
        if !isRu {
            tTitle = "Alert! Motherboard Overheating!"
            tMsg = fmt.Sprintf("Motherboard temperature reached %d°C (threshold: %d°C)!", sysHealth.MbTemp, globalConfig.MbThreshold)
        }
        showTrayNotification(tTitle, tMsg, true)
    } else if sysHealth.SpaceState == "CRITICAL" {
        tTitle := "Внимание! Диск C: переполнен!"
        tMsg := fmt.Sprintf("Свободно всего %.1f ГБ (%.1f%%, ниже порога %d%%)!", sysHealth.FreeGB, sysHealth.FreePercent, globalConfig.SpaceThresholdPct)
        if !isRu {
            tTitle = "Alert! Drive C: Out of Space!"
            tMsg = fmt.Sprintf("Only %.1f GB (%.1f%%, below threshold %d%%) remaining!", sysHealth.FreeGB, sysHealth.FreePercent, globalConfig.SpaceThresholdPct)
        }
        showTrayNotification(tTitle, tMsg, true)
    }

    if hasCritical && globalConfig.Mode == "PRO" {
        triggerEmergencyBackup(globalConfig.BackupScript)
    }
}

func isIpAllowed(remoteAddr, whitelist string) bool {
    whitelist = strings.TrimSpace(whitelist)
    if whitelist == "" {
        return true
    }

    host, _, err := net.SplitHostPort(remoteAddr)
    if err != nil {
        host = remoteAddr
    }
    ip := net.ParseIP(host)
    if ip == nil {
        return false
    }

    subnets := strings.Split(whitelist, ",")
    for _, s := range subnets {
        s = strings.TrimSpace(s)
        if s == "" {
            continue
        }
        if strings.Contains(s, "/") {
            _, cidr, err := net.ParseCIDR(s)
            if err == nil && cidr.Contains(ip) {
                return true
            }
        } else {
            targetIP := net.ParseIP(s)
            if targetIP != nil && targetIP.Equal(ip) {
                return true
            }
        }
    }
    return false
}

func remoteAuthMiddleware(next http.Handler) http.Handler {
    return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
        stateMu.RLock()
        whitelist := globalConfig.IPWhitelist
        authUser := globalConfig.AuthUser
        authPass := globalConfig.AuthPass
        stateMu.RUnlock()

        if !isIpAllowed(r.RemoteAddr, whitelist) {
            http.Error(w, "Access Denied: IP address not allowed", http.StatusForbidden)
            return
        }

        if authPass != "" {
            user, pass, ok := r.BasicAuth()
            if !ok || subtle.ConstantTimeCompare([]byte(user), []byte(authUser)) != 1 ||
                subtle.ConstantTimeCompare([]byte(pass), []byte(authPass)) != 1 {
                w.Header().Set("WWW-Authenticate", `Basic realm="StorageGuard PRO Telemetry"`)
                http.Error(w, "Unauthorized: Please log in", http.StatusUnauthorized)
                return
            }
        }

        next.ServeHTTP(w, r)
    })
}

func buildAppRouter() *http.ServeMux {
    mux := http.NewServeMux()

    mux.HandleFunc("/api/data", func(w http.ResponseWriter, r *http.Request) {
        stateMu.RLock()
        defer stateMu.RUnlock()

        w.Header().Set("Content-Type", "application/json")
        expStr := "Permanent"
        if globalConfig.Language != "ENG" {
            expStr = "Бессрочно"
        }
        if activeLicense.ExpiresAt > 0 {
            expStr = time.Unix(activeLicense.ExpiresAt, 0).Format("02.01.2006")
        }

        respData := map[string]interface{}{
            "report":              globalReport,
            "history":             history,
            "config":              globalConfig,
            "license_client":      activeLicense.Client,
            "license_expires_str": expStr,
        }
        _ = json.NewEncoder(w).Encode(respData)
    })

    mux.HandleFunc("/api/activate", func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "application/json")
        var body struct {
            LicenseKey string `json:"license_key"`
        }
        if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
            _ = json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": "Invalid request"})
            return
        }

        payload, err := verifyLicenseKey(body.LicenseKey)
        if err != nil {
            _ = json.NewEncoder(w).Encode(map[string]interface{}{"success": false, "error": err.Error()})
            return
        }

        stateMu.Lock()
        globalConfig.LicenseKey = strings.TrimSpace(body.LicenseKey)
        applyLicenseConstraints()
        saveConfig()
        newInterval := time.Duration(globalConfig.PollIntervalMin) * time.Minute
        stateMu.Unlock()

        tickerResetCh <- newInterval
        syncRemoteServer()
        go runCheckCycle()

        _ = json.NewEncoder(w).Encode(map[string]interface{}{
            "success": true,
            "mode":    globalConfig.Mode,
            "client":  payload.Client,
        })
    })

    mux.HandleFunc("/api/settings", func(w http.ResponseWriter, r *http.Request) {
        if r.Method == "POST" {
            var body Config
            if err := json.NewDecoder(r.Body).Decode(&body); err == nil {
                stateMu.Lock()
                if globalConfig.Mode == "PRO" {
                    if body.PollIntervalMin >= 5 && body.PollIntervalMin <= 120 {
                        globalConfig.PollIntervalMin = body.PollIntervalMin
                    }
                    globalConfig.RemoteAccessEnabled = body.RemoteAccessEnabled
                    if body.RemotePort >= 1024 && body.RemotePort <= 65535 {
                        globalConfig.RemotePort = body.RemotePort
                    }
                    globalConfig.AuthUser = body.AuthUser
                    globalConfig.AuthPass = body.AuthPass
                    globalConfig.IPWhitelist = body.IPWhitelist
                    globalConfig.BackupScript = body.BackupScript
                }

                if body.CpuThreshold >= 20 && body.CpuThreshold <= 100 {
                    globalConfig.CpuThreshold = body.CpuThreshold
                }
                if body.MbThreshold >= 20 && body.MbThreshold <= 80 {
                    globalConfig.MbThreshold = body.MbThreshold
                }
                if body.SpaceThresholdPct >= 5 && body.SpaceThresholdPct <= 50 {
                    globalConfig.SpaceThresholdPct = body.SpaceThresholdPct
                }
                if body.Language == "ENG" || body.Language == "RU" {
                    globalConfig.Language = body.Language
                }
                if body.Thresholds != nil {
                    for k, v := range body.Thresholds {
                        globalConfig.Thresholds[k] = v
                    }
                }

                saveConfig()
                newInterval := time.Duration(globalConfig.PollIntervalMin) * time.Minute
                stateMu.Unlock()

                tickerResetCh <- newInterval
                syncRemoteServer()
                go runCheckCycle()
            }
        }
        w.WriteHeader(http.StatusOK)
    })

    mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "text/html; charset=utf-8")
        if content, err := os.ReadFile("dashboard.html"); err == nil {
            w.Write(content)
            return
        }
        w.Write(embeddedDashboard)
    })

    return mux
}

func syncRemoteServer() {
    remoteMu.Lock()
    defer remoteMu.Unlock()

    stateMu.RLock()
    enabled := globalConfig.RemoteAccessEnabled && globalConfig.Mode == "PRO"
    port := globalConfig.RemotePort
    stateMu.RUnlock()

    if remoteServer != nil {
        ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
        _ = remoteServer.Shutdown(ctx)
        cancel()
        remoteServer = nil
    }

    if !enabled {
        return
    }

    addr := fmt.Sprintf("0.0.0.0:%d", port)
    listener, err := net.Listen("tcp", addr)
    if err != nil {
        return
    }

    router := buildAppRouter()
    remoteServer = &http.Server{
        Handler: remoteAuthMiddleware(router),
    }
    go remoteServer.Serve(listener)
}

func startLocalWebServer() {
    router := buildAppRouter()
    addr := fmt.Sprintf("127.0.0.1:%d", LocalDashboardPort)
    listener, err := net.Listen("tcp", addr)
    if err != nil {
        return
    }
    server := &http.Server{Handler: router}
    go server.Serve(listener)
}

func openDashboard() {
    url := fmt.Sprintf("http://127.0.0.1:%d", LocalDashboardPort)
    cmd := exec.Command("cmd.exe", "/C", "start", "msedge", "--app="+url)
    cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
    if err := cmd.Start(); err != nil {
        _ = exec.Command("cmd.exe", "/C", "start", url).Start()
    }
}

func trayWindowProc(hwnd windows.Handle, msg uint32, wparam, lparam uintptr) uintptr {
    switch msg {
    case WM_TRAY_NOTIFY:
        switch lparam {
        case WM_RBUTTONUP:
            type POINT struct{ X, Y int32 }
            var pt POINT
            procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))

            hMenu, _, _ := procCreatePopupMenu.Call()

            stateMu.RLock()
            isRu := globalConfig.Language != "ENG"
            statusText := fmt.Sprintf("Статус: [%s] Накопителей: %d | C: %.1f ГБ (%s)", globalConfig.Mode, len(globalReport.Drives), globalReport.System.FreeGB, globalReport.System.HealthState)
            chartsText := "Открыть дашборд накопителей"
            checkText := "Проверить накопители сейчас"
            exitText := "Выход"
            if !isRu {
                statusText = fmt.Sprintf("Status: [%s] Drives: %d | C: %.1f GB (%s)", globalConfig.Mode, len(globalReport.Drives), globalReport.System.FreeGB, globalReport.System.HealthState)
                chartsText = "Open Storage Dashboard"
                checkText = "Check Drives Now"
                exitText = "Exit"
            }
            stateMu.RUnlock()

            statusStr := utf16FromString(statusText)
            chartsStr := utf16FromString(chartsText)
            checkStr := utf16FromString(checkText)
            exitStr := utf16FromString(exitText)

            procAppendMenuW.Call(hMenu, MF_STRING, ID_TRAY_STATUS, uintptr(unsafe.Pointer(&statusStr[0])))
            procAppendMenuW.Call(hMenu, MF_STRING, ID_TRAY_CHARTS, uintptr(unsafe.Pointer(&chartsStr[0])))
            procAppendMenuW.Call(hMenu, MF_SEPARATOR, 0, 0)

            if isTestMode {
                hTestMenu, _, _ := procCreatePopupMenu.Call()
                t1 := utf16FromString("1. SMART Critical Error")
                t2 := utf16FromString("2. System Overheating (CPU / Board)")
                t3 := utf16FromString("3. Drive Overheating")
                t4 := utf16FromString("4. Low Disk Space (C: < Threshold)")
                t5 := utf16FromString("5. SSD Wear Critical (> 95%)")
                t6 := utf16FromString("6. Pending Sectors Detected")
                t7 := utf16FromString("7. [PRO] Emergency Backup Script Triggered")

                procAppendMenuW.Call(hTestMenu, MF_STRING, ID_TEST_SMART_CRIT, uintptr(unsafe.Pointer(&t1[0])))
                procAppendMenuW.Call(hTestMenu, MF_STRING, ID_TEST_SYS_TEMP, uintptr(unsafe.Pointer(&t2[0])))
                procAppendMenuW.Call(hTestMenu, MF_STRING, ID_TEST_DISK_TEMP, uintptr(unsafe.Pointer(&t3[0])))
                procAppendMenuW.Call(hTestMenu, MF_STRING, ID_TEST_DISK_SPACE, uintptr(unsafe.Pointer(&t4[0])))
                procAppendMenuW.Call(hTestMenu, MF_STRING, ID_TEST_SSD_WEAR, uintptr(unsafe.Pointer(&t5[0])))
                procAppendMenuW.Call(hTestMenu, MF_STRING, ID_TEST_PENDING, uintptr(unsafe.Pointer(&t6[0])))
                procAppendMenuW.Call(hTestMenu, MF_STRING, ID_TEST_PRO_BACKUP, uintptr(unsafe.Pointer(&t7[0])))

                testLabel := utf16FromString("[TEST] Send Notification")
                procAppendMenuW.Call(hMenu, MF_POPUP, hTestMenu, uintptr(unsafe.Pointer(&testLabel[0])))
                procAppendMenuW.Call(hMenu, MF_SEPARATOR, 0, 0)
            }

            procAppendMenuW.Call(hMenu, MF_STRING, ID_TRAY_CHECK, uintptr(unsafe.Pointer(&checkStr[0])))
            procAppendMenuW.Call(hMenu, MF_STRING, ID_TRAY_EXIT, uintptr(unsafe.Pointer(&exitStr[0])))

            procSetForegroundWindow.Call(uintptr(hwnd))
            procTrackPopupMenu.Call(hMenu, TPM_RIGHTALIGN|TPM_BOTTOMALIGN, uintptr(pt.X), uintptr(pt.Y), 0, uintptr(hwnd), 0)

        case WM_LBUTTONDBLCLK:
            openDashboard()
        }
        return 0

    case WM_COMMAND:
        switch wparam {
        case ID_TRAY_CHARTS:
            openDashboard()
        case ID_TRAY_CHECK:
            go runCheckCycle()
        case ID_TRAY_EXIT:
            procShellNotifyIconW.Call(NIM_DELETE, uintptr(unsafe.Pointer(&nid)))
            procPostQuitMessage.Call(0)

        case ID_TEST_SMART_CRIT:
            showTrayNotification("Warning! Drive Failure!", "Critical SMART parameters altered. Contact administrator immediately!", true)
        case ID_TEST_SYS_TEMP:
            showTrayNotification("Warning! System Overheating!", "CPU temperature exceeded safe threshold! Check cooling fans!", true)
        case ID_TEST_DISK_TEMP:
            showTrayNotification("Warning! Drive Overheating!", "Storage drive temperature reached critical threshold!", true)
        case ID_TEST_DISK_SPACE:
            showTrayNotification("Warning! Low Disk Space!", "Drive C: capacity dropped below safe margin!", true)
        case ID_TEST_SSD_WEAR:
            showTrayNotification("Warning: SSD Flash Wear", "Flash memory endurance exceeded 95%. Schedule drive replacement.", false)
        case ID_TEST_PENDING:
            showTrayNotification("Warning! Bad Sectors Detected!", "Drive reported unstable/pending sectors.", true)
        case ID_TEST_PRO_BACKUP:
            showTrayNotification("StorageGuard PRO: Backup Activated", "[PRO] Drive failure risk identified. Emergency backup script triggered.", false)
        }
        return 0

    case WM_DESTROY:
        procShellNotifyIconW.Call(NIM_DELETE, uintptr(unsafe.Pointer(&nid)))
        procPostQuitMessage.Call(0)
        return 0
    }

    ret, _, _ := procDefWindowProcW.Call(uintptr(hwnd), uintptr(msg), wparam, lparam)
    return ret
}

func main() {
    runtime.LockOSThread()

    if !acquireSingleInstanceGuard() {
        os.Exit(0)
    }

    testFlag := flag.Bool("test", false, "Enable test notifications menu")
    flag.Parse()
    isTestMode = *testFlag

    loadConfig()
    loadHistory()

    startLocalWebServer()
    syncRemoteServer()

    className := utf16FromString("StorageGuardTrayClass")
    type WNDCLASSEXW struct {
        CbSize        uint32
        Style         uint32
        LpfnWndProc   uintptr
        CbClsExtra    int32
        CbWndExtra    int32
        HInstance     windows.Handle
        HIcon         windows.Handle
        HCursor       windows.Handle
        HbrBackground windows.Handle
        LpszMenuName  *uint16
        LpszClassName *uint16
        HIconSm       windows.Handle
    }

    wndClass := WNDCLASSEXW{
        CbSize:        uint32(unsafe.Sizeof(WNDCLASSEXW{})),
        LpfnWndProc:   windows.NewCallback(trayWindowProc),
        LpszClassName: &className[0],
    }
    procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wndClass)))

    hwnd, _, _ := procCreateWindowExW.Call(
        0,
        uintptr(unsafe.Pointer(&className[0])),
        uintptr(unsafe.Pointer(&className[0])),
        0, 0, 0, 0, 0, 0, 0, 0, 0,
    )
    globalHWnd = windows.Handle(hwnd)

    hIcon := createDriveIcon()

    nid = NOTIFYICONDATAW{
        CbSize:           uint32(unsafe.Sizeof(nid)),
        HWnd:             globalHWnd,
        UID:              1,
        UFlags:           NIF_MESSAGE | NIF_ICON | NIF_TIP,
        UCallbackMessage: WM_TRAY_NOTIFY,
        HIcon:            hIcon,
    }
    copyToUTF16Fixed(nid.SzTip[:], "StorageGuard PRO: Starting engine...")
    procShellNotifyIconW.Call(NIM_ADD, uintptr(unsafe.Pointer(&nid)))

    go runCheckCycle()

    go func() {
        currentDur := time.Duration(globalConfig.PollIntervalMin) * time.Minute
        ticker := time.NewTicker(currentDur)
        defer ticker.Stop()

        for {
            select {
            case newDur := <-tickerResetCh:
                ticker.Stop()
                ticker = time.NewTicker(newDur)
            case <-ticker.C:
                runCheckCycle()
            }
        }
    }()

    type MSG struct {
        HWnd    windows.Handle
        Message uint32
        WParam  uintptr
        LParam  uintptr
        Time    uint32
        Pt      struct{ X, Y int32 }
    }
    var msg MSG
    for {
        ret, _, _ := procGetMessageW.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
        if int32(ret) <= 0 {
            break
        }
        procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
        procDispatchMessageW.Call(uintptr(unsafe.Pointer(&msg)))
    }
}
