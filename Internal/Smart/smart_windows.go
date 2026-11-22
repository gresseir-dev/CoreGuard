// CoreGuard Enterprise - Copyright (C) 2026 gresseir-dev - Licensed under GNU GPLv3
//go:build windows

package smart

import (
	"encoding/binary"
	"fmt"
	"strings"
	"syscall"
	"unsafe"
)

var (
	kernel32               = syscall.NewLazyDLL("kernel32.dll")
	procDeviceIoControl    = kernel32.NewProc("DeviceIoControl")
	procGetDiskFreeSpaceEx = kernel32.NewProc("GetDiskFreeSpaceExW")
)

const (
	ioctlStorageQueryProperty = 0x002D1400
	smartRcvDriveData         = 0x0007C088

	storageDeviceProperty        = 0
	storageProtocolSpecificProp  = 49
	propertyStandardQuery        = 0
	protocolTypeNvme             = 3
	nvmeDataTypeLogPage          = 2
	nvmeLogPageHealthInfo        = 0x02

	smartReadData       = 0xD0
	smartReadThresholds = 0xD1
	smartCylLow         = 0x4F
	smartCylHigh        = 0xC2
	smartCmd            = 0xB0
)

// DriveHealth — унифицированная структура состояния накопителя для CoreGuard Agent и Hub
type DriveHealth struct {
	Index            int              `json:"index"`
	Device           string           `json:"device"`
	Model            string           `json:"model"`
	Serial           string           `json:"serial"`
	Protocol         string           `json:"protocol"` // "NVMe" или "SATA"
	TemperatureC     int              `json:"temperature_c"`
	HealthPercent    int              `json:"health_percent"` // 0..100%
	PowerOnHours     uint64           `json:"power_on_hours"`
	DataWrittenGB    float64          `json:"data_written_gb"`
	CriticalWarning  bool             `json:"critical_warning"`
	Status           string           `json:"status"` // "OK", "WARNING", "CRITICAL"
	Warnings         []string         `json:"warnings,omitempty"`
	Attributes       []SmartAttribute `json:"attributes,omitempty"`
}

type SmartAttribute struct {
	ID        uint8  `json:"id"`
	Name      string `json:"name"`
	Current   uint8  `json:"current"`
	Worst     uint8  `json:"worst"`
	Threshold uint8  `json:"threshold"`
	Raw       uint64 `json:"raw"`
	Critical  bool   `json:"critical"`
}

// ScanAllDrives опрашивает физические диски 0..15 через прямые вызовы Windows IOCTL
func ScanAllDrives() []DriveHealth {
	var results []DriveHealth
	for i := 0; i < 16; i++ {
		devPath := fmt.Sprintf(`\\.\PhysicalDrive%d`, i)
		h, err := openDriveHandle(devPath)
		if err != nil {
			continue
		}

		model, serial, busType := queryDeviceDescriptor(h)
		drive := DriveHealth{
			Index:         i,
			Device:        devPath,
			Model:         model,
			Serial:        serial,
			HealthPercent: 100,
			Status:        "OK",
		}

		// BusType 17 = BusTypeNvme в Windows STORAGE_BUS_TYPE
		if busType == 17 || strings.Contains(strings.ToUpper(model), "NVME") {
			drive.Protocol = "NVMe"
			if err := readNvmeHealth(h, &drive); err != nil {
				drive.Warnings = append(drive.Warnings, "NVMe SMART недоступен: "+err.Error())
			}
		} else {
			drive.Protocol = "SATA"
			if err := readSataSmart(h, &drive); err != nil {
				drive.Warnings = append(drive.Warnings, "SATA SMART недоступен: "+err.Error())
			}
		}

		evaluateHealth(&drive)
		syscall.CloseHandle(h)
		results = append(results, drive)
	}
	return results
}

func openDriveHandle(path string) (syscall.Handle, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return syscall.InvalidHandle, err
	}
	return syscall.CreateFile(
		p,
		syscall.GENERIC_READ|syscall.GENERIC_WRITE,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE,
		nil,
		syscall.OPEN_EXISTING,
		0,
		0,
	)
}

