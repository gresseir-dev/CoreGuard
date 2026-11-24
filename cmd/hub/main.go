// CoreGuard Enterprise - Copyright (C) 2026 gresseir-dev - Licensed under GNU GPLv3
package main

import (
    _ "embed"
    "bytes"
    "context"
    "crypto/sha256"
    "encoding/base64"
    "encoding/hex"
    "encoding/json"
    "fmt"
    "io"
    "log"
    "net"
    "net/http"
    "os"
    "path/filepath"
    "strings"
    "sync"
    "time"

    "golang.org/x/sys/windows/svc"
)

//go:embed dashboard.html
var hubDashboardHTML []byte

type TelegramConfig struct {
    Enabled         bool   `json:"enabled"`
    BotToken        string `json:"bot_token"`
    ChatID          string `json:"chat_id"`
    NotifyOnWarn    bool   `json:"notify_on_warn"`
    NotifyOnCrit    bool   `json:"notify_on_crit"`
    NotifyOnOffline bool   `json:"notify_on_offline"`
}

type NodeConfig struct {
    ID             string `json:"id"`
    Name           string `json:"name"`
    Group          string `json:"group"`
    Host           string `json:"host"`
    AuthUser       string `json:"auth_user"`
    AuthPass       string `json:"auth_pass"`
    AlertOnOffline bool   `json:"alert_on_offline"`
}

type NodeStatus struct {
    Config         NodeConfig `json:"config"`
    Online         bool       `json:"online"`
    LastSeen       string     `json:"last_seen"`
    Overall        string     `json:"overall"`
    CpuTemp        int        `json:"cpu_temp"`
    MbTemp         int        `json:"mb_temp"`
    FreeGB         float64    `json:"free_gb"`
    FreePct        float64    `json:"free_pct"`
    DrivesTotal    int        `json:"drives_total"`
    DrivesCrit     int        `json:"drives_crit"`
	DriveAlerts    []string   `json:"drive_alerts,omitempty"`
    HasUnreadAlert bool       `json:"has_unread_alert"`
    AgentVersion   string     `json:"agent_version,omitempty"`
}

type MspBranding struct {
    CompanyName     string `json:"company_name"`
    SupportPhone    string `json:"support_phone"`
    SupportTelegram string `json:"support_telegram"`
    SupportEmail    string `json:"support_email"`
}

type HubSettings struct {
    Language           string         `json:"language"`
    ClientOrganization string         `json:"client_organization"`
    MspBranding        MspBranding    `json:"msp_branding"`
    Nodes              []NodeConfig   `json:"nodes"`
    Telegram           TelegramConfig `json:"telegram"`
}

type HubEvent struct {
    ID           string `json:"id"`
    Timestamp    string `json:"timestamp"`
    NodeID       string `json:"node_id"`
    NodeName     string `json:"node_name"`
    Group        string `json:"group"`
    Severity     string `json:"severity"`
    Message      string `json:"message"`
    Resolved     bool   `json:"resolved"`
    Acknowledged bool   `json:"acknowledged"`
}

type HubManager struct {
    mu                 sync.RWMutex
    language           string
    clientOrganization string
    mspBranding        MspBranding
    nodes              []NodeConfig
    telegram           TelegramConfig
    states       map[string]*NodeStatus
    lastSent     map[string]string
    events       []HubEvent
    client       *http.Client
    settingsPath string
    eventsPath   string
}

func NewHubManager(settingsPath, eventsPath string) *HubManager {
    return &HubManager{
        language:           "ru",
        clientOrganization: "Моя Организация",
        mspBranding: MspBranding{
            CompanyName:  "Служба ИТ-поддержки",
            SupportPhone: "+7 (000) 000-00-00",
        },
        nodes:        make([]NodeConfig, 0),
        states:       make(map[string]*NodeStatus),
        lastSent:     make(map[string]string),
        events:       make([]HubEvent, 0),
        client:       &http.Client{Timeout: 4 * time.Second},
        settingsPath: settingsPath,
        eventsPath:   eventsPath,
        telegram: TelegramConfig{
            NotifyOnCrit:    true,
            NotifyOnOffline: true,
        },
    }
}

func (h *HubManager) buildDefaultLocalNode() NodeConfig {
    port := 48123
    user := "admin"
    pass := ""

    // Ищем config.json локального агента рядом с hub_config.json или рядом с coreguard_hub.exe
    candidates := []string{
        filepath.Join(filepath.Dir(h.settingsPath), "config.json"),
        "config.json",
    }
    if exe, err := os.Executable(); err == nil {
        candidates = append([]string{filepath.Join(filepath.Dir(exe), "config.json")}, candidates...)
    }

    for _, cfgPath := range candidates {
        raw, err := os.ReadFile(cfgPath)
        if err != nil {
            continue
        }
        // Срезаем возможный UTF-8 BOM
        raw = bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF})
        var localCfg struct {
            RemotePort int    `json:"remote_port"`
            AuthUser   string `json:"auth_user"`
            AuthPass   string `json:"auth_pass"`
        }
        if json.Unmarshal(raw, &localCfg) == nil {
            if localCfg.RemotePort > 0 {
                port = localCfg.RemotePort
            }
            if strings.TrimSpace(localCfg.AuthUser) != "" {
                user = strings.TrimSpace(localCfg.AuthUser)
            }
            pass = localCfg.AuthPass
            break
        }
    }

    return NodeConfig{
        ID:             "local-pc",
        Name:           "Локальный хост",
        Group:          "IT Сервер",
        Host:           fmt.Sprintf("127.0.0.1:%d", port),
        AuthUser:       user,
        AuthPass:       pass,
        AlertOnOffline: true,
    }
}

