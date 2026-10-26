package main

import (
    "encoding/binary"
    "fmt"
    "strings"
    "syscall"
    "unsafe"

    "golang.org/x/sys/windows"
)

// Нормализованные метрики любого накопителя независимо от вендора
type DriveTelemetry struct {
    Model          string
    Vendor         string
    Interface      string
    PowerOnHours   uint64
    Temperature    int
    WearPercent    int
    AvailableSpare int
    MediaErrors    uint64
    CriticalFlags  string
}

// Интерфейс для профилей вендоров
type VendorProfile interface {
    Name() string
    Match(model string) bool
    DecodeNVMe(raw []byte) (*DriveTelemetry, error)
    DecodeATA(raw []byte) (*DriveTelemetry, error)
}

// ─────────────────────────────────────────────────────────────
// 1. ПРОФИЛЬ: INTEL / SOLIDIGM (Intel 660p, 760p, Optane, DC)
// ─────────────────────────────────────────────────────────────
type IntelNVMeProfile struct{}

func (p *IntelNVMeProfile) Name() string { return "Intel/Solidigm Storage Engine" }

func (p *IntelNVMeProfile) Match(model string) bool {
    m := strings.ToUpper(model)
    return strings.Contains(m, "INTEL") || 
           strings.Contains(m, "SSDPEK") || 
           strings.Contains(m, "SOLIDIGM")
}

func (p *IntelNVMeProfile) DecodeNVMe(raw []byte) (*DriveTelemetry, error) {
    if len(raw) < 512 {
        return nil, fmt.Errorf("буфер лога Intel мал (%d байт)", len(raw))
    }

    t := &DriveTelemetry{
        Vendor:    "Intel / Solidigm",
        Interface: "NVMe",
    }

    // 1. Температура: Intel использует Composite (байты 1-2)
    kelvin := binary.LittleEndian.Uint16(raw[1:3])
    if kelvin > 273 && kelvin < 390 {
        t.Temperature = int(kelvin) - 273
    }

    // 2. Резерв и износ
    t.AvailableSpare = int(raw[3])
    t.WearPercent = int(raw[5])
    t.CriticalFlags = fmt.Sprintf("0x%02X", raw[0])

    // 3. Power On Hours (Особенность Intel):
    // В прошивках Intel SSDPEKNW (660p) часы пишутся строго с 128 по 135 байт
    poh := binary.LittleEndian.Uint64(raw[128:136])

    // Защита от альтернативных прошивок Intel Enterprise (где лог дублируется в минутах)
    if poh > 1000000 {
        poh = poh / 60
    }
    t.PowerOnHours = poh

    // 4. Ошибки носителя (байты 176-183)
    t.MediaErrors = binary.LittleEndian.Uint64(raw[176:184])

    return t, nil
}

func (p *IntelNVMeProfile) DecodeATA(raw []byte) (*DriveTelemetry, error) {
    return parseGenericATA(raw, "Intel")
}

// ─────────────────────────────────────────────────────────────
// 2. ПРОФИЛЬ: SAMSUNG (970 EVO/PRO, 980, 990 PRO, PM9A1)
// ─────────────────────────────────────────────────────────────
type SamsungNVMeProfile struct{}

func (p *SamsungNVMeProfile) Name() string { return "Samsung V-NAND Engine" }

func (p *SamsungNVMeProfile) Match(model string) bool {
    m := strings.ToUpper(model)
    return strings.Contains(m, "SAMSUNG") || strings.Contains(m, "MZVL") || strings.Contains(m, "PM9")
}

func (p *SamsungNVMeProfile) DecodeNVMe(raw []byte) (*DriveTelemetry, error) {
    t := &DriveTelemetry{
        Vendor:    "Samsung Electronics",
        Interface: "NVMe",
    }

    // У Samsung датчик Sensor 1 (чипы памяти) часто точнее Composite (горячий ASIC-чип)
    sensor1 := binary.LittleEndian.Uint16(raw[200:202])
    if sensor1 > 273 && sensor1 < 380 {
        t.Temperature = int(sensor1) - 273
    } else {
        kelvin := binary.LittleEndian.Uint16(raw[1:3])
        if kelvin > 273 {
            t.Temperature = int(kelvin) - 273
        }
    }

    t.AvailableSpare = int(raw[3])
    t.WearPercent = int(raw[5])
    t.PowerOnHours = binary.LittleEndian.Uint64(raw[128:136])
    t.MediaErrors = binary.LittleEndian.Uint64(raw[176:184])
    t.CriticalFlags = fmt.Sprintf("0x%02X", raw[0])

    return t, nil
}

func (p *SamsungNVMeProfile) DecodeATA(raw []byte) (*DriveTelemetry, error) {
    return parseGenericATA(raw, "Samsung")
}

// ─────────────────────────────────────────────────────────────
// 3. ПРОФИЛЬ: WESTERN DIGITAL / SANDISK / KIOXIA
// ─────────────────────────────────────────────────────────────
type WdSanDiskProfile struct{}

func (p *WdSanDiskProfile) Name() string { return "Western Digital / SanDisk Engine" }

func (p *WdSanDiskProfile) Match(model string) bool {
    m := strings.ToUpper(model)
    return strings.Contains(m, "WDC") || strings.Contains(m, "WD ") || 
           strings.Contains(m, "SANDISK") || strings.Contains(m, "KIOXIA")
}

func (p *WdSanDiskProfile) DecodeNVMe(raw []byte) (*DriveTelemetry, error) {
    return (&StandardNVMeProfile{}).DecodeNVMe(raw)
}

func (p *WdSanDiskProfile) DecodeATA(raw []byte) (*DriveTelemetry, error) {
    // WD часто использует атрибут 190 (Airflow Temp) вместо 194
    return parseGenericATA(raw, "Western Digital")
}

