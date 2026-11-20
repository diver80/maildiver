package nativeui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"

	"github.com/diver80/maildiver/internal/backend"
	"github.com/diver80/maildiver/internal/triage"
)

// Conversations in the reading pane: the open mail in its place, newer mails of the same
// conversation above it and older ones below – newest on top – each folded to sender, date and
// the start of its text until clicked.

// convItem is a conversation mail unfolded in the reading pane.
type convItem struct {
	detail  *backend.MessageDetail
	body    readerBody
	loading bool
	err     string
}

type conversationUI struct {
	forID string // the open mail the conversation belongs to
	conv  *backend.Conversation
	open  map[string]*convItem
	seq   int
}

// maxThreadForAI bounds the mails of a conversation sent along with an AI action.
const maxThreadForAI = 20

// loadConversation loads the conversation of the open mail (UI thread). Reloading the same mail
// keeps unfolded mails unfolded.
func (a *App) loadConversation(ctx context.Context, id string) {
	if a.conversation.forID != id {
		a.conversation = conversationUI{forID: id, open: map[string]*convItem{}, seq: a.conversation.seq}
	}
	bc, err := a.client()
	if err != nil {
		return
	}
	a.conversation.seq++
	seq := a.conversation.seq
	go func() {
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		conv, err := bc.Conversation(c, id, 100)
		a.emitUpdate(func() {
			if err != nil || a.conversation.forID != id || a.conversation.seq != seq {
				return
			}
			a.conversation.conv = &conv
		})
	}()
}

// conversationSplit returns the other mails of the open mail's conversation: newer and older
// ones, both newest first.
func (a *App) conversationSplit(msg *backend.MessageDetail) (newer, older []backend.ConversationMessage) {
	conv := a.conversation.conv
	if conv == nil || a.conversation.forID != msg.ID || len(conv.Messages) < 2 {
		return nil, nil
	}
	pos := -1
	for i, m := range conv.Messages {
		if m.ID == msg.ID || (msg.MessageID != "" && m.MessageID == msg.MessageID) {
			pos = i
			break
		}
	}
	for i, m := range conv.Messages {
		switch {
		case i == pos:
		case pos >= 0 && i < pos:
			newer = append(newer, m)
		default:
			older = append(older, m)
		}
	}
	return newer, older
}

// toggleConversationMail unfolds or folds one mail of the conversation (UI thread). Unfolding
// is reading it: it is decrypted for the pane and marked read as set.
func (a *App) toggleConversationMail(ctx context.Context, m backend.ConversationMessage) {
	if it, ok := a.conversation.open[m.ID]; ok {
		if !it.loading {
			delete(a.conversation.open, m.ID)
		}
		return
	}
	bc, err := a.client()
	if err != nil {
		return
	}
	it := &convItem{loading: true}
	a.conversation.open[m.ID] = it
	forID := a.conversation.forID
	go func() {
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		detail, err := bc.MessageWithOptions(c, m.ID, backend.MessageOptions{Format: "both", Decrypt: true})
		var body readerBody
		if err == nil {
			body = buildReaderBody(&detail)
		}
		a.emitUpdate(func() {
			if a.conversation.forID != forID {
				return
			}
			it.loading = false
			if err != nil {
				it.err = a.errText(err)
				return
			}
			it.detail, it.body = &detail, body
			if detail.Unread && a.cfg.App.MarkRead != "never" && !a.readOnly && a.cfg.Mailbox.CanWrite(detail.AccountID) {
				a.markReadSilently(ctx, detail.ID)
				detail.Unread, detail.Read = false, true
				a.markConversationRead(detail.ID)
			}
		})
	}()
}

func (a *App) markConversationRead(id string) {
	if conv := a.conversation.conv; conv != nil {
		for i := range conv.Messages {
			if conv.Messages[i].ID == id {
				conv.Messages[i].Unread, conv.Messages[i].Read = false, true
			}
		}
	}
}

