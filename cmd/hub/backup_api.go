// CoreGuard Enterprise - Copyright (C) 2026 gresseir-dev - Licensed under GNU GPLv3
package main

import (
    "bytes"
    "encoding/json"
    "fmt"
    "io"
    "net"
    "net/http"
    "net/url"
    "os"
    "strings"
    "sync"
    "time"
)

var (
    backupMu       sync.Mutex
    backupConfigs  = make(map[string]json.RawMessage)
    backupHostIPs  = make(map[string]string)
    backupCfgStore = "hub_backup_configs.json"
    backupIPStore  = "hub_backup_ips.json"
)

func registerBackupRoutes() {
    if data, err := os.ReadFile(backupCfgStore); err == nil {
        json.Unmarshal(data, &backupConfigs)
    }
    if data, err := os.ReadFile(backupIPStore); err == nil {
        json.Unmarshal(data, &backupHostIPs)
    }

    // 1. Получение списка известных узлов с бэкапом и их IP
    http.HandleFunc("/api/backup/hosts", func(w http.ResponseWriter, r *http.Request) {
        backupMu.Lock()
        defer backupMu.Unlock()
        w.Header().Set("Content-Type", "application/json")
        json.NewEncoder(w).Encode(backupHostIPs)
    })

    // 2. Проксирование дерева папок с удалённого сетевого узла (порт 9095) в интерфейс Хаба
    http.HandleFunc("/api/backup/remote-fs", func(w http.ResponseWriter, r *http.Request) {
        host := r.URL.Query().Get("host")
        reqPath := r.URL.Query().Get("path")

        backupMu.Lock()
        ip := backupHostIPs[host]
        backupMu.Unlock()

        if ip == "" {
            if strings.EqualFold(host, "LocalHost") {
                ip = "127.0.0.1"
            } else {
                ip = host // Пробуем напрямую по DNS/NetBIOS имени
            }
        }

        targetURL := fmt.Sprintf("http://%s:9095/api/fs/list?path=%s", ip, url.QueryEscape(reqPath))
        client := &http.Client{Timeout: 5 * time.Second}
        resp, err := client.Get(targetURL)
        if err != nil {
            http.Error(w, fmt.Sprintf("Узел %s (%s:9095) недоступен по сети: %v", host, ip, err), http.StatusBadGateway)
            return
        }
        defer resp.Body.Close()

        w.Header().Set("Content-Type", "application/json; charset=utf-8")
        io.Copy(w, resp.Body)
    })

    // 3. Получение и сохранение настроек бэкапа для конкретного хоста
    http.HandleFunc("/api/backup/config", func(w http.ResponseWriter, r *http.Request) {
        backupMu.Lock()
        defer backupMu.Unlock()

        remoteIP, _, _ := net.SplitHostPort(r.RemoteAddr)

        if r.Method == http.MethodGet {
            host := r.URL.Query().Get("host")
            if host != "" && remoteIP != "" && r.URL.Query().Get("from_ui") == "" {
                backupHostIPs[host] = remoteIP
                if ipBytes, err := json.MarshalIndent(backupHostIPs, "", "  "); err == nil {
                    os.WriteFile(backupIPStore, ipBytes, 0644)
                }
            }
            if raw, ok := backupConfigs[host]; ok {
                w.Header().Set("Content-Type", "application/json")
                w.Write(raw)
            } else {
                w.WriteHeader(http.StatusNotFound)
            }
            return
        }

        if r.Method == http.MethodPost {
            var req struct {
                Host   string          `json:"host"`
                Config json.RawMessage `json:"config"`
                FromUI bool            `json:"from_ui"`
            }
            if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Host == "" {
                http.Error(w, "invalid payload", 400)
                return
            }
            if !req.FromUI && remoteIP != "" {
                backupHostIPs[req.Host] = remoteIP
                if ipBytes, err := json.MarshalIndent(backupHostIPs, "", "  "); err == nil {
                    os.WriteFile(backupIPStore, ipBytes, 0644)
                }
            }
            backupConfigs[req.Host] = req.Config
            data, _ := json.MarshalIndent(backupConfigs, "", "  ")
            os.WriteFile(backupCfgStore, data, 0644)

            // Если сохранили из Хаба — сразу пробуем протолкнуть новый конфиг на сетевой агент (9095)
            if req.FromUI {
                ip := backupHostIPs[req.Host]
                if ip == "" {
                    ip = req.Host
                }
                go func(targetIP string, cfgBytes []byte) {
                    c := &http.Client{Timeout: 4 * time.Second}
                    c.Post(fmt.Sprintf("http://%s:9095/api/config", targetIP), "application/json", bytes.NewBuffer(cfgBytes))
                }(ip, req.Config)
            }
            w.WriteHeader(200)
        }
    })

    // 4. Приём отчётов и алертов о бэкапах от узлов
    http.HandleFunc("/api/backup/report", func(w http.ResponseWriter, r *http.Request) {
        var rep struct {
            Host    string `json:"host"`
            OrgName string `json:"org_name"`
            Record  struct {
                Timestamp string   `json:"timestamp"`
                FileName  string   `json:"file_name"`
                SizeMB    float64  `json:"size_mb"`
                Success   bool     `json:"success"`
                Skipped   []string `json:"skipped"`
                Error     string   `json:"error"`
            } `json:"record"`
        }
        if err := json.NewDecoder(r.Body).Decode(&rep); err != nil {
            http.Error(w, err.Error(), 400)
            return
        }

        remoteIP, _, _ := net.SplitHostPort(r.RemoteAddr)
        if rep.Host != "" && remoteIP != "" {
            backupMu.Lock()
            backupHostIPs[rep.Host] = remoteIP
            backupMu.Unlock()
        }

        level := "INFO"
        msg := fmt.Sprintf("[BACKUP OK] Узел %s (%s): архив %s (%.1f МБ) проверен",
            rep.Host, rep.OrgName, rep.Record.FileName, rep.Record.SizeMB)

        if !rep.Record.Success {
            level = "CRITICAL"
            msg = fmt.Sprintf("[BACKUP ERROR] Узел %s (%s): %s", rep.Host, rep.OrgName, rep.Record.Error)
        } else if len(rep.Record.Skipped) > 0 {
            level = "WARNING"
            msg = fmt.Sprintf("[BACKUP WARN] Узел %s (%s): архив %s (%.1f МБ), пропущены папки: %v",
                rep.Host, rep.OrgName, rep.Record.FileName, rep.Record.SizeMB, rep.Record.Skipped)
        }

        appendHubBackupEvent(rep.Host, level, msg)
        w.WriteHeader(200)
    })
}

