package mcp

import (
	"fmt"
	"net/mail"
	"strings"
	"time"

	"github.com/diver80/maildiver/internal/backend"
	"github.com/diver80/maildiver/internal/copilot"
)

// A draft that answers or forwards a mail gets what a mail client fills in: the account the
// mail arrived in, its recipients, "Re:"/"Fwd:" and the original text. The engine only links
// the draft to the conversation, so this is done here. An encrypted original is never quoted
// or attached: its text is not read over MCP.

const maxQuoteRunes = 20000

func addrKey(raw string) string {
	if a, err := mail.ParseAddress(raw); err == nil {
		return strings.ToLower(strings.TrimSpace(a.Address))
	}
	return strings.ToLower(strings.TrimSpace(raw))
}

func splitAddrList(raw string) []string {
	list, err := mail.ParseAddressList(raw)
	if err != nil {
		if raw = strings.TrimSpace(raw); raw != "" {
			return []string{raw}
		}
		return nil
	}
	out := make([]string, 0, len(list))
	for _, a := range list {
		out = append(out, a.String())
	}
	return out
}

func withoutOwn(list []string, own map[string]bool) []string {
	var out []string
	for _, raw := range list {
		if raw = strings.TrimSpace(raw); raw != "" && !own[addrKey(raw)] {
			out = append(out, raw)
		}
	}
	return out
}

// replyRecipients is where an answer to orig goes: its Reply-To, else its sender; a mail the
// user sent himself is answered to its recipients. replyAll adds everyone else in copy.
func replyRecipients(orig backend.MessageDetail, own map[string]bool, replyAll bool) (to, cc []string) {
	if list := splitAddrList(orig.Headers["reply-to"]); len(withoutOwn(list, own)) > 0 {
		to = list
	} else if own[addrKey(orig.From)] {
		to = withoutOwn(orig.To, own)
	}
	if len(to) == 0 && strings.TrimSpace(orig.From) != "" {
		to = []string{orig.From}
	}
	if !replyAll {
		return to, nil
	}
	seen := map[string]bool{addrKey(orig.From): true}
	for _, r := range to {
		seen[addrKey(r)] = true
	}
	for _, raw := range append(append([]string{}, orig.To...), orig.CC...) {
		raw = strings.TrimSpace(raw)
		k := addrKey(raw)
		if raw == "" || k == "" || own[k] || seen[k] {
			continue
		}
		seen[k] = true
		cc = append(cc, raw)
	}
	return to, cc
}

func prefixedSubject(subject string, prefix string, known ...string) string {
	low := strings.ToLower(strings.TrimSpace(subject))
	for _, k := range known {
		if strings.HasPrefix(low, k) {
			return subject
		}
	}
	return prefix + subject
}

func mailDate(iso string) string {
	if t, err := time.Parse(time.RFC3339, iso); err == nil {
		return t.Local().Format("2006-01-02 15:04")
	}
	return iso
}

func originalText(orig backend.MessageDetail) string {
	if !orig.Security.Readable() || orig.Security.Encrypted {
		return ""
	}
	text := strings.TrimSpace(strings.ReplaceAll(orig.BodyText, "\r\n", "\n"))
	if len([]rune(text)) > maxQuoteRunes {
		text = copilot.TruncateRunes(text, maxQuoteRunes) + "\n[…]"
	}
	return text
}

// replyQuote is the original below an answer ("On … wrote:" and "> " lines).
func replyQuote(orig backend.MessageDetail) string {
	text := originalText(orig)
	if text == "" {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\n\nOn %s, %s wrote:\n", mailDate(orig.Date), orig.From)
	for _, line := range strings.Split(text, "\n") {
		switch {
		case strings.HasPrefix(line, ">"):
			b.WriteString(">" + line + "\n")
		case line == "":
			b.WriteString(">\n")
		default:
			b.WriteString("> " + line + "\n")
		}
	}
	return b.String()
}

// forwardBlock is the original below a forward, with its header lines.
func forwardBlock(orig backend.MessageDetail) string {
	var b strings.Builder
	b.WriteString("\n\n---------- Forwarded message ----------\n")
	b.WriteString("From: " + orig.From + "\n")
	if len(orig.To) > 0 {
		b.WriteString("To: " + strings.Join(orig.To, ", ") + "\n")
	}
	b.WriteString("Date: " + mailDate(orig.Date) + "\n")
	b.WriteString("Subject: " + orig.Subject + "\n\n")
	b.WriteString(originalText(orig))
	return b.String()
}
