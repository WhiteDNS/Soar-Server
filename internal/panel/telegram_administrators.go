package panel

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

const (
	telegramOwner     = "owner"
	telegramFullAdmin = "full"
	telegramFreeAdmin = "free"
	telegramMaxAdmins = 8
)

var (
	errTelegramAdminPermission = errors.New("this action requires a different administrator role")
	errTelegramUserID          = errors.New("enter the numeric Telegram ID of a public user")
	errTelegramAdminLimit      = errors.New("the additional administrator limit has been reached")
	errTelegramReplyRequired   = errors.New("reply to the latest prompt, or send /cancel to return to the menu")
)

type telegramAdministrator struct {
	ID   int64  `json:"id"`
	Role string `json:"role"`
}

type telegramDialogue struct {
	conversation       *telegramConversation
	confirmation       *telegramConfirmation
	freeSelected       []string
	freeDeleteSelected []string
}

func (c TelegramConfig) administratorRole(id int64) string {
	if id > 0 && id == c.ChatID {
		return telegramOwner
	}
	for _, a := range c.Administrators {
		if a.ID == id {
			return a.Role
		}
	}
	return ""
}

func validTelegramAdministrators(c TelegramConfig) bool {
	if len(c.Administrators) > telegramMaxAdmins || c.ChatID == 0 && len(c.Administrators) > 0 {
		return false
	}
	seen := map[int64]bool{c.ChatID: true}
	for _, admin := range c.Administrators {
		if admin.ID < 1 || admin.ID >= 1<<52 || seen[admin.ID] || !oneOf(admin.Role, telegramFreeAdmin, telegramFullAdmin) {
			return false
		}
		seen[admin.ID] = true
	}
	return true
}

type telegramActorKey struct{}

func telegramActor(ctx context.Context) telegramAdministrator {
	a, ok := ctx.Value(telegramActorKey{}).(telegramAdministrator)
	if !ok {
		a.Role = telegramOwner
	}
	return a
}

func telegramRecipient(ctx context.Context, fallback int64) int64 {
	if a := telegramActor(ctx); a.ID != 0 {
		return a.ID
	}
	return fallback
}

func (t *Telegram) enterTelegramDialogue(id int64) func() {
	if t.adminDialogues == nil {
		t.adminDialogues = make(map[int64]telegramDialogue)
	}
	owner := t.telegramDialogue
	t.telegramDialogue = t.adminDialogues[id]
	return func() {
		t.adminDialogues[id] = t.telegramDialogue
		t.telegramDialogue = owner
	}
}

func (t *Telegram) administratorName(role string) string {
	if role == telegramFreeAdmin {
		return t.tr("Free configurations", "مدیر کانفیگ رایگان")
	}
	return t.tr("Full management", "مدیر کامل ربات")
}

func (t *Telegram) administratorMenu(ctx context.Context, data string) error {
	if telegramActor(ctx).Role != telegramOwner {
		return errTelegramAdminPermission
	}
	c := t.config()
	action, value, _ := strings.Cut(data, ":")
	switch action {
	case "list":
		t.conversation, t.confirmation = nil, nil
		text := t.tr("Bot administrators\nOnly the owner can add, change or remove administrators. Select an ID to manage its role.",
			"مدیران ربات\nفقط مالک می‌تواند مدیران را اضافه یا حذف کند و نقششان را تغییر دهد. برای مدیریت نقش هر مدیر، شناسه‌اش را انتخاب کنید.")
		k := telegramKeyboard{}
		for _, a := range c.Administrators {
			id := strconv.FormatInt(a.ID, 10)
			k.Rows = append(k.Rows, []telegramButton{{Text: id + " · " + t.administratorName(a.Role), Data: "admins:roles:" + id}})
		}
		if len(c.Administrators) < telegramMaxAdmins {
			k.Rows = append(k.Rows, []telegramButton{t.button("Add administrator", "افزودن ادمین", "admins:add")})
		} else {
			text += t.tr("\nThe list is full. Remove an administrator before adding another.", "\nفهرست مدیران پر است. برای افزودن مدیر تازه، ابتدا یکی از مدیران را حذف کنید.")
		}
		k.Rows = append(k.Rows, []telegramButton{t.button("Back to the dashboard", "بازگشت به داشبورد", "free:manage")})
		_, e := t.notify(ctx, c, text, k)
		return e
	case "add":
		if len(c.Administrators) >= telegramMaxAdmins {
			return errTelegramAdminLimit
		}
		return t.freePrompt(ctx, "administrator", 0, t.tr("Reply with the person's numeric Telegram ID. You will choose their permissions and confirm before access is granted.",
			"شناسهٔ عددی تلگرام فرد را در پاسخ بفرستید. در مرحلهٔ بعد نقش را انتخاب و تأیید می‌کنید."))
	case "roles", "remove", "free", "full":
		if !validFreeChatID(value) {
			return errTelegramUserID
		}
		id, _ := strconv.ParseInt(value, 10, 64)
		role := c.administratorRole(id)
		if role == telegramOwner {
			return errTelegramAdminPermission
		}
		if action == "roles" {
			t.conversation, t.confirmation = nil, nil
			text := t.tr("Choose the role for Telegram ID ", "برای این شناسهٔ تلگرام نقش انتخاب کنید. ") + value +
				t.tr("\nFree configurations covers requests, plans and Telegram users. Full management also includes server settings, backups and updates.",
					"\nمدیر کانفیگ رایگان درخواست‌ها، پلن‌ها و کاربران تلگرام را مدیریت می‌کند. مدیر کامل به تنظیمات سرور، پشتیبان‌ها و به‌روزرسانی هم دسترسی دارد.")
			k := telegramKeyboard{Rows: [][]telegramButton{
				{t.button("Free configurations", "مدیر کانفیگ رایگان", "admins:free:"+value)},
				{t.button("Full management", "مدیر کامل ربات", "admins:full:"+value)},
			}}
			if role != "" {
				text += "\n" + t.tr("Current role: ", "نقش فعلی: ") + t.administratorName(role)
				b := t.button("Remove administrator", "حذف ادمین", "admins:remove:"+value)
				b.Style = "danger"
				k.Rows = append(k.Rows, []telegramButton{b})
			}
			k.Rows = append(k.Rows, []telegramButton{t.button("Back to administrators", "بازگشت به مدیران", "admins:list")})
			_, e := t.notify(ctx, c, text, k)
			return e
		}
		targetRole := action
		text := t.tr("Grant this role to Telegram ID ", "این نقش به شناسهٔ تلگرام داده شود؟ ") + value + "\n" + t.administratorName(action)
		if action == "remove" {
			targetRole = ""
			text = t.tr("Remove this administrator? Their management access ends immediately.", "این مدیر حذف شود؟ دسترسی مدیریتی او بلافاصله قطع می‌شود.") + "\n" + value
		}
		if role == targetRole {
			return ErrConflict
		}
		return t.confirm(ctx, telegramConfirmation{Action: "administrator", FreeChat: id, AdminBefore: role, AdminRole: targetRole}, text)
	}
	return ErrNotFound
}

