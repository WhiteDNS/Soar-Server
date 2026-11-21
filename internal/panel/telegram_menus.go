package panel

import (
	"context"
	"errors"
	"slices"
	"strings"
	"time"
)

type telegramCommand struct{ name, en, fa string }

const telegramLanguageLabel = "🌐 فارسی / English"

func administratorCommands(role string) []telegramCommand {
	if role == telegramFreeAdmin {
		return []telegramCommand{
			{"admin", "Free configuration dashboard", "داشبورد کانفیگ رایگان"},
			{"start", "Open the dashboard", "بازکردن داشبورد"},
			{"cancel", "Cancel the current form", "لغو فرم جاری"},
		}
	}
	return []telegramCommand{
		{"admin", "Free configuration dashboard", "داشبورد کانفیگ رایگان"},
		{"start", "Management menu", "منوی مدیریت سرور"},
		{"menu", "Management menu", "منوی مدیریت سرور"},
		{"stats", "Server status", "وضعیت سرور"},
		{"settings", "Server settings", "تنظیمات سرور"},
		{"backup", "Backups", "پشتیبان‌ها"},
		{"restore", "Restore backup", "بازیابی پشتیبان"},
		{"diagnostics", "Diagnostics", "عیب‌یابی"},
		{"access", "Panel address", "آدرس پنل"},
		{"help", "Help", "راهنما"},
		{"cancel", "Cancel", "لغو"},
	}
}

func (t *Telegram) setTelegramCommands(ctx context.Context, c TelegramConfig, scope map[string]any, language string, commands []telegramCommand) error {
	list := make([]map[string]string, 0, len(commands))
	for _, command := range commands {
		description := command.en
		if language == "fa" || language == "" && scope["type"] == "chat" && c.Language == "fa" {
			description = command.fa
		}
		list = append(list, map[string]string{"command": command.name, "description": description})
	}
	body := map[string]any{"commands": list, "scope": scope}
	if language != "" {
		body["language_code"] = language
	}
	return t.call(ctx, c, "setMyCommands", body, nil)
}

func (t *Telegram) updateAdministratorCommands(ctx context.Context, c TelegramConfig, id int64, role string) {
	job, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	scope := map[string]any{"type": "chat", "chat_id": id}
	if role == "" {
		_ = t.call(job, c, "deleteMyCommands", map[string]any{"scope": scope}, nil)
		return
	}
	c.Language = c.administratorLanguage(id)
	_ = t.setTelegramCommands(job, c, scope, "", administratorCommands(role))
}

func (t *Telegram) syncTelegramCommands(ctx context.Context, c TelegramConfig) error {
	public := []telegramCommand{}
	if c.FreeAccess {
		public = []telegramCommand{
			{"start", "Main menu", "منوی اصلی"},
			{"free", "Request a free configuration", "درخواست کانفیگ رایگان"},
			{"free_status", "My account and request status", "حساب و وضعیت درخواست من"},
			{"help", "Connection guide", "راهنمای اتصال"},
			{"language", "Change language", "تغییر زبان"},
		}
	}
	if e := t.setTelegramCommands(ctx, c, map[string]any{"type": "chat", "chat_id": c.ChatID}, "", administratorCommands(telegramOwner)); e != nil {
		return e
	}
	private := map[string]any{"type": "all_private_chats"}
	for _, language := range []string{"", "fa"} {
		if e := t.setTelegramCommands(ctx, c, private, language, public); e != nil {
			return e
		}
	}
	if e := t.call(ctx, c, "setChatMenuButton", map[string]any{"menu_button": map[string]string{"type": "commands"}}, nil); e != nil {
		return e
	}
	for _, admin := range c.Administrators {
		t.updateAdministratorCommands(ctx, c, admin.ID, admin.Role)
	}
	return nil
}

type telegramMenuKey struct{}

type telegramMenuMessage struct {
	chat, message int64
	used          bool
}

func withTelegramMenu(ctx context.Context, chat, message int64) context.Context {
	if chat <= 0 || message <= 0 {
		return ctx
	}
	return context.WithValue(ctx, telegramMenuKey{}, &telegramMenuMessage{chat: chat, message: message})
}

var errTelegramMenuUnchanged = errors.New("Telegram menu is already current")
var telegramPersianDigits = strings.NewReplacer("0", "۰", "1", "۱", "2", "۲", "3", "۳", "4", "۴", "5", "۵", "6", "۶", "7", "۷", "8", "۸", "9", "۹")

func telegramButtonStyle(data string) string {
	action, arg, _ := strings.Cut(data, ":")
	switch action {
	case "engines":
		return "danger"
	case "free", "action", "admins":
		action, arg, _ = strings.Cut(arg, ":")
		if action == "opt" {
			action += ":" + arg
		}
	}
	switch action {
	case "ok", "new", "add", "plannew", "approve", "bulkapprove", "resume", "enable", "planpublish", "restoreresume", "save", "retry", "unblock", "release", "access-apply", "opt:on":
		return "success"
	case "reject", "bulkreject", "disable", "pause", "planhide", "plandelete", "delete", "deleteusers", "remove", "broadcaststop", "cancel", "block", "retryoff",
		"restart", "restore", "disconnect", "reset", "rotate", "subscription-rotate", "opt:off":
		return "danger"
	default:
		return "primary"
	}
}

func styledTelegramKeyboard(k telegramKeyboard) telegramKeyboard {
	k.Rows = slices.Clone(k.Rows)
	for i, row := range k.Rows {
		k.Rows[i] = slices.Clone(row)
		for j := range k.Rows[i] {
			if k.Rows[i][j].Style == "" {
				k.Rows[i][j].Style = telegramButtonStyle(row[j].Data)
			}
		}
	}
	return k
}