func appendHubBackupEvent(host, level, message string) {
    backupMu.Lock()
    defer backupMu.Unlock()

    var events []map[string]any
    if data, err := os.ReadFile("hub_events.json"); err == nil {
        json.Unmarshal(data, &events)
    }
    ev := map[string]any{
        "id":        fmt.Sprintf("bkp_%d", time.Now().UnixNano()),
        "timestamp": time.Now().Format("2006-01-02 15:04:05"),
        "host":      host,
        "level":     level,
        "message":   message,
        "ack":       false,
    }
    events = append([]map[string]any{ev}, events...)
    if len(events) > 200 {
        events = events[:200]
    }
    if out, err := json.MarshalIndent(events, "", "  "); err == nil {
        os.WriteFile("hub_events.json", out, 0644)
    }

    // Отправляем алерт в Telegram при CRITICAL или WARNING
    if cfgBytes, err := os.ReadFile("hub_config.json"); err == nil {
        var hCfg map[string]any
        if json.Unmarshal(cfgBytes, &hCfg) == nil {
            tok, _ := hCfg["tg_token"].(string)
            chat, _ := hCfg["tg_chat"].(string)
            if tok != "" && chat != "" && (level == "CRITICAL" || level == "WARNING") {
                u := fmt.Sprintf("https://api.telegram.org/bot%s/sendMessage", tok)
                body, _ := json.Marshal(map[string]string{
                    "chat_id": chat,
                    "text":    message,
                })
                http.Post(u, "application/json", bytes.NewBuffer(body))
            }
        }
    }
}