func deviceIoControl(h syscall.Handle, code uint32, inBuf []byte, outBuf []byte) (uint32, error) {
	var bytesReturned uint32
	var inPtr, outPtr uintptr
	var inLen, outLen uint32

	if len(inBuf) > 0 {
		inPtr = uintptr(unsafe.Pointer(&inBuf[0]))
		inLen = uint32(len(inBuf))
	}
	if len(outBuf) > 0 {
		outPtr = uintptr(unsafe.Pointer(&outBuf[0]))
		outLen = uint32(len(outBuf))
	}

	r1, _, err := procDeviceIoControl.Call(
		uintptr(h),
		uintptr(code),
		inPtr,
		uintptr(inLen),
		outPtr,
		uintptr(outLen),
		uintptr(unsafe.Pointer(&bytesReturned)),
		0,
	)
	if r1 == 0 {
		return 0, err
	}
	return bytesReturned, nil
}

// queryDeviceDescriptor извлекает модель, серийник и тип шины (SATA / NVMe / USB)
func queryDeviceDescriptor(h syscall.Handle) (model, serial string, busType byte) {
	inBuf := make([]byte, 12)
	binary.LittleEndian.PutUint32(inBuf[0:4], storageDeviceProperty)
	binary.LittleEndian.PutUint32(inBuf[4:8], propertyStandardQuery)

	outBuf := make([]byte, 1024)
	n, err := deviceIoControl(h, ioctlStorageQueryProperty, inBuf, outBuf)
	if err != nil || n < 36 {
		return "Unknown Drive", "", 0
	}

	productOffset := binary.LittleEndian.Uint32(outBuf[16:20])
	serialOffset := binary.LittleEndian.Uint32(outBuf[24:28])
	busType = outBuf[28]

	model = readCString(outBuf, int(productOffset))
	serial = readCString(outBuf, int(serialOffset))
	if model == "" {
		model = "Generic Storage Device"
	}
	return strings.TrimSpace(model), strings.TrimSpace(serial), busType
}

// readNvmeHealth запрашивает 512-байтную страницу лога здоровья NVMe (0x02)
func readNvmeHealth(h syscall.Handle, d *DriveHealth) error {
	// STORAGE_PROPERTY_QUERY (8 байт) + STORAGE_PROTOCOL_SPECIFIC_DATA (40 байт) + 512 байт буфер
	const headerSize = 48
	const logSize = 512
	buf := make([]byte, headerSize+logSize)

	binary.LittleEndian.PutUint32(buf[0:4], storageProtocolSpecificProp)
	binary.LittleEndian.PutUint32(buf[4:8], propertyStandardQuery)

	// STORAGE_PROTOCOL_SPECIFIC_DATA со смещения 8
	binary.LittleEndian.PutUint32(buf[8:12], protocolTypeNvme)
	binary.LittleEndian.PutUint32(buf[12:16], nvmeDataTypeLogPage)
	binary.LittleEndian.PutUint32(buf[16:20], nvmeLogPageHealthInfo)
	binary.LittleEndian.PutUint32(buf[20:24], 0)          // ProtocolDataRequestSubValue
	binary.LittleEndian.PutUint32(buf[24:28], 40)         // ProtocolDataOffset (от начала STORAGE_PROTOCOL_SPECIFIC_DATA)
	binary.LittleEndian.PutUint32(buf[28:32], logSize)    // ProtocolDataLength

	n, err := deviceIoControl(h, ioctlStorageQueryProperty, buf, buf)
	if err != nil || n < headerSize+logSize {
		return fmt.Errorf("IOCTL NVMe failed: %v", err)
	}

	logData := buf[headerSize : headerSize+logSize]

	critWarn := logData[0]
	d.CriticalWarning = critWarn != 0
	if critWarn&0x01 != 0 {
		d.Warnings = append(d.Warnings, "Резервное пространство NVMe упало ниже порога")
	}
	if critWarn&0x02 != 0 {
		d.Warnings = append(d.Warnings, "Критическая температура NVMe")
	}
	if critWarn&0x04 != 0 {
		d.Warnings = append(d.Warnings, "Деградация надёжности ячеек памяти NVMe")
	}
	if critWarn&0x08 != 0 {
		d.Warnings = append(d.Warnings, "Накопитель перешёл в режим только для чтения (Read-Only)")
	}

	// Температура (байты 1..2 в Кельвинах)
	tempK := int(binary.LittleEndian.Uint16(logData[1:3]))
	if tempK > 200 && tempK < 450 {
		d.TemperatureC = tempK - 273
	}

	// Percentage Used (байт 5: 0% у нового, 100%+ при исчерпании ресурса)
	usedPct := int(logData[5])
	if usedPct > 100 {
		usedPct = 100
	}
	d.HealthPercent = 100 - usedPct

	// Data Units Written (байты 48..63, 1 единица = 1000 * 512 байт = 512 000 байт)
	unitsWritten := binary.LittleEndian.Uint64(logData[48:56])
	d.DataWrittenGB = float64(unitsWritten) * 512000.0 / (1024 * 1024 * 1024)

	    // Power Cycles (байты 112..127), Power On Hours (байты 128..143), Unsafe Shutdowns (байты 144..159), Media Errors (байты 160..175)
    powerCycles := binary.LittleEndian.Uint64(logData[112:120])
    d.PowerOnHours = binary.LittleEndian.Uint64(logData[128:136])
    unsafeShutdowns := binary.LittleEndian.Uint64(logData[144:152])
    mediaErrors := binary.LittleEndian.Uint64(logData[160:168])

    d.Attributes = []SmartAttribute{
        {ID: 1, Name: "Critical Warning", Raw: uint64(critWarn), Critical: critWarn != 0},
        {ID: 2, Name: "Composite Temperature (C)", Raw: uint64(d.TemperatureC)},
        {ID: 3, Name: "Available Spare (%)", Raw: uint64(logData[3])},
        {ID: 5, Name: "Percentage Used (%)", Raw: uint64(usedPct)},
        {ID: 9, Name: "Power On Hours", Raw: d.PowerOnHours},
        {ID: 12, Name: "Power Cycles", Raw: powerCycles},
        {ID: 13, Name: "Unsafe Shutdowns", Raw: unsafeShutdowns},
        {ID: 14, Name: "Media Integrity Errors", Raw: mediaErrors, Critical: false},
    }
	return nil
}

