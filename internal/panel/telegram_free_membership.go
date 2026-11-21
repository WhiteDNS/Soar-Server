package panel

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"
)

const freeMaxJoinChats = 5
const freeJoinCheckTimeout = 4 * time.Second

var (
	errFreeJoinMissing     = errors.New("membership in every required chat must be verified before issuing a free configuration")
	errFreeJoinUnavailable = errors.New("Telegram could not verify membership; check that the bot is still an administrator in each required chat")
	errFreeJoinChatLimit   = errors.New("the required membership list is full; remove a chat before adding another")
	freePublicChatName     = regexp.MustCompile(`^@[A-Za-z0-9_]{1,32}$`)
	freeInviteLink         = regexp.MustCompile(`^https://t\.me/(\+[A-Za-z0-9_-]{6,128}|joinchat/[A-Za-z0-9_-]{6,128}|[A-Za-z0-9_]{1,32})$`)
	telegramUsername       = regexp.MustCompile(`^[A-Za-z0-9_]{1,32}$`)
)

type freeRequiredChat struct {
	ID         int64
	Title, URL string
}

type freeJoinPolicy struct {
	revision uint64
	chats    [freeMaxJoinChats]int64
}

type freeMembershipProof struct {
	policy  freeJoinPolicy
	user    int64
	expires time.Time
}

type freeJoinCacheEntry struct {
	policy   freeJoinPolicy
	botToken string
	checked  time.Time
	missing  []freeRequiredChat
}

type freeJoinHealth struct {
	policy   freeJoinPolicy
	botToken string
	until    time.Time
	err      error
	inFlight chan struct{}
}

type telegramMembership struct {
	Status   string       `json:"status"`
	IsMember bool         `json:"is_member"`
	User     telegramUser `json:"user"`
}

func (c freeCatalog) joinPolicy() freeJoinPolicy {
	p := freeJoinPolicy{revision: c.JoinRevision}
	for i, chat := range c.JoinChats {
		if i == len(p.chats) {
			break
		}
		p.chats[i] = chat.ID
	}
	return p
}

func (c freeCatalog) validateJoinChats() error {
	if len(c.JoinChats) > freeMaxJoinChats || c.JoinRequired && (len(c.JoinChats) == 0 || c.JoinRevision == 0) {
		return errFreeJoinUnavailable
	}
	ids := make(map[int64]bool, len(c.JoinChats))
	for _, item := range c.JoinChats {
		if item.ID >= 0 || item.ID <= -(1<<52) || ids[item.ID] || item.Title == "" ||
			!utf8.ValidString(item.Title) || len(item.Title) > 512 || !freeInviteLink.MatchString(item.URL) ||
			strings.ContainsFunc(item.Title, unicode.IsControl) {
			return errFreeJoinUnavailable
		}
		ids[item.ID] = true
	}
	return nil
}

func (c freeCatalog) checkJoinProof(user int64, now time.Time, proofs []freeMembershipProof) error {
	if !c.JoinRequired {
		return nil
	}
	if len(proofs) == 1 && proofs[0].user == user && proofs[0].policy == c.joinPolicy() && now.Before(proofs[0].expires) {
		return nil
	}
	return errFreeJoinMissing
}

func (t *Telegram) getFreeChatMember(ctx context.Context, c TelegramConfig, chat, user int64) (telegramMembership, error) {
	var member telegramMembership
	if ctx.Err() != nil {
		return member, errFreeJoinUnavailable
	}
	t.freeMu.Lock()
	start := maxTime(time.Now(), t.freeJoinNextLookup)
	if deadline, ok := ctx.Deadline(); ok && !start.Before(deadline) {
		t.freeMu.Unlock()
		return member, errFreeJoinUnavailable
	}
	t.freeJoinNextLookup = start.Add(50 * time.Millisecond)
	t.freeMu.Unlock()
	if delay := time.Until(start); delay > 0 {
		timer := time.NewTimer(delay)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			return member, errFreeJoinUnavailable
		case <-timer.C:
		}
	}
	err := t.call(withFreeLane(ctx, freeMembershipAPILane), c, "getChatMember", map[string]int64{"chat_id": chat, "user_id": user}, &member)
	if err != nil || member.User.ID != user {
		return telegramMembership{}, errFreeJoinUnavailable
	}
	return member, nil
}

