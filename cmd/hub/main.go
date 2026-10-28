package main

import (
    _ "embed"
    "bytes"
    "encoding/base64"
    "encoding/json"
    "fmt"
    "log"
    "net/http"
    "os"
    "sync"
    "time"
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
    HasUnreadAlert bool       `json:"has_unread_alert"`
}

type HubSettings struct {
    Language string         `json:"language"`
    Nodes    []NodeConfig   `json:"nodes"`
    Telegram TelegramConfig `json:"telegram"`
}

type HubEvent struct {
    ID           string `json:"id"`
    Timestamp    string `json:"timestamp"`
    NodeID       string `json:"node_id"`
    NodeName     string `json:"node_name"`
    Group        string `json:"group"`
    Severity     string `json:"severity"` // CRITICAL, WARNING, OFFLINE
    Message      string `json:"message"`
    Resolved     bool   `json:"resolved"`     // Вернулся ли узел в норму
    Acknowledged bool   `json:"acknowledged"` // Прочитано ли админом
}

type HubManager struct {
    mu           sync.RWMutex
    language     string
    nodes        []NodeConfig
    telegram     TelegramConfig
    states       map[string]*NodeStatus
    lastSent     map[string]string // ID -> last overall status
    events       []HubEvent
    client       *http.Client
    settingsPath string
    eventsPath   string
}

func NewHubManager(settingsPath, eventsPath string) *HubManager {
    return &HubManager{
        language:     "ru",
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

func (h *HubManager) LoadSettings() {
    h.mu.Lock()
    defer h.mu.Unlock()

    data, err := os.ReadFile(h.settingsPath)
    if err != nil {
        h.language = "ru"
        h.nodes = []NodeConfig{
            {ID: "local-pc", Name: "Локальный хост", Group: "IT Сервер", Host: "127.0.0.1:48123", AuthUser: "admin", AuthPass: "", AlertOnOffline: true},
        }
        h.saveDiskLocked()
    } else {
        var hs HubSettings
        if err := json.Unmarshal(data, &hs); err == nil {
            if hs.Language != "" {
                h.language = hs.Language
            } else {
                h.language = "ru"
            }
            h.nodes = hs.Nodes
            h.telegram = hs.Telegram
        }
    }

    evData, err := os.ReadFile(h.eventsPath)
    if err == nil {
        _ = json.Unmarshal(evData, &h.events)
    }
}

func (h *HubManager) saveDiskLocked() error {
    hs := HubSettings{
        Language: h.language,
        Nodes:    h.nodes,
        Telegram: h.telegram,
    }
    raw, err := json.MarshalIndent(hs, "", "  ")
    if err != nil {
        return err
    }
    return os.WriteFile(h.settingsPath, raw, 0644)
}

func (h *HubManager) saveEventsLocked() error {
    raw, err := json.MarshalIndent(h.events, "", "  ")
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

    // Добавляем в начало списка
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

    // 1. Фиксация в локальном журнале событий (Event Log)
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
        // Показатели пришли в норму — помечаем активные события узла как resolved
        h.resolveActiveEventsForNode(node.ID)
    }

    // 2. Отправка в Telegram
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
	req.Header.Set("Cache-Control", "no-cache")
    if err != nil {
        h.dispatchAlert(node, st)
        return st
    }

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
        Report struct {
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
                Index       int    `json:"index"`
                TempC       int    `json:"temperature_c"`
                HealthState string `json:"health_state"`
            } `json:"drives"`
        } `json:"report"`
    }

    if err := json.NewDecoder(resp.Body).Decode(&outer); err != nil {
        h.dispatchAlert(node, st)
        return st
    }

    rep := outer.Report
    st.Online = true

    // Поддерживаем оба варианта именования полей
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
    for _, d := range rep.Drives {
        if d.HealthState == "CRITICAL" {
            critCount++
        } else if d.HealthState == "WARNING" {
            hasWarn = true
        }
    }
    st.DrivesCrit = critCount

    // Учитываем пороги агента: если сам агент выставил CRITICAL/WARNING, хаб уважает это
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
    return st
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
            // Проверяем, есть ли неквитированные события по данному узлу
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

func main() {
    hub := NewHubManager("hub_config.json", "hub_events.json")
    hub.LoadSettings()

    go hub.Worker()

    http.HandleFunc("/api/nodes", func(w http.ResponseWriter, r *http.Request) {
        hub.mu.RLock()
        defer hub.mu.RUnlock()
        w.Header().Set("Content-Type", "application/json")
        _ = json.NewEncoder(w).Encode(hub.states)
    })

    // Эндпоинт журнала событий
    http.HandleFunc("/api/events", func(w http.ResponseWriter, r *http.Request) {
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

    // Эндпоинт квитирования (ACK)
    http.HandleFunc("/api/events/ack", func(w http.ResponseWriter, r *http.Request) {
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
            log.Printf("[Hub] ACK error decoding request: %v", err)
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
            log.Println("[Hub] All events acknowledged by operator")
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
            log.Printf("[Hub] Event %s acknowledged", req.ID)
        }
        _ = hub.saveEventsLocked()
        hub.mu.Unlock()

        _ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
    })

    http.HandleFunc("/api/config", func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "application/json")

        if r.Method == http.MethodGet {
            hub.mu.RLock()
            defer hub.mu.RUnlock()
            _ = json.NewEncoder(w).Encode(HubSettings{
                Language: hub.language,
                Nodes:    hub.nodes,
                Telegram: hub.telegram,
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
            hub.nodes = req.Nodes
            hub.telegram = req.Telegram
            _ = hub.saveDiskLocked()
            hub.mu.Unlock()

            go hub.TriggerPoll()
            _ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
            return
        }

        http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
    })

    http.HandleFunc("/api/test-telegram", func(w http.ResponseWriter, r *http.Request) {
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

    http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
        w.Header().Set("Content-Type", "text/html; charset=utf-8")
        _, _ = w.Write(hubDashboardHTML)
    })

    port := "9090"
    fmt.Printf("[CoreGuard Enterprise Central] Server listening on http://127.0.0.1:%s\n", port)
    log.Fatal(http.ListenAndServe(":"+port, nil))
}