func (h *HubManager) LoadSettings() {
    h.mu.Lock()
    defer h.mu.Unlock()

    data, err := os.ReadFile(h.settingsPath)
    if err != nil {
        h.language = "ru"
        h.nodes = []NodeConfig{h.buildDefaultLocalNode()}
        _ = h.saveDiskLocked()
    } else {
        data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
        var hs HubSettings
        if err := json.Unmarshal(data, &hs); err == nil {
            if hs.Language != "" {
                h.language = hs.Language
            } else {
                h.language = "ru"
            }
            if hs.ClientOrganization != "" {
                h.clientOrganization = hs.ClientOrganization
            }
            h.mspBranding = hs.MspBranding
            h.telegram = hs.Telegram

            if len(hs.Nodes) == 0 {
                h.nodes = []NodeConfig{h.buildDefaultLocalNode()}
                _ = h.saveDiskLocked()
            } else {
                h.nodes = hs.Nodes
            }
        } else {
            h.language = "ru"
            h.nodes = []NodeConfig{h.buildDefaultLocalNode()}
            _ = h.saveDiskLocked()
        }
    }

    evData, err := os.ReadFile(h.eventsPath)
    if err == nil {
        _ = json.Unmarshal(evData, &h.events)
    }
    if h.events == nil {
        h.events = make([]HubEvent, 0)
    }
}

func (h *HubManager) saveDiskLocked() error {
    nodesToSave := h.nodes
    if nodesToSave == nil {
        nodesToSave = make([]NodeConfig, 0)
    }
        hs := HubSettings{
        Language:           h.language,
        ClientOrganization: h.clientOrganization,
        MspBranding:        h.mspBranding,
        Nodes:              h.nodes,
        Telegram:           h.telegram,
    }
    raw, err := json.MarshalIndent(hs, "", "  ")
    if err != nil {
        return err
    }
    return os.WriteFile(h.settingsPath, raw, 0644)
}

func (h *HubManager) saveEventsLocked() error {
    eventsToSave := h.events
    if eventsToSave == nil {
        eventsToSave = make([]HubEvent, 0)
    }
    raw, err := json.MarshalIndent(eventsToSave, "", "  ")
    if err != nil {
        return err
    }
    return os.WriteFile(h.eventsPath, raw, 0644)
}

func (h *HubManager) recordEvent(node NodeConfig, severity, message string) {
    h.mu.Lock()
    defer h.mu.Unlock()

    ev := HubEvent{
        ID:           fmt.Sprintf("ev-%d", time.Now().UnixNano()),
        Timestamp:    time.Now().Format("02.01 15:04:05"),
        NodeID:       node.ID,
        NodeName:     node.Name,
        Group:        node.Group,
        Severity:     severity,
        Message:      message,
        Resolved:     false,
        Acknowledged: false,
    }

    h.events = append([]HubEvent{ev}, h.events...)
    if len(h.events) > 300 {
        h.events = h.events[:300]
    }
    _ = h.saveEventsLocked()
}

func (h *HubManager) resolveActiveEventsForNode(nodeID string) {
    h.mu.Lock()
    defer h.mu.Unlock()

    changed := false
    for i := range h.events {
        if h.events[i].NodeID == nodeID && !h.events[i].Resolved {
            h.events[i].Resolved = true
            changed = true
        }
    }
    if changed {
        _ = h.saveEventsLocked()
    }
}

func (h *HubManager) SendTelegram(text string) error {
    h.mu.RLock()
    token := h.telegram.BotToken
    chatID := h.telegram.ChatID
    enabled := h.telegram.Enabled
    h.mu.RUnlock()

    if !enabled || token == "" || chatID == "" {
        return fmt.Errorf("telegram not configured")
    }

    url := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", token)
    body, _ := json.Marshal(map[string]string{
        "chat_id":    chatID,
        "text":       text,
        "parse_mode": "HTML",
    })

    resp, err := http.Post(url, "application/json", bytes.NewBuffer(body))
    if err != nil {
        return err
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
        return fmt.Errorf("TG response code: %d", resp.StatusCode)
    }
    return nil
}