func (t *Telegram) noteFreeBotAccess(token string, chat int64, available bool) {
	t.freeMu.Lock()
	defer t.freeMu.Unlock()
	if t.freeJoinBotToken != token || len(t.freeJoinLostAccess) >= 2*freeMaxJoinChats {
		t.freeJoinBotToken = token
		t.freeJoinLostAccess = nil
	}
	if available {
		delete(t.freeJoinLostAccess, chat)
	} else {
		if t.freeJoinLostAccess == nil {
			t.freeJoinLostAccess = make(map[int64]bool)
		}
		t.freeJoinLostAccess[chat] = true
	}
	t.freeJoinCache = nil
	t.freeJoinHealth.until = time.Time{}
}

func (t *Telegram) checkFreeMembership(ctx context.Context, config TelegramConfig, catalog freeCatalog, user int64, browse, refresh bool) (freeMembershipProof, []freeRequiredChat, error) {
	if !catalog.JoinRequired {
		return freeMembershipProof{}, nil, nil
	}
	if err := catalog.validateJoinChats(); err != nil {
		return freeMembershipProof{}, nil, err
	}
	policy, now := catalog.joinPolicy(), t.now()
	t.freeMu.Lock()
	for _, chat := range catalog.JoinChats {
		if t.freeJoinBotToken == config.Token && t.freeJoinLostAccess[chat.ID] {
			t.freeMu.Unlock()
			return freeMembershipProof{}, nil, errFreeJoinUnavailable
		}
	}
	cached := t.freeJoinCache[user]
	t.freeMu.Unlock()
	if browse {
		ttl := 20 * time.Second
		if refresh {
			ttl = 2 * time.Second
		}
		if cached.policy == policy && cached.botToken == config.Token && now.Before(cached.checked.Add(ttl)) {
			return freeMembershipProof{}, cached.missing, nil
		}
	}
	ctx, cancel := context.WithTimeout(ctx, freeJoinCheckTimeout)
	defer cancel()
	if err := t.ensureFreeBotAccess(ctx, config, catalog); err != nil {
		return freeMembershipProof{}, nil, err
	}
	var missing []freeRequiredChat
	for _, chat := range catalog.JoinChats {
		member, err := t.getFreeChatMember(ctx, config, chat.ID, user)
		if err != nil {
			return freeMembershipProof{}, nil, err
		}
		switch member.Status {
		case "creator", "administrator", "member":
			continue
		case "restricted":
			if member.IsMember {
				continue
			}
		case "left", "kicked":
		default:
			return freeMembershipProof{}, nil, errFreeJoinUnavailable
		}
		missing = append(missing, chat)
	}
	now = t.now()
	t.freeMu.Lock()
	if t.freeJoinCache == nil || len(t.freeJoinCache) >= 2048 {
		t.freeJoinCache = make(map[int64]freeJoinCacheEntry)
	}
	t.freeJoinCache[user] = freeJoinCacheEntry{policy: policy, botToken: config.Token, checked: now, missing: missing}
	t.freeMu.Unlock()
	if len(missing) != 0 {
		return freeMembershipProof{}, missing, nil
	}
	return freeMembershipProof{policy: policy, user: user, expires: now.Add(30 * time.Second)}, nil, nil
}

