package mailengine

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/diver80/postdive/internal/backend"
	"github.com/diver80/postdive/internal/config"
	"github.com/diver80/postdive/internal/secretstore"
)

func TestEventICSParticipantsRoundTrip(t *testing.T) {
	ev := StoredEvent{
		ID: "local::evt-1", CalendarID: LocalCalendarID, UID: "evt-1@postdive.local", Title: "Planung",
		StartISO: "2026-12-01T10:00:00Z", EndISO: "2026-12-01T11:00:00Z",
		StartUnix: 1796112000, EndUnix: 1796115600,
		Organizer: "Ich <ich@example.org>",
		Attendees: []backend.EventAttendee{
			{Email: "anna@example.org", Name: "Anna Muster", Status: "accepted"},
			{Email: "ben@example.org", Status: "needsAction"},
		},
		Sequence: 2,
	}
	stored := ExportEventsICS("Persönlich", []StoredEvent{ev})
	if !strings.Contains(stored, "SCHEDULE-AGENT=CLIENT") || strings.Contains(stored, "METHOD:") {
		t.Fatalf("calendar copy must keep the server from scheduling and carry no method:\n%s", stored)
	}
	back, err := ParseICS(LocalCalendarID, "Persönlich", false, strings.NewReader(stored))
	if err != nil || len(back) != 1 {
		t.Fatalf("parse back: %v %d", err, len(back))
	}
	got := back[0]
	if got.Organizer != ev.Organizer || got.Sequence != 2 || len(got.Attendees) != 2 ||
		got.Attendees[0] != ev.Attendees[0] || got.Attendees[1] != ev.Attendees[1] {
		t.Fatalf("participants lost in the round trip: %+v", got)
	}

	req := buildEventsICS("", []StoredEvent{ev}, "REQUEST")
	if !strings.Contains(req, "METHOD:REQUEST") || strings.Contains(req, "BEGIN:VALARM") || strings.Contains(req, "SCHEDULE-AGENT") || !strings.Contains(req, "RSVP=TRUE") {
		t.Fatalf("unexpected invitation payload:\n%s", req)
	}
	cancel := buildEventsICS("", []StoredEvent{occurrenceOf(ev, "2026-12-08T10:00:00Z")}, "CANCEL")
	if !strings.Contains(cancel, "METHOD:CANCEL") || !strings.Contains(cancel, "STATUS:CANCELLED") || !strings.Contains(cancel, "RECURRENCE-ID:20261208T100000Z") {
		t.Fatalf("unexpected cancellation payload:\n%s", cancel)
	}
}

func TestAttendeeListHelpers(t *testing.T) {
	in := []backend.EventAttendee{
		{Email: "Anna <anna@example.org>"}, {Email: "anna@example.org"}, {Email: "nope"},
		{Email: "ich@example.org"}, {Email: "ben@example.org", Name: "ben@example.org"},
	}
	clean := cleanAttendees(in, "ICH@example.org")
	if len(clean) != 2 || clean[0].Email != "anna@example.org" || clean[1].Name != "" {
		t.Fatalf("cleanAttendees: %+v", clean)
	}
	old := []backend.EventAttendee{{Email: "anna@example.org", Status: "accepted"}, {Email: "carl@example.org", Status: "declined"}}
	upd := keepAttendeeAnswers(old, []backend.EventAttendee{{Email: "ANNA@example.org"}, {Email: "dora@example.org"}})
	if upd[0].Status != "accepted" || upd[1].Status != "needsAction" {
		t.Fatalf("keepAttendeeAnswers: %+v", upd)
	}
	if rm := removedAttendees(old, upd); len(rm) != 1 || rm[0].Email != "carl@example.org" {
		t.Fatalf("removedAttendees: %+v", rm)
	}
}

// fakeSMTP is a plain SMTP server on loopback that keeps what it receives.
type fakeSMTP struct {
	addr string
	mu   sync.Mutex
	got  []fakeMail
}

type fakeMail struct {
	rcpts []string
	data  string
}

func startFakeSMTP(t *testing.T) *fakeSMTP {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeSMTP{addr: ln.Addr().String()}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go f.serve(conn)
		}
	}()
	return f
}