func (h *HubManager) getLocalizedAlert(alertType string, node NodeConfig, st *NodeStatus) string {
    isRu := h.language == "ru"

    switch alertType {
    case "OFFLINE":
        if isRu {
            return fmt.Sprintf("🔴 <b>[CoreGuard] Узел OFFLINE</b>\n<b>Узел:</b> %s (<code>%s</code>)\n<b>Группа:</b> %s\n<i>Хост недоступен или выключен</i>",
                node.Name, node.Host, node.Group)
        }
        return fmt.Sprintf("🔴 <b>[CoreGuard] Node OFFLINE</b>\n<b>Node:</b> %s (<code>%s</code>)\n<b>Group:</b> %s\n<i>Host is unreachable or down</i>",
            node.Name, node.Host, node.Group)

    case "CRITICAL":
        if isRu {
            return fmt.Sprintf("🚨 <b>[CoreGuard] КРИТИЧЕСКИЙ СБОЙ</b>\n<b>Узел:</b> %s (<code>%s</code>)\n<b>Группа:</b> %s\nЦП: %d°C | Диск C:: %.1f GB (%.0f%%)\nКритических дисков: %d",
                node.Name, node.Host, node.Group, st.CpuTemp, st.FreeGB, st.FreePct, st.DrivesCrit)
        }
        return fmt.Sprintf("🚨 <b>[CoreGuard] CRITICAL ALERT</b>\n<b>Node:</b> %s (<code>%s</code>)\n<b>Group:</b> %s\nCPU: %d°C | Drive C:: %.1f GB (%.0f%%)\nCritical drives: %d",
            node.Name, node.Host, node.Group, st.CpuTemp, st.FreeGB, st.FreePct, st.DrivesCrit)

    case "WARNING":
        if isRu {
            return fmt.Sprintf("⚠️ <b>[CoreGuard] ПРЕДУПРЕЖДЕНИЕ</b>\n<b>Узел:</b> %s (<code>%s</code>)\n<b>Группа:</b> %s\nЦП: %d°C | Диск C:: %.1f GB (%.0f%%)",
                node.Name, node.Host, node.Group, st.CpuTemp, st.FreeGB, st.FreePct)
        }
        return fmt.Sprintf("⚠️ <b>[CoreGuard] WARNING</b>\n<b>Node:</b> %s (<code>%s</code>)\n<b>Group:</b> %s\nCPU: %d°C | Drive C:: %.1f GB (%.0f%%)",
            node.Name, node.Host, node.Group, st.CpuTemp, st.FreeGB, st.FreePct)

    case "HEALTHY":
        if isRu {
            return fmt.Sprintf("✅ <b>[CoreGuard] Восстановление нормы</b>\n<b>Узел:</b> %s (<code>%s</code>)\nСостояние стабильно (HEALTHY).",
                node.Name, node.Host)
        }
        return fmt.Sprintf("✅ <b>[CoreGuard] Recovered</b>\n<b>Node:</b> %s (<code>%s</code>)\nSystem status is stable (HEALTHY).",
            node.Name, node.Host)

    case "TEST":
        if isRu {
            return "🔔 <b>[CoreGuard] Проверка связи:</b> Тестовое оповещение успешно доставлено из центрального хаба CoreGuard Central."
        }
        return "🔔 <b>[CoreGuard] Connection Test:</b> Test notification successfully delivered from CoreGuard Central NOC hub."
    }
    return ""
}

func (h *HubManager) dispatchAlert(node NodeConfig, st *NodeStatus) {
    h.mu.Lock()
    prev := h.lastSent[node.ID]
    curr := st.Overall
    h.lastSent[node.ID] = curr
    tg := h.telegram
    isRu := h.language == "ru"
    h.mu.Unlock()

    if prev == curr {
        return
    }

    if curr == "CRITICAL" {
        msg := fmt.Sprintf("ЦП: %d°C, Диск C:: %.1f GB, Сбойных дисков: %d", st.CpuTemp, st.FreeGB, st.DrivesCrit)
        if !isRu {
            msg = fmt.Sprintf("CPU: %d°C, Drive C:: %.1f GB, Critical drives: %d", st.CpuTemp, st.FreeGB, st.DrivesCrit)
        }
        h.recordEvent(node, "CRITICAL", msg)
    } else if curr == "WARNING" {
        msg := fmt.Sprintf("ЦП: %d°C, Диск C:: %.1f GB", st.CpuTemp, st.FreeGB)
        if !isRu {
            msg = fmt.Sprintf("CPU: %d°C, Drive C:: %.1f GB", st.CpuTemp, st.FreeGB)
        }
        h.recordEvent(node, "WARNING", msg)
    } else if curr == "OFFLINE" {
        if node.AlertOnOffline {
            msg := "Узел не отвечает по сети"
            if !isRu {
                msg = "Host is unreachable on network"
            }
            h.recordEvent(node, "OFFLINE", msg)
        }
    } else if curr == "HEALTHY" && (prev == "CRITICAL" || prev == "WARNING" || prev == "OFFLINE") {
        h.resolveActiveEventsForNode(node.ID)
    }

    var msg string
    if curr == "OFFLINE" && tg.NotifyOnOffline && node.AlertOnOffline {
        msg = h.getLocalizedAlert("OFFLINE", node, st)
    } else if curr == "CRITICAL" && tg.NotifyOnCrit {
        msg = h.getLocalizedAlert("CRITICAL", node, st)
    } else if curr == "WARNING" && tg.NotifyOnWarn {
        msg = h.getLocalizedAlert("WARNING", node, st)
    } else if curr == "HEALTHY" {
        if prev == "OFFLINE" {
            if tg.NotifyOnOffline && node.AlertOnOffline {
                msg = h.getLocalizedAlert("HEALTHY", node, st)
            }
        } else if prev == "CRITICAL" || prev == "WARNING" {
            msg = h.getLocalizedAlert("HEALTHY", node, st)
        }
    }

    if msg != "" {
        go h.SendTelegram(msg)
    }
}