func (t *Telegram) ensureFreeBotAccess(ctx context.Context, config TelegramConfig, catalog freeCatalog) error {
	policy := catalog.joinPolicy()
	for {
		t.freeMu.Lock()
		health := &t.freeJoinHealth
		if health.botToken == config.Token && health.policy == policy && time.Now().Before(health.until) {
			err := health.err
			t.freeMu.Unlock()
			return err
		}
		if pending := health.inFlight; pending != nil {
			t.freeMu.Unlock()
			select {
			case <-ctx.Done():
				return errFreeJoinUnavailable
			case <-pending:
				continue
			}
		}
		pending := make(chan struct{})
		health.inFlight = pending
		t.freeMu.Unlock()
		err := t.checkFreeBotMembership(ctx, config, catalog.JoinChats)
		ttl := time.Minute
		if err != nil {
			ttl = 3 * time.Second
		}
		t.freeMu.Lock()
		t.freeJoinHealth = freeJoinHealth{policy: policy, botToken: config.Token, until: time.Now().Add(ttl), err: err}
		close(pending)
		t.freeMu.Unlock()
		return err
	}
}

func (t *Telegram) freeIssuanceProof(ctx context.Context, user int64) (freeMembershipProof, error) {
	c, err := t.h.store.freeCatalog()
	if err != nil {
		return freeMembershipProof{}, err
	}
	proof, missing, err := t.checkFreeMembership(ctx, t.config(), c, user, false, false)
	if err == nil && len(missing) != 0 {
		err = errFreeJoinMissing
	}
	return proof, err
}

type freeReviewMembership struct {
	proof freeMembershipProof
	err   error
}

func (t *Telegram) freeReviewProofs(ctx context.Context, approve bool, keys []string) []freeReviewMembership {
	results := make([]freeReviewMembership, len(keys))
	if !approve {
		return results
	}
	catalog, err := t.h.store.freeCatalog()
	if err != nil || !catalog.JoinRequired {
		for i := range results {
			results[i].err = err
		}
		return results
	}
	bot := t.config()
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	work := make(chan int, len(keys))
	for i := range keys {
		work <- i
	}
	close(work)
	var wg sync.WaitGroup
	for range min(4, len(keys)) {
		wg.Go(func() {
			for i := range work {
				chat, _, _ := strings.Cut(keys[i], ":")
				user, err := strconv.ParseInt(chat, 10, 64)
				if err != nil || user <= 0 {
					results[i].err = ErrNotFound
					continue
				}
				proof, missing, err := t.checkFreeMembership(ctx, bot, catalog, user, false, false)
				if err == nil && len(missing) != 0 {
					err = errFreeJoinMissing
				}
				results[i] = freeReviewMembership{proof, err}
			}
		})
	}
	wg.Wait()
	return results
}

func validTelegramUsername(name string) bool { return telegramUsername.MatchString(name) }

func (r telegramFreeRequest) displayUser() string {
	chat, _, _ := strings.Cut(r.Key, ":")
	if validTelegramUsername(r.Username) {
		return chat + " · @" + r.Username
	}
	return chat
}

func (t *Telegram) checkFreeBotMembership(ctx context.Context, c TelegramConfig, chats []freeRequiredChat) error {
	ctx, cancel := context.WithTimeout(withFreeLane(ctx, freeMembershipAPILane), freeJoinCheckTimeout)
	defer cancel()
	var bot telegramUser
	if t.call(ctx, c, "getMe", map[string]any{}, &bot) != nil || !bot.IsBot || bot.ID <= 0 {
		return errFreeJoinUnavailable
	}
	for _, chat := range chats {
		m, err := t.getFreeChatMember(ctx, c, chat.ID, bot.ID)
		if err != nil || !oneOf(m.Status, "administrator", "creator") {
			if err == nil {
				t.noteFreeBotAccess(c.Token, chat.ID, false)
			}
			return fmt.Errorf("%s · %w", chat.Title, errFreeJoinUnavailable)
		}
		t.noteFreeBotAccess(c.Token, chat.ID, true)
	}
	return nil
}