func (t *Telegram) updateTelegramAdministrator(ctx context.Context, change *telegramConfirmation) error {
	if telegramActor(ctx).Role != telegramOwner {
		return errTelegramAdminPermission
	}
	c := t.config()
	if change.FreeChat == c.ChatID || c.administratorRole(change.FreeChat) != change.AdminBefore {
		return ErrConflict
	}
	updated := slices.Clone(c.Administrators)
	index := slices.IndexFunc(updated, func(a telegramAdministrator) bool { return a.ID == change.FreeChat })
	switch {
	case change.AdminRole == "" && index >= 0:
		updated = slices.Delete(updated, index, index+1)
	case index >= 0:
		updated[index].Role = change.AdminRole
	default:
		if len(updated) >= telegramMaxAdmins {
			return errTelegramAdminLimit
		}
		updated = append(updated, telegramAdministrator{ID: change.FreeChat, Role: change.AdminRole})
	}
	c.Administrators = updated
	if !validTelegramAdministrators(c) {
		return errTelegramUserID
	}
	if e := WritePrivateJSON(t.file, telegramDisk{Config: c, State: t.state}); e != nil {
		return errors.New("cannot save Telegram administrators")
	}
	t.mu.Lock()
	t.cfg = c
	t.mu.Unlock()
	delete(t.adminDialogues, change.FreeChat)
	t.clearFreeChallenge(change.FreeChat)
	_ = t.h.store.Audit("telegram.administrator_changed", fmt.Sprintf("%d:%s", change.FreeChat, change.AdminRole))
	t.updateAdministratorCommands(ctx, c, change.FreeChat, change.AdminRole)
	text := t.tr("Role saved. Ask the administrator to open this bot and send /start.", "نقش ذخیره شد. مدیر برای بازکردن داشبورد باید ربات را باز کند و /start بفرستد.")
	if change.AdminRole == "" {
		text = t.tr("Administrator access was removed.", "دسترسی مدیریتی این شناسه حذف شد.")
	}
	_, e := t.notify(ctx, c, text, telegramKeyboard{Rows: [][]telegramButton{{t.button("Open administrators", "مشاهدهٔ مدیران", "admins:list")}}})
	return e
}

func allowedFreeAdminAction(data string) bool {
	prefix, value, _ := strings.Cut(data, ":")
	action, _, _ := strings.Cut(value, ":")
	return prefix == "free" && oneOf(action,
		"manage", "enable", "disable", "pause", "resume", "restoreresume", "stats", "accounts", "options",
		"queue", "select", "selectall", "unselect", "requestdetail", "approve", "reject", "bulkapprove", "bulkreject",
		"plans", "planedit", "plannew", "planview", "planpublish", "planhide", "plandelete", "engines", "engine", "editor",
		"texts", "text", "access", "member", "block", "unblock", "retry", "retryoff", "custom", "customfor",
		"broadcast", "announce", "broadcaststop")
}
