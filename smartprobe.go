package main

import (
    "encoding/binary"
    "encoding/json"
    "fmt"
    "os/exec"
    "strings"
    "syscall"
    "unsafe"

    "golang.org/x/sys/windows"
)

const (
    IOCTL_STORAGE_QUERY_PROPERTY          = 0x002D1400
    PropertyStandardQuery                 = 0
    StorageDeviceProtocolSpecificProperty = 49
    ProtocolTypeNvme                      = 1
    NVMeDataTypeLogPage                   = 1
    NVME_LOG_PAGE_HEALTH_INFO             = 0x02
)

// Структуры в точности по спецификации Windows DDK / smartmontools (os_win32.cpp)
type STORAGE_PROPERTY_QUERY struct {
    PropertyId           uint32
    QueryType            uint32
    AdditionalParameters [1]byte
    Padding              [3]byte
}

type STORAGE_PROTOCOL_SPECIFIC_DATA struct {
    ProtocolType                uint32
    DataType                    uint32
    ProtocolDataRequestValue    uint32
    ProtocolDataRequestSubValue uint32
    ProtocolDataOffset          uint32
    ProtocolDataLength          uint32
    FixedProtocolReturnData     uint32
    ProtocolDataRequestSubValue2 uint32
    ProtocolDataRequestSubValue3 uint32
    Reserved                    uint32
}

// 512-байтный буфер NVMe SMART Health Information Log (NVMe Spec 1.4, Section 5.14.1.2)
type NVME_HEALTH_INFO_LOG struct {
    CriticalWarning         uint8
    Temperature             [2]uint8
    AvailableSpare          uint8
    AvailableSpareThreshold uint8
    PercentageUsed          uint8
    Reserved1               [26]uint8
    DataUnitsRead           [16]uint8
    DataUnitsWritten        [16]uint8
    HostReadCommands        [16]uint8
    HostWriteCommands       [16]uint8
    ControllerBusyTime      [16]uint8
    PowerCycles             [16]uint8
    PowerOnHours            [16]uint8
    UnsafeShutdowns         [16]uint8
    MediaErrors             [16]uint8
    NumErrorEntries         [16]uint8
    WarningTempTime         uint32
    CritTempTime            uint32
    Reserved2               [380]uint8
}

type SmartData struct {
    VendorProfile      string
    IsNVMe             bool
    TemperatureC       int
    WearPercent        int
    AvailableSpare     int
    PowerOnHours       uint64
    MediaErrors        uint64
    ReallocatedSectors uint64
    PendingSectors     uint64
}

func openDriveHandle(driveIndex int) (windows.Handle, error) {
    drivePath, err := windows.UTF16PtrFromString(fmt.Sprintf(`\\.\PhysicalDrive%d`, driveIndex))
    if err != nil {
        return 0, err
    }
    return windows.CreateFile(
        drivePath,
        windows.GENERIC_READ|windows.GENERIC_WRITE,
        windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
        nil,
        windows.OPEN_EXISTING,
        0,
        0,
    )
}

// Аппаратный опрос SATA дисков через WMI MSStorageDriver_ATAPISmartData
func querySataSmartFallback(driveIndex int) *SmartData {
    psScript := fmt.Sprintf(`
$ErrorActionPreference = 'SilentlyContinue'
$poh = [uint64]0; $temp = 0; $realloc = [uint64]0; $pending = [uint64]0

$drive = Get-CimInstance Win32_DiskDrive | Where-Object { $_.Index -eq %d }
if ($drive) {
    $smart = Get-CimInstance -Namespace root\wmi -ClassName MSStorageDriver_ATAPISmartData | Where-Object {
        $_.InstanceName -like "*$($drive.PNPDeviceID)*" -or ($drive.Model -and $_.InstanceName -like "*$($drive.Model)*")
    } | Select-Object -First 1

    if ($smart -and $smart.VendorSpecific) {
        $b = $smart.VendorSpecific
        for ($i = 2; $i -lt [Math]::Min($b.Length, 362); $i += 12) {
            $id = $b[$i]
            switch ($id) {
                0x09 { $poh = [BitConverter]::ToUInt32($b, $i + 5) }
                0x05 { $realloc = [BitConverter]::ToUInt32($b, $i + 5) }
                0xC5 { $pending = [BitConverter]::ToUInt32($b, $i + 5) }
                0xC2 { $temp = [int]$b[$i + 5] }
                0xE7 { if ($temp -eq 0) { $temp = [int]$b[$i + 5] } }
            }
        }
    }
}

if ($poh -eq 0 -or $temp -eq 0) {
    $c = Get-PhysicalDisk | Where-Object DeviceId -eq %d | Get-StorageReliabilityCounter
    if ($c) {
        if ($poh -eq 0 -and $c.PowerOnHours) { $poh = [uint64]$c.PowerOnHours }
        if ($temp -eq 0 -and $c.Temperature) { $temp = [int]$c.Temperature }
    }
}

[PSCustomObject]@{
    PowerOnHours = [uint64]$poh
    Temperature  = [int]$temp
    Reallocated  = [uint64]$realloc
    Pending      = [uint64]$pending
} | ConvertTo-Json -Compress
`, driveIndex, driveIndex)

    cmd := exec.Command("powershell.exe", "-NoProfile", "-NonInteractive", "-Command", psScript)
    cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
    out, err := cmd.Output()
    if err != nil {
        return &SmartData{
            VendorProfile:  "SATA Hard Drive",
            AvailableSpare: 100,
        }
    }

    var res struct {
        PowerOnHours uint64 `json:"PowerOnHours"`
        Temperature  int    `json:"Temperature"`
        Reallocated  uint64 `json:"Reallocated"`
        Pending      uint64 `json:"Pending"`
    }

    if err := json.Unmarshal([]byte(strings.TrimSpace(string(out))), &res); err == nil {
        return &SmartData{
            VendorProfile:      "SATA Hard Drive",
            IsNVMe:             false,
            TemperatureC:       res.Temperature,
            AvailableSpare:     100,
            PowerOnHours:       res.PowerOnHours,
            ReallocatedSectors: res.Reallocated,
            PendingSectors:     res.Pending,
        }
    }

    return &SmartData{
        VendorProfile:  "SATA Hard Drive",
        AvailableSpare: 100,
    }
}