func (t *Telegram) freeMembershipMenu(ctx context.Context, action, status string) error {
	if telegramActor(ctx).Role != telegramOwner {
		return errTelegramAdminPermission
	}
	s := t.h.store
	c, err := s.freeCatalog()
	if err != nil {
		return err
	}
	if action == "add" {
		return t.freePrompt(ctx, "membership", c.Revision, t.tr(
			"Up to five chats. Make the bot an administrator in each. Reply with @username for a public chat, or the numeric chat ID followed by its working https://t.me/+ invite link for a private chat.",
			"حداکثر پنج کانال یا گروه. ربات را در هرکدام ادمین کنید. برای چت عمومی نام کاربری با @ را بفرستید. برای چت خصوصی، شناسهٔ عددی چت و سپس لینک دعوت معتبر https://t.me/+ را در همان پاسخ بفرستید."))
	}
	if action != "" {
		kind, arg, _ := strings.Cut(action, ":")
		switch kind {
		case "on", "off", "check":
			if kind != "off" {
				if len(c.JoinChats) == 0 {
					return t.freeMembershipMenu(ctx, "", t.tr("Add at least one chat first.\n\n", "ابتدا یک کانال یا گروه اضافه کنید.\n\n"))
				}
				if err := t.checkFreeBotMembership(ctx, t.config(), c.JoinChats); err != nil {
					return t.freeMembershipMenu(ctx, "", t.tr("A required chat could not be checked. Verify the bot is an administrator in each chat.\n\n", "بررسی یکی از چت‌ها ممکن نبود. مطمئن شوید ربات در همهٔ موارد ادمین است.\n\n"))
				}
				if kind == "check" {
					return t.freeMembershipMenu(ctx, "", t.tr("Bot permissions verified for all listed chats.\n\n", "دسترسی ربات در همهٔ موارد بررسی و تأیید شد.\n\n"))
				}
			}
			err = s.changeFreeCatalog(c.Revision, func(next *freeCatalog) error { next.JoinRequired = kind == "on"; next.JoinRevision++; return nil })
		case "remove":
			id, e := strconv.ParseInt(arg, 10, 64)
			if e != nil {
				return ErrNotFound
			}
			err = s.changeFreeCatalog(c.Revision, func(next *freeCatalog) error {
				index := slices.IndexFunc(next.JoinChats, func(chat freeRequiredChat) bool { return chat.ID == id })
				if index < 0 {
					return ErrNotFound
				}
				next.JoinChats = slices.Delete(next.JoinChats, index, index+1)
				if len(next.JoinChats) == 0 {
					next.JoinRequired = false
				}
				next.JoinRevision++
				return nil
			})
		default:
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		_ = s.Audit("telegram.free_membership_changed", kind)
		return t.freeMembershipMenu(ctx, "", t.tr("Membership settings saved.\n\n", "تنظیمات عضویت ذخیره شد.\n\n"))
	}
	t.conversation = nil
	k := telegramKeyboard{}
	text := status + t.tr("Required membership is off.", "عضویت اجباری خاموش است.")
	toggle := t.button("Turn on", "روشن‌کردن", "free:membership:on")
	if c.JoinRequired {
		text = status + t.tr("Required membership is on. Users must join every listed chat.", "عضویت اجباری روشن است. کاربر باید عضو تمام موارد فهرست باشد.")
		toggle = t.button("Turn off", "خاموش‌کردن", "free:membership:off")
	}
	for _, chat := range c.JoinChats {
		text += fmt.Sprintf("\n%s · %d", chat.Title, chat.ID)
		k.Rows = append(k.Rows, []telegramButton{{Text: chat.Title, URL: chat.URL}, t.button("Remove", "حذف", fmt.Sprintf("free:membership:remove:%d", chat.ID))})
	}
	k.Rows = append(k.Rows, []telegramButton{t.button("Add or update a chat", "افزودن یا به‌روزرسانی چت", "free:membership:add")},
		[]telegramButton{toggle, t.button("Check bot access", "بررسی دسترسی ربات", "free:membership:check")},
		[]telegramButton{t.button("Back", "بازگشت", "free:options")})
	_, err = t.notify(ctx, t.config(), text, k)
	return err
}

func (t *Telegram) addFreeMembershipChat(ctx context.Context, revision uint64, input string) error {
	if telegramActor(ctx).Role != telegramOwner {
		return errTelegramAdminPermission
	}
	bad := func(en, fa string) error { _, err := t.notify(ctx, t.config(), t.tr(en, fa), nil); return err }
	parts := strings.Fields(input)
	if len(parts) < 1 || len(parts) > 2 {
		return bad("Use @username, or numeric ID and a Telegram invite link. Reply to the same prompt.", "نام کاربری با @ یا شناسهٔ عددی و لینک دعوت تلگرام را در پاسخ به همان پیام بفرستید.")
	}
	var ref any
	if freePublicChatName.MatchString(parts[0]) {
		ref = parts[0]
	} else if id, err := strconv.ParseInt(telegramDigits(parts[0]), 10, 64); err == nil && id < 0 && id > -(1<<52) {
		ref = id
	}
	if ref == nil {
		return bad("Use a valid group or channel ID.", "شناسهٔ معتبر کانال یا گروه را بفرستید.")
	}
	c := t.config()
	job, cancel := context.WithTimeout(withFreeLane(ctx, freeMembershipAPILane), freeJoinCheckTimeout)
	defer cancel()
	var chat telegramChat
	if t.call(job, c, "getChat", map[string]any{"chat_id": ref}, &chat) != nil || chat.ID >= 0 || !oneOf(chat.Type, "channel", "supergroup", "group") {
		return bad("Could not open that Telegram channel or group. Check the ID and the bot's access.", "دسترسی به این کانال یا گروه ممکن نیست. شناسه و دسترسی ربات را بررسی کنید.")
	}
	link := ""
	if len(parts) == 2 {
		link = parts[1]
	} else if chat.Username != "" {
		link = "https://t.me/" + chat.Username
	}
	if !freeInviteLink.MatchString(link) {
		return bad("Send a valid https://t.me invite link with the chat ID.", "شناسهٔ چت را همراه یک لینک دعوت معتبر https://t.me بفرستید.")
	}
	if path := strings.TrimPrefix(link, "https://t.me/"); !strings.HasPrefix(path, "+") && !strings.HasPrefix(path, "joinchat/") && !strings.EqualFold(path, chat.Username) {
		return bad("The public link must match the chat username.", "لینک عمومی باید با نام کاربری این چت یکسان باشد.")
	}
	title := strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, chat.Title))
	item := freeRequiredChat{ID: chat.ID, Title: title, URL: link}
	if err := t.checkFreeBotMembership(job, c, []freeRequiredChat{item}); err != nil {
		return bad("Make the bot an administrator in that chat and reply again.", "ربات را در این چت ادمین کنید و دوباره به همان پیام پاسخ دهید.")
	}
	if err := t.h.store.changeFreeCatalog(revision, func(c *freeCatalog) error {
		i := slices.IndexFunc(c.JoinChats, func(chat freeRequiredChat) bool { return chat.ID == item.ID })
		if i < 0 {
			if len(c.JoinChats) >= freeMaxJoinChats {
				return errFreeJoinChatLimit
			}
			c.JoinChats = append(c.JoinChats, item)
		} else {
			c.JoinChats[i] = item
		}
		c.JoinRevision++
		return nil
	}); err != nil {
		return err
	}
	_ = t.h.store.Audit("telegram.free_membership_changed", "add")
	return t.freeMembershipMenu(ctx, "", t.tr("Chat saved. Check its invite link before sharing it.\n\n", "چت ذخیره شد. پیش از انتشار، لینک دعوت را امتحان کنید.\n\n"))
}