// renderConversationGroup draws folded/unfolded conversation mails (UI thread).
func (a *App) renderConversationGroup(c *ui.Context, key, title string, msgs []backend.ConversationMessage) {
	if len(msgs) == 0 {
		return
	}
	t := c.Theme()
	ui.Column(c.Key("conv-" + key)).FillWidth().Gap(4).Children(func() {
		ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(6).Children(func() {
			ui.Icon(c, iconThread).Size(13, 13).TextColor(t.TextMuted)
			ui.Text(c, title).FontSize(11.5).Bold().TextColor(t.TextMuted)
		})
		for _, m := range msgs {
			a.renderConversationMail(c, m)
		}
	})
}

func (a *App) renderConversationMail(c *ui.Context, m backend.ConversationMessage) {
	t := c.Theme()
	it := a.conversation.open[m.ID]
	ui.Column(c.Key("conv-mail-"+m.ID)).FillWidth().Radius(8).Border(1, t.Border).Children(func() {
		head := ui.ButtonBase(c.Key("conv-head-"+m.ID)).FillWidth().Padding(7, 10).Radius(8).
			Label(a.tr("Mail auf- oder zuklappen", "Unfold or fold the email"))
		head.Children(func() {
			ui.Column(c).FillWidth().Gap(2).Children(func() {
				ui.Row(c).FillWidth().AlignItems(ui.Center).Gap(6).Children(func() {
					if m.Unread {
						statusDot(c, t.Accent, 6)
					}
					from := ui.Text(c, shortSender(m.From)).FontSize(12).Grow(1).MinWidth(0).SingleLine()
					if m.Unread {
						from.Bold()
					}
					if m.Folder != "" && !strings.EqualFold(m.Folder, "INBOX") {
						miniTag(c, m.Folder, t.TextMuted, t.Surface)
					}
					ui.Text(c, formatRelTimeLang(m.Date, a.uiLang())).FontSize(11).TextColor(t.TextMuted).Shrink(0)
				})
				if it == nil && m.Snippet != "" {
					ui.Text(c, m.Snippet).FontSize(11.5).TextColor(t.TextMuted).MaxLines(2)
				}
			})
		})
		if head.Clicked() {
			a.toggleConversationMail(context.Background(), m)
		}
		if it == nil {
			return
		}
		ui.Column(c).FillWidth().Padding(4, 12, 10, 12).Gap(6).Children(func() {
			switch {
			case it.loading:
				ui.Spinner(c)
			case it.err != "":
				ui.Text(c, it.err).FontSize(12).TextColor(t.Danger)
			case it.detail != nil:
				md := it.body.Markdown
				if strings.TrimSpace(md) == "" {
					ui.Text(c, a.tr("(Kein Textinhalt)", "(No text content)")).FontSize(12).TextColor(t.TextMuted)
				} else {
					markdown.View(c.Key("conv-body-"+m.ID), md, a.bodyMarkdownOpts()).FillWidth().MaxWidth(900)
				}
				ui.Row(c).Gap(6).Children(func() {
					if ui.Button(c.Key("conv-open-"+m.ID), a.tr("Öffnen", "Open")).Clicked() {
						a.SelectMessage(context.Background(), m.ID)
					}
				})
			}
		})
	})
}

// threadForAI returns the open mail's conversation in reading order for an AI action (nil when
// the mail stands alone). Runs off the UI thread.
func threadForAI(ctx context.Context, bc *backend.Client, id string, convSize int) []triage.MessageInput {
	if bc == nil || convSize < 2 {
		return nil
	}
	c, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	msgs, err := bc.MessageThread(c, id, maxThreadForAI)
	if err != nil || len(msgs) < 2 {
		return nil
	}
	return msgs
}

// conversationSize is how many mails the open mail's conversation holds (0 = unknown).
func (a *App) conversationSize(id string) int {
	if conv := a.conversation.conv; conv != nil && a.conversation.forID == id {
		return len(conv.Messages)
	}
	return 0
}

// threadBadge shows how many mails a list row's conversation holds.
func threadBadge(c *ui.Context, n int) {
	t := c.Theme()
	ui.Row(c).Gap(2).AlignItems(ui.Center).Shrink(0).Children(func() {
		ui.Icon(c, iconThread).Size(11, 11).TextColor(t.TextMuted)
		ui.Text(c, fmt.Sprintf("%d", n)).FontSize(10.5).TextColor(t.TextMuted)
	})
}