func (f *fakeSMTP) serve(conn net.Conn) {
	defer conn.Close()
	r := bufio.NewReader(conn)
	say := func(s string) { _, _ = conn.Write([]byte(s + "\r\n")) }
	say("220 fake")
	var cur fakeMail
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			say("250-fake")
			say("250 AUTH PLAIN")
		case strings.HasPrefix(cmd, "AUTH"):
			say("235 ok")
		case strings.HasPrefix(cmd, "MAIL FROM"):
			cur = fakeMail{}
			say("250 ok")
		case strings.HasPrefix(cmd, "RCPT TO"):
			addr := strings.TrimSpace(line[len("RCPT TO:"):])
			addr = strings.Trim(strings.Fields(addr)[0], "<>")
			cur.rcpts = append(cur.rcpts, addr)
			say("250 ok")
		case cmd == "DATA":
			say("354 go")
			var b strings.Builder
			for {
				l, err := r.ReadString('\n')
				if err != nil {
					return
				}
				if l == ".\r\n" {
					break
				}
				b.WriteString(l)
			}
			cur.data = b.String()
			f.mu.Lock()
			f.got = append(f.got, cur)
			f.mu.Unlock()
			say("250 queued")
		case cmd == "QUIT":
			say("221 bye")
			return
		default:
			say("250 ok")
		}
	}
}

func (f *fakeSMTP) mails() []fakeMail {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]fakeMail(nil), f.got...)
}

// calendarPart is the decoded text/calendar part of a mail built by BuildCalendarMIME.
func calendarPart(t *testing.T, data string) string {
	t.Helper()
	i := strings.Index(data, "Content-Type: text/calendar")
	if i < 0 {
		t.Fatalf("no calendar part:\n%s", data)
	}
	rest := data[i:]
	body := rest[strings.Index(rest, "\r\n\r\n")+4:]
	if j := strings.Index(body, "\r\n--"); j >= 0 {
		body = body[:j]
	}
	dec, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(body, "\r\n", ""))
	if err != nil {
		t.Fatalf("decode calendar part: %v", err)
	}
	return string(dec)
}