// readSataSmart читает 30 стандартных 12-байтных атрибутов ATA SMART через SMART_RCV_DRIVE_DATA
func readSataSmart(h syscall.Handle, d *DriveHealth) error {
	attrRaw, err := sendAtaSmartCmd(h, smartReadData)
	if err != nil {
		return err
	}
	threshRaw, _ := sendAtaSmartCmd(h, smartReadThresholds)

	thresholds := make(map[uint8]uint8)
	if len(threshRaw) >= 362 {
		for i := 0; i < 30; i++ {
			off := 2 + i*12
			id := threshRaw[off]
			if id != 0 {
				thresholds[id] = threshRaw[off+1]
			}
		}
	}

	// Разбор 30 атрибутов (каждый по 12 байт, начиная со смещения 2)
	for i := 0; i < 30; i++ {
		off := 2 + i*12
		if off+12 > len(attrRaw) {
			break
		}
		id := attrRaw[off]
		if id == 0 {
			continue
		}
		cur := attrRaw[off+3]
		worst := attrRaw[off+4]

		// 6-байтное Raw-значение (little-endian)
		var rawVal uint64
		for b := 0; b < 6; b++ {
			rawVal |= uint64(attrRaw[off+5+b]) << (8 * b)
		}

		thresh := thresholds[id]
		name := ataAttrName(id)
		isCrit := false

		// Интерпретация ключевых атрибутов (логика smartmontools)
		switch id {
		case 0x05, 0xC4, 0xC5, 0xC6: // Reallocated / Pending / Uncorrectable sectors
			if rawVal > 0 {
				isCrit = true
				d.Warnings = append(d.Warnings, fmt.Sprintf("%s (ID %d): обнаружено %d проблемных секторов", name, id, rawVal))
			}
		case 0x09: // Power-On Hours
			d.PowerOnHours = rawVal & 0xFFFFFFFF
		case 0xBE, 0xC2: // Airflow / Drive Temperature
			temp := int(rawVal & 0xFF)
			if temp > 0 && temp < 120 {
				d.TemperatureC = temp
			}
		case 0xE7, 0xCA, 0xE9: // SSD Life Left / Media Wearout Indicator
			if cur > 0 && cur <= 100 {
				d.HealthPercent = int(cur)
			}
		case 0xF1: // Total LBAs Written (обычно в 32MB или 512B блоках)
			d.DataWrittenGB = float64(rawVal) * 512.0 / (1024 * 1024 * 1024)
		}

		if thresh > 0 && cur <= thresh {
			isCrit = true
			d.Warnings = append(d.Warnings, fmt.Sprintf("Атрибут %s (%d) упал ниже порога (%d <= %d)", name, id, cur, thresh))
		}

		d.Attributes = append(d.Attributes, SmartAttribute{
			ID:        id,
			Name:      name,
			Current:   cur,
			Worst:     worst,
			Threshold: thresh,
			Raw:       rawVal,
			Critical:  isCrit,
		})
	}
	return nil
}