func (h *HubManager) PollNode(node NodeConfig) *NodeStatus {
    st := &NodeStatus{
        Config:   node,
        Overall:  "OFFLINE",
        LastSeen: time.Now().Format("15:04:05"),
    }

    url := fmt.Sprintf("http://%s/api/data", node.Host)
    req, err := http.NewRequest("GET", url, nil)
    if err != nil {
        h.dispatchAlert(node, st)
        return st
    }
    req.Header.Set("Cache-Control", "no-cache")

    if node.AuthUser != "" || node.AuthPass != "" {
        auth := base64.StdEncoding.EncodeToString([]byte(node.AuthUser + ":" + node.AuthPass))
        req.Header.Set("Authorization", "Basic "+auth)
    }

    resp, err := h.client.Do(req)
    if err != nil {
        h.dispatchAlert(node, st)
        return st
    }
    defer resp.Body.Close()

    if resp.StatusCode == http.StatusForbidden {
        st.Overall = "NO_LICENSE"
        st.Online = false
        return st
    }

    if resp.StatusCode != http.StatusOK {
        h.dispatchAlert(node, st)
        return st
    }

    var outer struct {
        Version string `json:"version"`
        Report  struct {
            System struct {
                CpuTemp     int     `json:"cpu_temp"`
                CpuTempC    int     `json:"cpu_temp_c"`
                CpuState    string  `json:"cpu_state"`
                MbTemp      int     `json:"mb_temp"`
                MbTempC     int     `json:"mb_temp_c"`
                MbState     string  `json:"mb_state"`
                FreeGB      float64 `json:"free_gb"`
                FreePercent float64 `json:"free_percent"`
                SpaceState  string  `json:"space_state"`
                Overall     string  `json:"overall_state"`
            } `json:"system"`
            Drives []struct {
                Index              int      `json:"index"`
                Model              string   `json:"model"`
                TempC              int      `json:"temperature_c"`
                HealthState        string   `json:"health_state"`
                PendingSectors     uint64   `json:"pending_sectors"`
                ReallocatedSectors uint64   `json:"reallocated_sectors"`
                MediaErrors        uint64   `json:"media_errors"`
                Alerts             []string `json:"alerts"`
            } `json:"drives"`
        } `json:"report"`
    }

    if err := json.NewDecoder(resp.Body).Decode(&outer); err != nil {
        h.dispatchAlert(node, st)
        return st
    }

    if strings.TrimSpace(outer.Version) != "" {
        st.AgentVersion = strings.TrimSpace(outer.Version)
    } else {
        st.AgentVersion = "legacy"
    }
    rep := outer.Report
    st.Online = true

    if rep.System.CpuTemp != 0 {
        st.CpuTemp = rep.System.CpuTemp
    } else {
        st.CpuTemp = rep.System.CpuTempC
    }

    if rep.System.MbTemp != 0 {
        st.MbTemp = rep.System.MbTemp
    } else {
        st.MbTemp = rep.System.MbTempC
    }

    st.FreeGB = rep.System.FreeGB
    st.FreePct = rep.System.FreePercent
    st.DrivesTotal = len(rep.Drives)

    critCount := 0
    hasWarn := false
    var driveAlerts []string
    for _, d := range rep.Drives {
        if d.HealthState == "IGNORED" {
            continue
        }
        isCritDrive := d.HealthState == "CRITICAL" || d.PendingSectors > 0 || d.ReallocatedSectors > 5 || d.MediaErrors > 0
        if isCritDrive {
            critCount++
            if len(d.Alerts) > 0 {
                for _, a := range d.Alerts {
                    driveAlerts = append(driveAlerts, fmt.Sprintf("%s: %s", d.Model, a))
                }
            } else {
                driveAlerts = append(driveAlerts, fmt.Sprintf("%s: критическое состояние SMART (Pending:%d, Realloc:%d, Media:%d)", d.Model, d.PendingSectors, d.ReallocatedSectors, d.MediaErrors))
            }
        } else if d.HealthState == "WARNING" || d.ReallocatedSectors > 0 {
            hasWarn = true
        }
    }
    st.DrivesCrit = critCount
    st.DriveAlerts = driveAlerts

    isAgentCrit := rep.System.CpuState == "CRITICAL" || rep.System.MbState == "CRITICAL" || rep.System.SpaceState == "CRITICAL" || rep.System.Overall == "CRITICAL"
    isAgentWarn := rep.System.CpuState == "WARNING" || rep.System.MbState == "WARNING" || rep.System.SpaceState == "WARNING" || rep.System.Overall == "WARNING"

    if critCount > 0 || isAgentCrit || st.CpuTemp >= 80 {
        st.Overall = "CRITICAL"
    } else if hasWarn || isAgentWarn || st.CpuTemp >= 70 {
        st.Overall = "WARNING"
    } else {
        st.Overall = "HEALTHY"
    }

    h.dispatchAlert(node, st)
    go h.syncBrandingToNode(node)
    return st
}

func (h *HubManager) syncBrandingToNode(node NodeConfig) {
    h.mu.RLock()
    payload := map[string]interface{}{
        "client_organization": h.clientOrganization,
        "msp_branding":        h.mspBranding,
    }
    h.mu.RUnlock()

    body, err := json.Marshal(payload)
    if err != nil {
        return
    }

    url := fmt.Sprintf("http://%s/api/branding", node.Host)
    req, err := http.NewRequest("POST", url, bytes.NewBuffer(body))
    if err != nil {
        return
    }
    req.Header.Set("Content-Type", "application/json")
    if node.AuthUser != "" || node.AuthPass != "" {
        auth := base64.StdEncoding.EncodeToString([]byte(node.AuthUser + ":" + node.AuthPass))
        req.Header.Set("Authorization", "Basic "+auth)
    }

    resp, err := h.client.Do(req)
    if err == nil {
        resp.Body.Close()
    }
}

func (h *HubManager) TriggerPoll() {
    h.mu.RLock()
    nodesCopy := make([]NodeConfig, len(h.nodes))
    copy(nodesCopy, h.nodes)
    h.mu.RUnlock()

    var wg sync.WaitGroup
    for _, n := range nodesCopy {
        wg.Add(1)
        go func(nc NodeConfig) {
            defer wg.Done()
            status := h.PollNode(nc)
            h.mu.Lock()
            hasUnread := false
            for _, ev := range h.events {
                if ev.NodeID == nc.ID && !ev.Acknowledged {
                    hasUnread = true
                    break
                }
            }
            status.HasUnreadAlert = hasUnread
            h.states[nc.ID] = status
            h.mu.Unlock()
        }(n)
    }
    wg.Wait()
}

func (h *HubManager) Worker() {
    for {
        h.TriggerPoll()
        time.Sleep(5 * time.Second)
    }
}

// Windows Service Runner
type hubWindowsService struct {
    server *http.Server
}

