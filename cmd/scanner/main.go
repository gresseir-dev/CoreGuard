// CoreGuard Fleet Scanner - Copyright (C) 2026 gresseir-dev - Licensed under GNU GPLv3
package main

import (
    "encoding/json"
    "flag"
    "fmt"
    "net"
    "os"
    "os/user"
    "path/filepath"
    "strings"
    "time"

    "golang.org/x/sys/windows"
)

type FleetHostEntry struct {
    Hostname      string `json:"hostname"`
    User          string `json:"user"`
    IP            string `json:"ip"`
    AgentPort     int    `json:"agent_port"`
    Group         string `json:"group,omitempty"`
    AgentRunning  bool   `json:"agent_running"`
    LastScannedAt string `json:"last_scanned_at"`
}

type FleetInventoryFile struct {
    UpdatedAt string           `json:"updated_at"`
    Hosts     []FleetHostEntry `json:"hosts"`
}

func getScannerDir() string {
    if exe, err := os.Executable(); err == nil {
        return filepath.Dir(exe)
    }
    return "."
}

func getPrimaryIPv4() string {
    // ОС сама выбирает локальный IP активного сетевого маршрута без отправки трафика
    if conn, err := net.DialTimeout("udp4", "8.8.8.8:80", 1500*time.Millisecond); err == nil {
        defer conn.Close()
        if localAddr, ok := conn.LocalAddr().(*net.UDPAddr); ok && localAddr.IP != nil {
            ip4 := localAddr.IP.To4()
            if ip4 != nil && !ip4.IsLoopback() {
                return ip4.String()
            }
        }
    }

    ifaces, err := net.Interfaces()
    if err == nil {
        for _, iface := range ifaces {
            if (iface.Flags&net.FlagUp) == 0 || (iface.Flags&net.FlagLoopback) != 0 {
                continue
            }
            addrs, err := iface.Addrs()
            if err != nil {
                continue
            }
            for _, addr := range addrs {
                var ip net.IP
                switch v := addr.(type) {
                case *net.IPNet:
                    ip = v.IP
                case *net.IPAddr:
                    ip = v.IP
                }
                if ip == nil {
                    continue
                }
                ip4 := ip.To4()
                if ip4 == nil || ip4.IsLoopback() || ip4.IsLinkLocalUnicast() {
                    continue
                }
                return ip4.String()
            }
        }
    }

    return "127.0.0.1"
}

func getCurrentUserName() string {
    if u, err := user.Current(); err == nil && strings.TrimSpace(u.Username) != "" {
        return strings.TrimSpace(u.Username)
    }
    domain := strings.TrimSpace(os.Getenv("USERDOMAIN"))
    name := strings.TrimSpace(os.Getenv("USERNAME"))
    if domain != "" && name != "" && !strings.Contains(name, "\\") {
        return domain + "\\" + name
    }
    if name != "" {
        return name
    }
    return "unknown"
}

func isPortOpen(ip string, port int) bool {
    if ip == "" || port <= 0 {
        return false
    }
    target := net.JoinHostPort(ip, fmt.Sprintf("%d", port))
    conn, err := net.DialTimeout("tcp", target, 600*time.Millisecond)
    if err != nil {
        return false
    }
    _ = conn.Close()
    return true
}

func acquireFileLock(lockPath string) (*os.File, error) {
    for attempt := 0; attempt < 40; attempt++ {
        f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0666)
        if err == nil {
            var ov windows.Overlapped
            errLock := windows.LockFileEx(
                windows.Handle(f.Fd()),
                windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
                0, 1, 0, &ov,
            )
            if errLock == nil {
                return f, nil
            }
            _ = f.Close()
        }
        time.Sleep(100 * time.Millisecond)
    }
    return nil, fmt.Errorf("не удалось получить блокировку файла %s", lockPath)
}

func releaseFileLock(f *os.File, lockPath string) {
    if f != nil {
        var ov windows.Overlapped
        _ = windows.UnlockFileEx(windows.Handle(f.Fd()), 0, 1, 0, &ov)
        _ = f.Close()
    }
    _ = os.Remove(lockPath)
}