// ─────────────────────────────────────────────────────────────
// 4. СТАНДАРТНЫЙ БАЗОВЫЙ ПРОФИЛЬ (JEDEC / NVM Express Fallback)
// ─────────────────────────────────────────────────────────────
type StandardNVMeProfile struct{}

func (p *StandardNVMeProfile) Name() string { return "Standard NVMe Specification" }
func (p *StandardNVMeProfile) Match(model string) bool { return true } // Fallback

func (p *StandardNVMeProfile) DecodeNVMe(raw []byte) (*DriveTelemetry, error) {
    if len(raw) < 512 {
        return nil, fmt.Errorf("недостаточный размер NVMe лога")
    }
    kelvin := binary.LittleEndian.Uint16(raw[1:3])
    temp := 0
    if kelvin > 273 && kelvin < 390 {
        temp = int(kelvin) - 273
    }

    return &DriveTelemetry{
        Vendor:         "Standard NVMe",
        Interface:      "NVMe",
        Temperature:    temp,
        AvailableSpare: int(raw[3]),
        WearPercent:    int(raw[5]),
        PowerOnHours:   binary.LittleEndian.Uint64(raw[128:136]),
        MediaErrors:    binary.LittleEndian.Uint64(raw[176:184]),
        CriticalFlags:  fmt.Sprintf("0x%02X", raw[0]),
    }, nil
}

func (p *StandardNVMeProfile) DecodeATA(raw []byte) (*DriveTelemetry, error) {
    return parseGenericATA(raw, "Standard ATA")
}

// Парсер ATA SMART таблицы (SATA SSD / HDD)
func parseGenericATA(raw []byte, vendorName string) (*DriveTelemetry, error) {
    if len(raw) < 512 {
        return nil, fmt.Errorf("недостаточно данных ATA SMART")
    }

    t := &DriveTelemetry{
        Vendor:         vendorName,
        Interface:      "SATA",
        WearPercent:    -1,
        AvailableSpare: 100,
    }

    for idx := 2; idx < 362; idx += 12 {
        attrID := raw[idx]
        if attrID == 0 {
            continue
        }
        rawVal := binary.LittleEndian.Uint32(raw[idx+5 : idx+9])

        switch attrID {
        case 5: // Reallocated Sectors
            t.MediaErrors += uint64(rawVal)
        case 9: // Power-On Hours
            hours := uint64(rawVal)
            if hours > 400000 { // Минуты -> Часы
                hours = hours / 60
            }
            t.PowerOnHours = hours
        case 194, 190: // Temperature
            temp := int(raw[idx+5])
            if temp >= 10 && temp <= 100 {
                t.Temperature = temp
            }
        case 231, 232: // Life Left
            norm := int(raw[idx+3])
            if norm > 0 && norm <= 100 {
                t.WearPercent = 100 - norm
            }
        }
    }
    return t, nil
}

// ─────────────────────────────────────────────────────────────
// 5. РЕЕСТР ПРОФИЛЕЙ И МУЛЬТИ-ТРАНСПОРТНЫЙ ДВИЖОК
// ─────────────────────────────────────────────────────────────
type SmartEngine struct {
    profiles []VendorProfile
}

func NewSmartEngine() *SmartEngine {
    return &SmartEngine{
        profiles: []VendorProfile{
            &IntelNVMeProfile{},
            &SamsungNVMeProfile{},
            &WdSanDiskProfile{},
            &StandardNVMeProfile{}, // Обязательно последним как fallback
        },
    }
}

func (e *SmartEngine) ResolveProfile(model string) VendorProfile {
    for _, p := range e.profiles {
        if p.Match(model) {
            return p
        }
    }
    return &StandardNVMeProfile{}
}

// Чтение лога NVMe через два альтернативных транспорта
func (e *SmartEngine) ReadRawNVMe(handle windows.Handle) ([]byte, error) {
    // Канал 1: Прямой запрос через Windows Storage Protocol
    buf := make([]byte, 4096)
    *(*uint32)(unsafe.Pointer(&buf[0])) = 49 // StorageDeviceProtocolSpecificProperty
    *(*uint32)(unsafe.Pointer(&buf[4])) = 0  // PropertyStandardQuery

    *(*uint32)(unsafe.Pointer(&buf[12])) = 1    // ProtocolTypeNvme
    *(*uint32)(unsafe.Pointer(&buf[16])) = 1    // NVMeDataTypeLogPage
    *(*uint32)(unsafe.Pointer(&buf[20])) = 0x02 // NVME_LOG_PAGE_HEALTH_INFO
    *(*uint32)(unsafe.Pointer(&buf[28])) = 48   // Offset
    *(*uint32)(unsafe.Pointer(&buf[32])) = 512  // Length

    var bytesRet uint32
    err := windows.DeviceIoControl(
        handle,
        0x002D1400, // IOCTL_STORAGE_QUERY_PROPERTY
        &buf[0], uint32(len(buf)),
        &buf[0], uint32(len(buf)),
        &bytesRet, nil,
    )

    if err == nil && bytesRet >= 200 {
        offset := int(*(*uint32)(unsafe.Pointer(&buf[24])))
        if offset <= 0 || offset+512 > len(buf) {
            offset = 56
        }
        if offset+512 <= int(bytesRet) {
            return buf[offset : offset+512], nil
        }
    }

    // Канал 2: Если stornvme заблокирован, пробуем прямое чтение со смещением 48
    if bytesRet >= 560 {
        return buf[48 : 48+512], nil
    }

    return nil, fmt.Errorf("контроллер NVMe не ответил на стандартные запросы")
}