// Прямой опрос контроллера NVMe (реализация на базе smartmontools / LibreHardwareMonitor)
func ProbeNVMeDisk(hDrive windows.Handle, driveIndex int, model string) (*SmartData, error) {
    const logPageSize = 512
    queryHeaderSize := uint32(unsafe.Sizeof(STORAGE_PROPERTY_QUERY{}))
    protocolDataSize := uint32(unsafe.Sizeof(STORAGE_PROTOCOL_SPECIFIC_DATA{}))
    totalHeaderSize := queryHeaderSize + protocolDataSize
    bufferSize := totalHeaderSize + logPageSize

    buffer := make([]byte, bufferSize)

    // 1. Заполняем STORAGE_PROPERTY_QUERY
    query := (*STORAGE_PROPERTY_QUERY)(unsafe.Pointer(&buffer[0]))
    query.PropertyId = StorageDeviceProtocolSpecificProperty
    query.QueryType = PropertyStandardQuery

    // 2. Заполняем STORAGE_PROTOCOL_SPECIFIC_DATA
    protoData := (*STORAGE_PROTOCOL_SPECIFIC_DATA)(unsafe.Pointer(&buffer[queryHeaderSize]))
    protoData.ProtocolType = ProtocolTypeNvme
    protoData.DataType = NVMeDataTypeLogPage
    protoData.ProtocolDataRequestValue = NVME_LOG_PAGE_HEALTH_INFO
    protoData.ProtocolDataOffset = protocolDataSize
    protoData.ProtocolDataLength = logPageSize

    var bytesReturned uint32
    err := windows.DeviceIoControl(
        hDrive,
        IOCTL_STORAGE_QUERY_PROPERTY,
        &buffer[0],
        bufferSize,
        &buffer[0],
        bufferSize,
        &bytesReturned,
        nil,
    )

    if err != nil {
        return nil, fmt.Errorf("DeviceIoControl failed: %w", err)
    }

    if bytesReturned < totalHeaderSize+uint32(unsafe.Sizeof(NVME_HEALTH_INFO_LOG{})) {
        return nil, fmt.Errorf("incomplete SMART payload received")
    }

    rawLog := (*NVME_HEALTH_INFO_LOG)(unsafe.Pointer(&buffer[totalHeaderSize]))

    // Декодирование температуры по стандарту NVMe (Kelvin -> Celsius)
    kelvin := binary.LittleEndian.Uint16(rawLog.Temperature[:])
    var tempC int
    if kelvin >= 273 && kelvin <= 373 {
        tempC = int(kelvin) - 273
    }

    // 128-битные счетчики (младшие 64 бита)
    poh := binary.LittleEndian.Uint64(rawLog.PowerOnHours[:8])
    mediaErrors := binary.LittleEndian.Uint64(rawLog.MediaErrors[:8])

    return &SmartData{
        VendorProfile:  "NVMe Direct IOCTL",
        IsNVMe:         true,
        TemperatureC:   tempC,
        WearPercent:    int(rawLog.PercentageUsed),
        AvailableSpare: int(rawLog.AvailableSpare),
        PowerOnHours:   poh,
        MediaErrors:    mediaErrors,
    }, nil
}

func ProbePhysicalStorage(driveIndex int, model string) (*SmartData, error) {
    h, err := openDriveHandle(driveIndex)
    if err == nil {
        defer windows.CloseHandle(h)

        // 1. Пробуем прямой опрос NVMe контроллера
        data, errNvme := ProbeNVMeDisk(h, driveIndex, model)
        if errNvme == nil && data != nil {
            // Если NVMe контроллер не отдал часы, подтягиваем из WMI
            if data.PowerOnHours == 0 {
                if fb := querySataSmartFallback(driveIndex); fb != nil && fb.PowerOnHours > 0 {
                    data.PowerOnHours = fb.PowerOnHours
                }
            }
            return data, nil
        }
    }

    // 2. Для SATA HDD/SSD используем аппаратный WMI ATA SMART
    return querySataSmartFallback(driveIndex), nil
}