func (s *hubWindowsService) Execute(args []string, r <-chan svc.ChangeRequest, changes chan<- svc.Status) (bool, uint32) {
    const cmdsAccepted = svc.AcceptStop | svc.AcceptShutdown
    changes <- svc.Status{State: svc.StartPending}

    serverErrChan := make(chan error, 1)
    go func() {
        if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
            serverErrChan <- err
        }
    }()

    changes <- svc.Status{State: svc.Running, Accepts: cmdsAccepted}

    for {
        select {
        case <-serverErrChan:
            changes <- svc.Status{State: svc.StopPending}
            return false, 1
        case req := <-r:
            switch req.Cmd {
            case svc.Interrogate:
                changes <- req.CurrentStatus
            case svc.Stop, svc.Shutdown:
                changes <- svc.Status{State: svc.StopPending}
                ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
                _ = s.server.Shutdown(ctx)
                cancel()
                return false, 0
            }
        }
    }
}

type ReleaseManifest struct {
    Available bool   `json:"available"`
    Version   string `json:"version"`
    SHA256    string `json:"sha256"`
    BuiltAt   string `json:"built_at,omitempty"`
    FileSize  int64  `json:"file_size"`
    Dir       string `json:"-"`
}

func getReleasesDir() string {
    if exe, err := os.Executable(); err == nil {
        exeRel := filepath.Join(filepath.Dir(exe), "releases")
        if st, err := os.Stat(exeRel); err == nil && st.IsDir() {
            return exeRel
        }
        // Создаём папку рядом с exe, если ещё не создана
        _ = os.MkdirAll(exeRel, 0755)
        return exeRel
    }
    _ = os.MkdirAll("releases", 0755)
    return "releases"
}

func loadHubReleaseInfo() ReleaseManifest {
    relDir := getReleasesDir()
    exeFile := filepath.Join(relDir, "CoreGuard.exe")
    info, err := os.Stat(exeFile)
    if err != nil || info.IsDir() || info.Size() < 1024*512 {
        return ReleaseManifest{Available: false, Dir: relDir}
    }

    res := ReleaseManifest{
        Available: true,
        FileSize:  info.Size(),
        BuiltAt:   info.ModTime().Format("2006-01-02 15:04:05"),
        Dir:       relDir,
    }

    if raw, err := os.ReadFile(filepath.Join(relDir, "manifest.json")); err == nil {
        var m struct {
            Version string `json:"version"`
            SHA256  string `json:"sha256"`
            BuiltAt string `json:"built_at"`
        }
        if json.Unmarshal(raw, &m) == nil {
            res.Version = strings.TrimSpace(m.Version)
            res.SHA256 = strings.ToLower(strings.TrimSpace(m.SHA256))
            if m.BuiltAt != "" {
                res.BuiltAt = m.BuiltAt
            }
        }
    }

    if res.SHA256 == "" {
        if f, err := os.Open(exeFile); err == nil {
            h := sha256.New()
            if _, err := io.Copy(h, f); err == nil {
                res.SHA256 = hex.EncodeToString(h.Sum(nil))
            }
            _ = f.Close()
        }
    }
    if res.Version == "" {
        res.Version = "custom"
    }
    return res
}

