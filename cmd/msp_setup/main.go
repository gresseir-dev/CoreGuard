package main

import (
    "bufio"
    "bytes"
    "encoding/json"
    "fmt"
    "os"
    "path/filepath"
    "strings"
)

type MspProfile struct {
    CompanyName     string `json:"company_name"`
    SupportPhone    string `json:"support_phone"`
    SupportTelegram string `json:"support_telegram"`
    SupportEmail    string `json:"support_email"`
}

func promptField(reader *bufio.Reader, label, current string) string {
    if current != "" {
        fmt.Printf("%s [%s]: ", label, current)
    } else {
        fmt.Printf("%s: ", label)
    }
    line, _ := reader.ReadString('\n')
    val := strings.TrimSpace(line)
    if val == "" {
        return current
    }
    return val
}

func main() {
    exePath, err := os.Executable()
    baseDir := "."
    if err == nil {
        baseDir = filepath.Dir(exePath)
    }
    profilePath := filepath.Join(baseDir, "msp_profile.json")

    profile := MspProfile{
        CompanyName:  "Служба ИТ-поддержки",
        SupportPhone: "+7 (000) 000-00-00",
    }

    if raw, err := os.ReadFile(profilePath); err == nil {
        raw = bytes.TrimPrefix(raw, []byte{0xEF, 0xBB, 0xBF})
        _ = json.Unmarshal(raw, &profile)
    }

    fmt.Println("===============================================================")
    fmt.Println("  CoreGuard Enterprise — Мастер профиля обслуживающей компании")
    fmt.Println("===============================================================")
    fmt.Println("Заполните реквизиты вашей сервисной/MSP-организации один раз.")
    fmt.Println("Файл msp_profile.json будет сохранён рядом с этой утилитой.")
    fmt.Println("При запуске инсталлятора у клиента реквизиты подтянутся в Хаб")
    fmt.Println("автоматически — останется ввести только название клиента.")
    fmt.Println("---------------------------------------------------------------")
    fmt.Println("(Нажмите Enter, чтобы оставить текущее значение в скобках)")
    fmt.Println()

    reader := bufio.NewReader(os.Stdin)
    profile.CompanyName = promptField(reader, "1. Название обслуживающей компании", profile.CompanyName)
    profile.SupportPhone = promptField(reader, "2. Телефон техподдержки", profile.SupportPhone)
    profile.SupportTelegram = promptField(reader, "3. Telegram поддержки (например, @it_help)", profile.SupportTelegram)
    profile.SupportEmail = promptField(reader, "4. Email техподдержки", profile.SupportEmail)

    data, err := json.MarshalIndent(profile, "", "  ")
    if err != nil {
        fmt.Printf("\n[ОШИБКА] Не удалось сформировать JSON: %v\n", err)
        fmt.Print("\nНажмите Enter для выхода...")
        _, _ = reader.ReadString('\n')
        os.Exit(1)
    }

    if err := os.WriteFile(profilePath, append(data, '\n'), 0644); err != nil {
        fmt.Printf("\n[ОШИБКА] Не удалось сохранить файл %s: %v\n", profilePath, err)
        fmt.Print("\nНажмите Enter для выхода...")
        _, _ = reader.ReadString('\n')
        os.Exit(1)
    }

    fmt.Println("\n---------------------------------------------------------------")
    fmt.Printf("[OK] Профиль успешно сохранён: %s\n", profilePath)
    fmt.Println("Положите msp_profile.json в одну папку с CoreGuard_Setup_v..._x64.exe")
    fmt.Println("при установке на сервере обслуживаемой организации.")
    fmt.Print("\nНажмите Enter для выхода...")
    _, _ = reader.ReadString('\n')
}