func newInviteTestEngine(t *testing.T, smtpAddr string) *Engine {
	t.Helper()
	host, port, _ := net.SplitHostPort(smtpAddr)
	var p int
	for _, c := range port {
		p = p*10 + int(c-'0')
	}
	cfg := config.Default()
	cfg.Direct.Accounts = []config.DirectAccount{{
		ID: "acc-1", Name: "Ich", DisplayName: "Ich Selbst", Email: "ich@example.org", Enabled: true,
		IMAPHost: "127.0.0.1", IMAPPort: 1, IMAPTLS: "tls",
		SMTPHost: host, SMTPPort: p, SMTPTLS: "plain",
	}}
	secrets := secretstore.NewMemoryStore()
	eng, err := New(Options{
		DBPath:       filepath.Join(t.TempDir(), "postdive.db"),
		Secrets:      secrets,
		ConfigLoader: func() (config.Config, error) { return cfg, nil },
		ConfigSaver:  func(next config.Config) error { cfg = next; return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = eng.Close() })
	if err := secrets.Set(eng.accountSecretKey("acc-1"), "pw"); err != nil {
		t.Fatal(err)
	}
	return eng
}

func TestLocalEventInvitationsUpdatesAndCancellation(t *testing.T) {
	smtp := startFakeSMTP(t)
	eng := newInviteTestEngine(t, smtp.addr)
	ctx := context.Background()

	raw, err := eng.Call(ctx, "calendar.create", backend.CreateCalendarEventRequest{
		Title: "Planung", Start: "2026-12-01T10:00:00Z", End: "2026-12-01T11:00:00Z",
		Attendees: []backend.EventAttendee{
			{Email: "anna@example.org", Name: "Anna"}, {Email: "ben@example.org"}, {Email: "ich@example.org"},
		},
		SendInvites: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var res struct{ Event backend.CalendarEvent }
	_ = json.Unmarshal(raw, &res)
	ev := res.Event
	if ev.InviteError != "" || !ev.CanNotify || ev.Organizer != "Ich Selbst <ich@example.org>" || len(ev.Attendees) != 2 {
		t.Fatalf("unexpected created event: %+v", ev)
	}
	mails := smtp.mails()
	if len(mails) != 1 || strings.Join(mails[0].rcpts, ",") != "anna@example.org,ben@example.org" {
		t.Fatalf("expected one invitation to both attendees, got %+v", mails)
	}
	ics := calendarPart(t, mails[0].data)
	if !strings.Contains(mails[0].data, "method=REQUEST") || !strings.Contains(ics, "METHOD:REQUEST") ||
		!strings.Contains(ics, "ORGANIZER;CN=Ich Selbst:mailto:ich@example.org") || !strings.Contains(mails[0].data, "Subject: Einladung: Planung") {
		t.Fatalf("unexpected invitation:\n%s\n%s", mails[0].data, ics)
	}

	// Ben is taken off, Carl added: the remaining get the new version, Ben a cancellation.
	raw, err = eng.Call(ctx, "calendar.update", map[string]any{
		"calendarId": LocalCalendarID, "id": ev.ID, "title": "Planung (neu)", "notify": true,
		"attendees": []map[string]any{{"email": "anna@example.org", "name": "Anna"}, {"email": "carl@example.org"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(raw, &res)
	if res.Event.InviteError != "" || len(res.Event.Attendees) != 2 {
		t.Fatalf("unexpected updated event: %+v", res.Event)
	}
	mails = smtp.mails()
	if len(mails) != 3 {
		t.Fatalf("expected update and cancellation mails, got %d", len(mails))
	}
	upd, canc := calendarPart(t, mails[1].data), calendarPart(t, mails[2].data)
	if strings.Join(mails[1].rcpts, ",") != "anna@example.org,carl@example.org" || !strings.Contains(upd, "SEQUENCE:1") || !strings.Contains(upd, "SUMMARY:Planung (neu)") {
		t.Fatalf("unexpected update %v:\n%s", mails[1].rcpts, upd)
	}
	if strings.Join(mails[2].rcpts, ",") != "ben@example.org" || !strings.Contains(canc, "METHOD:CANCEL") {
		t.Fatalf("unexpected cancellation %v:\n%s", mails[2].rcpts, canc)
	}

	// Anna accepts; an older answer opened later does not undo that.
	reply := func(stamp, partstat string) string {
		return "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Test//EN\r\nMETHOD:REPLY\r\nBEGIN:VEVENT\r\nUID:" + ev.ID[len(LocalCalendarID)+2:] +
			"\r\nDTSTAMP:" + stamp + "\r\nDTSTART:20261201T100000Z\r\nSUMMARY:Planung (neu)\r\nATTENDEE;CN=Anna;PARTSTAT=" + partstat +
			":mailto:anna@example.org\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	}
	inv := eng.icsInviteOf(ctx, backend.MessageDetail{}, reply("20261126T120000Z", "ACCEPTED"))
	if inv == nil || inv.Method != "REPLY" || inv.ResponseStatus != "accepted" || inv.Attendees[0].Email != "anna@example.org" {
		t.Fatalf("unexpected reply card: %+v", inv)
	}
	_ = eng.icsInviteOf(ctx, backend.MessageDetail{}, reply("20261126T080000Z", "DECLINED"))
	stored, err := eng.store.GetEvent(ctx, LocalCalendarID, ev.ID)
	if err != nil || stored.Attendees[0].Status != "accepted" || stored.Attendees[1].Status != "needsAction" {
		t.Fatalf("answer not recorded or overwritten by an older one: %+v err=%v", stored.Attendees, err)
	}

	// Deleting with notify tells everybody still invited.
	if _, err := eng.Call(ctx, "calendar.delete", map[string]any{"calendarId": LocalCalendarID, "id": ev.ID, "notify": true}); err != nil {
		t.Fatal(err)
	}
	mails = smtp.mails()
	if len(mails) != 4 || strings.Join(mails[3].rcpts, ",") != "anna@example.org,carl@example.org" || !strings.Contains(calendarPart(t, mails[3].data), "STATUS:CANCELLED") {
		t.Fatalf("expected a cancellation to the remaining attendees, got %d mails", len(mails))
	}
	if acts, _ := eng.ListActivity(ctx, ActivityQuery{Kinds: []string{ActivitySend}}); len(acts) != 4 {
		t.Fatalf("each invitation mail should be in the activity log, got %d", len(acts))
	}
}

func TestInviteCardFallsBackToRecipients(t *testing.T) {
	eng := newInviteTestEngine(t, "127.0.0.1:1")
	invite := "BEGIN:VCALENDAR\r\nVERSION:2.0\r\nPRODID:-//Test//EN\r\nMETHOD:REQUEST\r\nBEGIN:VEVENT\r\nUID:x-1\r\nDTSTAMP:20261126T120000Z\r\nDTSTART:20261201T100000Z\r\nDTEND:20261201T110000Z\r\nSUMMARY:Review\r\nORGANIZER;CN=Olga:mailto:olga@example.org\r\nEND:VEVENT\r\nEND:VCALENDAR\r\n"
	detail := backend.MessageDetail{MessageHeader: backend.MessageHeader{
		ID: "m-1", From: "Olga <olga@example.org>", To: []string{"Ich <ich@example.org>", "Anna <anna@example.org>"}, CC: []string{"olga@example.org"},
	}}
	inv := eng.icsInviteOf(context.Background(), detail, invite)
	if inv == nil || !inv.AttendeesFromMail || len(inv.Attendees) != 2 || inv.Attendees[1].Email != "anna@example.org" || inv.Method != "REQUEST" {
		t.Fatalf("expected the mail's recipients without the organizer: %+v", inv)
	}
}