func resolveHubIPForTarget(targetHostPort string) string {
    host := targetHostPort
    if !strings.Contains(host, ":") {
        host = host + ":48123"
    }
    conn, err := net.DialTimeout("udp", host, 500*time.Millisecond)
    if err == nil {
        defer conn.Close()
        if udpAddr, ok := conn.LocalAddr().(*net.UDPAddr); ok && udpAddr.IP != nil && !udpAddr.IP.IsLoopback() {
            return udpAddr.IP.String()
        }
    }
    // Fallback: первый не-loopback IPv4 интерфейс
    if ifaces, err := net.Interfaces(); err == nil {
        for _, iface := range ifaces {
            if (iface.Flags&net.FlagUp) == 0 || (iface.Flags&net.FlagLoopback) != 0 {
                continue
            }
            addrs, _ := iface.Addrs()
            for _, addr := range addrs {
                if ipNet, ok := addr.(*net.IPNet); ok && ipNet.IP.To4() != nil && !ipNet.IP.IsLoopback() {
                    return ipNet.IP.String()
                }
            }
        }
    }
    return "127.0.0.1"
}
func main() {
    // Фиксируем рабочую папку строго рядом с exe, чтобы служба не читала System32
    exePath, err := os.Executable()
    if err == nil {
        _ = os.Chdir(filepath.Dir(exePath))
    }

    hub := NewHubManager("hub_config.json", "hub_events.json")
    hub.LoadSettings()

    go hub.Worker()

    mux := http.NewServeMux()

    mux.HandleFunc("/api/nodes", func(w http.ResponseWriter, r *http.Request) {
        hub.mu.RLock()
        defer hub.mu.RUnlock()
        w.Header().Set("Content-Type", "application/json")
        _ = json.NewEncoder(w).Encode(hub.states)
    })

    mux.HandleFunc("/api/events", func(w http.ResponseWriter, r *http.Request) {
        hub.mu.RLock()
        defer hub.mu.RUnlock()
        w.Header().Set("Content-Type", "application/json")

        unreadCount := 0
        for _, ev := range hub.events {
            if !ev.Acknowledged {
                unreadCount++
            }
        }

        _ = json.NewEncoder(w).Encode(map[string]interface{}{
            "events":       hub.events,
            "unread_count": unreadCount,
        })
    })

    mux.HandleFunc("/api/events/ack", func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "application/json")
        if r.Method != http.MethodPost {
            http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
            return
        }

        var req struct {
            ID  string `json:"id"`
            All bool   `json:"all"`
        }
        if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
            w.WriteHeader(http.StatusBadRequest)
            _ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
            return
        }

        hub.mu.Lock()
        if req.All {
            for i := range hub.events {
                hub.events[i].Acknowledged = true
            }
            for _, st := range hub.states {
                st.HasUnreadAlert = false
            }
        } else if req.ID != "" {
            var affectedNodeID string
            for i := range hub.events {
                if hub.events[i].ID == req.ID {
                    hub.events[i].Acknowledged = true
                    affectedNodeID = hub.events[i].NodeID
                    break
                }
            }
            if affectedNodeID != "" {
                hasOtherUnread := false
                for _, ev := range hub.events {
                    if ev.NodeID == affectedNodeID && !ev.Acknowledged {
                        hasOtherUnread = true
                        break
                    }
                }
                if st, ok := hub.states[affectedNodeID]; ok {
                    st.HasUnreadAlert = hasOtherUnread
                }
            }
        }
        _ = hub.saveEventsLocked()
        hub.mu.Unlock()

        _ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
    })

    mux.HandleFunc("/api/config", func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "application/json")

        if r.Method == http.MethodGet {
            hub.mu.RLock()
            defer hub.mu.RUnlock()
            nodesList := hub.nodes
            if nodesList == nil {
                nodesList = make([]NodeConfig, 0)
            }
                        _ = json.NewEncoder(w).Encode(HubSettings{
                Language:           hub.language,
                ClientOrganization: hub.clientOrganization,
                MspBranding:        hub.mspBranding,
                Nodes:              hub.nodes,
                Telegram:           hub.telegram,
            })
            return
        }

        if r.Method == http.MethodPost {
            var req HubSettings
            if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
                http.Error(w, err.Error(), http.StatusBadRequest)
                return
            }

            hub.mu.Lock()
            if req.Language == "en" {
                hub.language = "en"
            } else {
                hub.language = "ru"
            }
            hub.clientOrganization = req.ClientOrganization
            hub.mspBranding = req.MspBranding
            if len(req.Nodes) == 0 {
                hub.nodes = []NodeConfig{hub.buildDefaultLocalNode()}
            } else {
                // Если в существующей конфигурации был local-pc, а клиент прислал только новые узлы без него (например, со старой кэш-страницы), сохраняем local-pc
                hasLocalInReq := false
                for _, n := range req.Nodes {
                    if n.ID == "local-pc" || strings.HasPrefix(n.Host, "127.0.0.1:") || strings.EqualFold(n.Host, "localhost:48123") {
                        hasLocalInReq = true
                        break
                    }
                }
                if !hasLocalInReq {
                    var existingLocal *NodeConfig
                    for _, oldN := range hub.nodes {
                        if oldN.ID == "local-pc" {
                            cp := oldN
                            existingLocal = &cp
                            break
                        }
                    }
                    if existingLocal != nil {
                        hub.nodes = append([]NodeConfig{*existingLocal}, req.Nodes...)
                    } else {
                        hub.nodes = req.Nodes
                    }
                } else {
                    hub.nodes = req.Nodes
                }
            }
            hub.telegram = req.Telegram
            _ = hub.saveDiskLocked()
            hub.mu.Unlock()

                        go func() {
                hub.TriggerPoll()
                hub.mu.RLock()
                nodesList := make([]NodeConfig, len(hub.nodes))
                copy(nodesList, hub.nodes)
                hub.mu.RUnlock()
                for _, n := range nodesList {
                    hub.syncBrandingToNode(n)
                }
            }()
            _ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
            return
        }

        http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
    })

    mux.HandleFunc("/api/test-telegram", func(w http.ResponseWriter, r *http.Request) {
        hub.mu.RLock()
        msg := hub.getLocalizedAlert("TEST", NodeConfig{}, nil)
        hub.mu.RUnlock()

        err := hub.SendTelegram(msg)
        w.Header().Set("Content-Type", "application/json")
        if err != nil {
            w.WriteHeader(http.StatusBadRequest)
            _ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
            return
        }
        _ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
    })

    relDir := getReleasesDir()
    mux.Handle("/releases/", http.StripPrefix("/releases/", http.FileServer(http.Dir(relDir))))

    mux.HandleFunc("/api/github/check", func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "application/json")
        latestWebURL := "https://github.com/gresseir-dev/CoreGuard/releases/latest"

        // Используем веб-редирект github.com/.../releases/latest -> .../releases/tag/vX.Y.Z (не тратит лимит api.github.com и не даёт HTTP 403)
        noRedirectClient := &http.Client{
            Timeout: 12 * time.Second,
            CheckRedirect: func(req *http.Request, via []*http.Request) error {
                return http.ErrUseLastResponse
            },
        }
        req, _ := http.NewRequest(http.MethodGet, latestWebURL, nil)
        req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) CoreGuard-Hub")

        resp, err := noRedirectClient.Do(req)
        if err != nil {
            w.WriteHeader(http.StatusBadGateway)
            _ = json.NewEncoder(w).Encode(map[string]interface{}{"error": "Ошибка соединения с GitHub: " + err.Error()})
            return
        }
        _ = resp.Body.Close()

        loc := resp.Header.Get("Location")
        tagName := ""
        if idx := strings.LastIndex(loc, "/tag/"); idx >= 0 {
            tagName = strings.TrimSpace(loc[idx+5:])
        }

        if tagName == "" {
            w.WriteHeader(http.StatusBadGateway)
            _ = json.NewEncoder(w).Encode(map[string]interface{}{
                "error": fmt.Sprintf("Не удалось определить последний тег релиза на GitHub (HTTP %d)", resp.StatusCode),
            })
            return
        }

        remoteVer := strings.TrimPrefix(tagName, "v")
        localRel := loadHubReleaseInfo()
        exeURL := fmt.Sprintf("https://github.com/gresseir-dev/CoreGuard/releases/download/%s/CoreGuard.exe", tagName)

        // Проверяем HEAD-запросом, прикреплён ли CoreGuard.exe к этому релизу
        headClient := &http.Client{Timeout: 10 * time.Second}
        headReq, _ := http.NewRequest(http.MethodHead, exeURL, nil)
        headReq.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) CoreGuard-Hub")
        exeAvailable := false
        if hResp, hErr := headClient.Do(headReq); hErr == nil {
            exeAvailable = (hResp.StatusCode == http.StatusOK)
            _ = hResp.Body.Close()
        }

        hasUpdate := remoteVer != "" && (!localRel.Available || localRel.Version != remoteVer)
        _ = json.NewEncoder(w).Encode(map[string]interface{}{
            "has_update":     hasUpdate,
            "remote_version": remoteVer,
            "local_version":  localRel.Version,
            "local_ready":    localRel.Available,
            "release_name":   "CoreGuard Enterprise " + tagName,
            "html_url":       fmt.Sprintf("https://github.com/gresseir-dev/CoreGuard/releases/tag/%s", tagName),
            "exe_available":  exeAvailable,
            "exe_url":        exeURL,
        })
    })

    mux.HandleFunc("/api/github/sync", func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "application/json")
        if r.Method != http.MethodPost {
            http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
            return
        }

        // Узнаём точный тег последнего релиза через веб-редирект
        noRedirectClient := &http.Client{
            Timeout: 12 * time.Second,
            CheckRedirect: func(req *http.Request, via []*http.Request) error {
                return http.ErrUseLastResponse
            },
        }
        tagReq, _ := http.NewRequest(http.MethodGet, "https://github.com/gresseir-dev/CoreGuard/releases/latest", nil)
        tagReq.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) CoreGuard-Hub")
        tagResp, err := noRedirectClient.Do(tagReq)
        if err != nil {
            w.WriteHeader(http.StatusBadGateway)
            _ = json.NewEncoder(w).Encode(map[string]interface{}{"error": "Ошибка связи с GitHub: " + err.Error()})
            return
        }
        _ = tagResp.Body.Close()

        loc := tagResp.Header.Get("Location")
        tagName := ""
        if idx := strings.LastIndex(loc, "/tag/"); idx >= 0 {
            tagName = strings.TrimSpace(loc[idx+5:])
        }
        if tagName == "" {
            w.WriteHeader(http.StatusBadGateway)
            _ = json.NewEncoder(w).Encode(map[string]interface{}{"error": "Не найден актуальный релиз на GitHub"})
            return
        }

        remoteVer := strings.TrimPrefix(tagName, "v")
        exeURL := fmt.Sprintf("https://github.com/gresseir-dev/CoreGuard/releases/download/%s/CoreGuard.exe", tagName)

        client := &http.Client{Timeout: 90 * time.Second}
        dlReq, _ := http.NewRequest(http.MethodGet, exeURL, nil)
        dlReq.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) CoreGuard-Hub")
        dlResp, err := client.Do(dlReq)
        if err != nil {
            w.WriteHeader(http.StatusBadGateway)
            _ = json.NewEncoder(w).Encode(map[string]interface{}{"error": "Ошибка скачивания: " + err.Error()})
            return
        }
        defer dlResp.Body.Close()

        if dlResp.StatusCode == http.StatusNotFound {
            w.WriteHeader(http.StatusUnprocessableEntity)
            _ = json.NewEncoder(w).Encode(map[string]interface{}{
                "error": fmt.Sprintf("В релизе %s на GitHub не прикреплён отдельный файл CoreGuard.exe", tagName),
            })
            return
        }
        if dlResp.StatusCode != http.StatusOK {
            w.WriteHeader(http.StatusBadGateway)
            _ = json.NewEncoder(w).Encode(map[string]interface{}{
                "error": fmt.Sprintf("GitHub вернул HTTP %d при скачивании CoreGuard.exe", dlResp.StatusCode),
            })
            return
        }

        relDir := getReleasesDir()
        tmpExe := filepath.Join(relDir, "CoreGuard.exe.tmp")
        finalExe := filepath.Join(relDir, "CoreGuard.exe")

        out, err := os.Create(tmpExe)
        if err != nil {
            w.WriteHeader(http.StatusInternalServerError)
            _ = json.NewEncoder(w).Encode(map[string]interface{}{"error": "Ошибка записи файла: " + err.Error()})
            return
        }

        hasher := sha256.New()
        written, err := io.Copy(io.MultiWriter(out, hasher), dlResp.Body)
        _ = out.Close()
        if err != nil || written < 1024*512 {
            _ = os.Remove(tmpExe)
            w.WriteHeader(http.StatusBadGateway)
            _ = json.NewEncoder(w).Encode(map[string]interface{}{"error": "Файл CoreGuard.exe скачался не полностью"})
            return
        }

        _ = os.Remove(finalExe)
        if err := os.Rename(tmpExe, finalExe); err != nil {
            _ = os.Remove(tmpExe)
            w.WriteHeader(http.StatusInternalServerError)
            _ = json.NewEncoder(w).Encode(map[string]interface{}{"error": "Не удалось сохранить CoreGuard.exe: " + err.Error()})
            return
        }

        sha := hex.EncodeToString(hasher.Sum(nil))
        manifestBytes, _ := json.Marshal(map[string]string{
            "version":  remoteVer,
            "sha256":   sha,
            "built_at": time.Now().Format("2006-01-02 15:04:05") + " (GitHub)",
        })
        _ = os.WriteFile(filepath.Join(relDir, "manifest.json"), manifestBytes, 0644)

        _ = json.NewEncoder(w).Encode(map[string]interface{}{
            "status":  "ok",
            "version": remoteVer,
            "sha256":  sha,
        })
    })

    mux.HandleFunc("/api/release", func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "application/json")
        _ = json.NewEncoder(w).Encode(loadHubReleaseInfo())
    })

    mux.HandleFunc("/api/deploy", func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "application/json")
        if r.Method != http.MethodPost {
            http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
            return
        }

        rel := loadHubReleaseInfo()
        if !rel.Available || rel.SHA256 == "" {
            w.WriteHeader(http.StatusBadRequest)
            _ = json.NewEncoder(w).Encode(map[string]interface{}{
                "error": "В папке releases/ Хаба не найден файл CoreGuard.exe",
            })
            return
        }

        var req struct {
            NodeIDs     []string `json:"node_ids"`
            AllOutdated bool     `json:"all_outdated"`
        }
        _ = json.NewDecoder(r.Body).Decode(&req)

        targetSet := make(map[string]bool)
        for _, id := range req.NodeIDs {
            targetSet[id] = true
        }

        hub.mu.RLock()
        var targets []NodeConfig
        for _, n := range hub.nodes {
            st := hub.states[n.ID]
            if req.AllOutdated {
                if st != nil && st.Online && st.AgentVersion != rel.Version {
                    targets = append(targets, n)
                }
            } else if targetSet[n.ID] {
                targets = append(targets, n)
            }
        }
        hub.mu.RUnlock()

        type deployItemResult struct {
            NodeID   string `json:"node_id"`
            NodeName string `json:"node_name"`
            Success  bool   `json:"success"`
            Message  string `json:"message"`
        }

        results := make([]deployItemResult, 0, len(targets))
        var wg sync.WaitGroup
        var resMu sync.Mutex

        for _, node := range targets {
            wg.Add(1)
            go func(n NodeConfig) {
                defer wg.Done()
                hubIP := resolveHubIPForTarget(n.Host)
                if strings.HasPrefix(n.Host, "127.0.0.1") || strings.HasPrefix(strings.ToLower(n.Host), "localhost") {
                    hubIP = "127.0.0.1"
                }
                downloadURL := fmt.Sprintf("http://%s:9090/releases/CoreGuard.exe", hubIP)

                payloadBytes, _ := json.Marshal(map[string]string{
                    "version": rel.Version,
                    "url":     downloadURL,
                    "sha256":  rel.SHA256,
                })

                updateURL := fmt.Sprintf("http://%s/api/update", n.Host)
                httpReq, err := http.NewRequest(http.MethodPost, updateURL, bytes.NewReader(payloadBytes))
                if err != nil {
                    resMu.Lock()
                    results = append(results, deployItemResult{NodeID: n.ID, NodeName: n.Name, Success: false, Message: err.Error()})
                    resMu.Unlock()
                    return
                }
                httpReq.Header.Set("Content-Type", "application/json")
                if n.AuthUser != "" || n.AuthPass != "" {
                    auth := base64.StdEncoding.EncodeToString([]byte(n.AuthUser + ":" + n.AuthPass))
                    httpReq.Header.Set("Authorization", "Basic "+auth)
                }

                client := &http.Client{Timeout: 30 * time.Second}
                resp, err := client.Do(httpReq)
                if err != nil {
                    resMu.Lock()
                    results = append(results, deployItemResult{NodeID: n.ID, NodeName: n.Name, Success: false, Message: "Ошибка связи: " + err.Error()})
                    resMu.Unlock()
                    return
                }
                bodyBytes, _ := io.ReadAll(resp.Body)
                _ = resp.Body.Close()

                resMu.Lock()
                if resp.StatusCode == http.StatusOK {
                    results = append(results, deployItemResult{
                        NodeID:   n.ID,
                        NodeName: n.Name,
                        Success:  true,
                        Message:  fmt.Sprintf("Обновление до v%s запущено (перезапуск агента)", rel.Version),
                    })
                } else {
                    errMsg := strings.TrimSpace(string(bodyBytes))
                    if errMsg == "" {
                        errMsg = fmt.Sprintf("HTTP %d", resp.StatusCode)
                    }
                    results = append(results, deployItemResult{
                        NodeID:   n.ID,
                        NodeName: n.Name,
                        Success:  false,
                        Message:  errMsg,
                    })
                }
                resMu.Unlock()
            }(node)
        }
        wg.Wait()

        // Через 4 секунды запускаем внеочередной опрос узлов, чтобы в таблице Хаба обновилась версия
        go func() {
            time.Sleep(4 * time.Second)
            hub.TriggerPoll()
        }()

        w.Header().Set("Content-Type", "application/json")
        _ = json.NewEncoder(w).Encode(map[string]interface{}{
            "target_version": rel.Version,
            "results":        results,
        })
    })
    mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "text/html; charset=utf-8")
        _, _ = w.Write(hubDashboardHTML)
    })

    server := &http.Server{
        Addr:    ":9090",
        Handler: mux,
    }

    isService, err := svc.IsWindowsService()
    if err != nil {
        log.Fatalf("svc.IsWindowsService error: %v", err)
    }

    if isService {
        if err := svc.Run("CoreGuardHub", &hubWindowsService{server: server}); err != nil {
            log.Fatalf("svc.Run error: %v", err)
        }
    } else {
        fmt.Println("[CoreGuard Enterprise Central] Запущен в консоли на http://localhost:9090")
        if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
            log.Fatalf("Server error: %v", err)
        }
    }
}