func waitForExitIfRequested(noPause bool) {
    if noPause {
        return
    }
    fmt.Print("\nНажмите Enter для выхода...")
    var buf [1]byte
    _, _ = os.Stdin.Read(buf[:])
}

func main() {
    outFileName := flag.String("out", "fleet_inventory.json", "Имя файла инвентаризации в каталоге сканера")
    agentPort := flag.Int("port", 48123, "Порт агента CoreGuard")
    groupName := flag.String("group", "", "Необязательная группа узлов для импорта в Хаб")
    noPause := flag.Bool("no-pause", false, "Не ждать нажатия Enter после завершения")
    flag.Parse()

    hostname, err := os.Hostname()
    if err != nil || strings.TrimSpace(hostname) == "" {
        hostname = "UNKNOWN-PC"
    } else {
        hostname = strings.TrimSpace(hostname)
    }

    currentUser := getCurrentUserName()
    primaryIP := getPrimaryIPv4()
    running := isPortOpen("127.0.0.1", *agentPort) || isPortOpen(primaryIP, *agentPort)
    nowStr := time.Now().Format("2006-01-02 15:04:05")

    invPath := filepath.Join(getScannerDir(), *outFileName)
    lockPath := invPath + ".lock"

    lockF, err := acquireFileLock(lockPath)
    if err != nil {
        fmt.Printf("[ОШИБКА] %v\n", err)
        waitForExitIfRequested(*noPause)
        os.Exit(1)
    }
    defer releaseFileLock(lockF, lockPath)

    var inv FleetInventoryFile
    if raw, readErr := os.ReadFile(invPath); readErr == nil && len(raw) > 0 {
        if jsonErr := json.Unmarshal(raw, &inv); jsonErr != nil {
            // Если файл пуст или повреждён, начинаем чистый список
            inv = FleetInventoryFile{}
        }
    }

    newEntry := FleetHostEntry{
        Hostname:      hostname,
        User:          currentUser,
        IP:            primaryIP,
        AgentPort:     *agentPort,
        Group:         strings.TrimSpace(*groupName),
        AgentRunning:  running,
        LastScannedAt: nowStr,
    }

    updated := false
    for i := range inv.Hosts {
        if strings.EqualFold(inv.Hosts[i].Hostname, hostname) {
            if newEntry.Group == "" {
                newEntry.Group = inv.Hosts[i].Group
            }
            inv.Hosts[i] = newEntry
            updated = true
            break
        }
    }
    if !updated {
        inv.Hosts = append(inv.Hosts, newEntry)
    }
    inv.UpdatedAt = nowStr

    outBytes, err := json.MarshalIndent(inv, "", "  ")
    if err != nil {
        fmt.Printf("[ОШИБКА] Формирование JSON: %v\n", err)
        waitForExitIfRequested(*noPause)
        os.Exit(1)
    }

    tmpPath := invPath + ".tmp"
    if err := os.WriteFile(tmpPath, outBytes, 0644); err != nil {
        fmt.Printf("[ОШИБКА] Запись файла: %v\n", err)
        waitForExitIfRequested(*noPause)
        os.Exit(1)
    }
    if err := os.Rename(tmpPath, invPath); err != nil {
        _ = os.Remove(tmpPath)
        fmt.Printf("[ОШИБКА] Обновление %s: %v\n", invPath, err)
        waitForExitIfRequested(*noPause)
        os.Exit(1)
    }

    actionLabel := "Добавлен"
    if updated {
        actionLabel = "Обновлён"
    }

    fmt.Printf("[%s] %s узел: %s | Пользователь: %s | IP: %s | Агент (%d): %v\n",
        nowStr, actionLabel, hostname, currentUser, primaryIP, *agentPort, running)
    fmt.Printf("Файл сохранён: %s (всего узлов: %d)\n", invPath, len(inv.Hosts))

    // Подавляем предупреждение о неиспользуемом unsafe при кросс-компиляции/сборке
    waitForExitIfRequested(*noPause)
}