func sendAtaSmartCmd(h syscall.Handle, subCmd byte) ([]byte, error) {
	// SENDCMDINPARAMS (32 байта)
	inBuf := make([]byte, 32)
	binary.LittleEndian.PutUint32(inBuf[0:4], 512) // cBufferSize
	inBuf[4] = subCmd                              // bFeaturesReg
	inBuf[5] = 1                                   // bSectorCountReg
	inBuf[6] = 1                                   // bSectorNumberReg
	inBuf[7] = smartCylLow                         // bCylLowReg (0x4F)
	inBuf[8] = smartCylHigh                        // bCylHighReg (0xC2)
	inBuf[9] = 0xA0                                // bDriveHeadReg
	inBuf[10] = smartCmd                           // bCommandReg (0xB0)

	// SENDCMDOUTPARAMS (16 байт заголовка + 512 байт данных)
	outBuf := make([]byte, 16+512)
	n, err := deviceIoControl(h, smartRcvDriveData, inBuf, outBuf)
	if err != nil || n < 16+512 {
		return nil, fmt.Errorf("ATA SMART IOCTL failed: %v", err)
	}
	return outBuf[16 : 16+512], nil
}

func evaluateHealth(d *DriveHealth) {
	if d.TemperatureC >= 65 {
		d.Warnings = append(d.Warnings, fmt.Sprintf("Перегрев накопителя: %d°C", d.TemperatureC))
		d.Status = "CRITICAL"
	} else if d.TemperatureC >= 52 && d.Status == "OK" {
		d.Warnings = append(d.Warnings, fmt.Sprintf("Повышенная температура: %d°C", d.TemperatureC))
		d.Status = "WARNING"
	}

	if d.CriticalWarning || d.HealthPercent <= 10 {
		d.Status = "CRITICAL"
	} else if d.HealthPercent <= 25 && d.Status == "OK" {
		d.Status = "WARNING"
	}

	for _, a := range d.Attributes {
		if a.Critical {
			d.Status = "CRITICAL"
		}
	}
}

func ataAttrName(id uint8) string {
	names := map[uint8]string{
		0x01: "Raw Read Error Rate",
		0x03: "Spin-Up Time",
		0x04: "Start/Stop Count",
		0x05: "Reallocated Sectors Count",
		0x07: "Seek Error Rate",
		0x09: "Power-On Hours",
		0x0A: "Spin Retry Count",
		0x0C: "Power Cycle Count",
		0xB8: "End-to-End Error",
		0xBB: "Reported Uncorrectable Errors",
		0xBC: "Command Timeout",
		0xBE: "Airflow Temperature",
		0xC0: "Power-Off Retract Count",
		0xC1: "Load/Unload Cycle Count",
		0xC2: "Temperature Celsius",
		0xC4: "Reallocation Event Count",
		0xC5: "Current Pending Sector Count",
		0xC6: "Offline Uncorrectable Sector Count",
		0xC7: "UltraDMA CRC Error Count",
		0xCA: "Percent Lifetime Remaining",
		0xE7: "SSD Life Left",
		0xE9: "Media Wearout Indicator",
		0xF1: "Total LBAs Written",
		0xF2: "Total LBAs Read",
	}
	if n, ok := names[id]; ok {
		return n
	}
	return fmt.Sprintf("Attribute_0x%02X", id)
}

func readCString(buf []byte, offset int) string {
	if offset <= 0 || offset >= len(buf) {
		return ""
	}
	end := offset
	for end < len(buf) && buf[end] != 0 {
		end++
	}
	return string(buf[offset:end])
}