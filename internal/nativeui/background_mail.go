package nativeui

import (
	"context"
	"fmt"
	"log"
	"sync/atomic"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/background"

	"github.com/diver80/maildiver/internal/mailengine"
)

// Mail while the app is not open (phones). Android pauses an app in the background, so its IMAP
// IDLE connection stops; the system's job scheduler (WorkManager, through mygo's background
// plugin) wakes it about every 15 minutes with a network instead. The task syncs the inboxes and
// shows a notification for what arrived since the user last had the app open.

const (
	backgroundMailTask  = "mail-sync"
	backgroundMailEvery = 15 * time.Minute // the shortest period Android allows
)

// backgroundMail is what the task needs of the mail engine.
type backgroundMail interface {
	SyncInboxes(ctx context.Context) error
	TakeNewInboxMail(ctx context.Context) ([]mailengine.StoredMessage, error)
	MarkInboxSeen(ctx context.Context) error
}

// setupBackgroundMail registers the task and keeps the "seen" watermark with the app's
// foreground; call it before mygo.App.Run.
func (s *desktopShell) setupBackgroundMail(ctx context.Context, opts Options) {
	if !isMobileOS() {
		return
	}
	bm, ok := opts.Direct.(backgroundMail)
	if !ok {
		return
	}
	var foreground atomic.Bool
	seen := func() {
		go func() {
			c, cancel := context.WithTimeout(ctx, 10*time.Second)
			defer cancel()
			_ = bm.MarkInboxSeen(c)
		}()
	}
	mygo.App.OnActivate(func(bool) { foreground.Store(true); seen() })
	mygo.Power.OnResume(func() { foreground.Store(true); seen() })
	mygo.Power.OnSuspend(func() { foreground.Store(false); seen() })

	background.Handle(backgroundMailTask, func(c context.Context) error {
		if err := bm.SyncInboxes(c); err != nil {
			return err // runs again later
		}
		if foreground.Load() {
			// Open: the app's own new-mail alerts have shown them.
			return bm.MarkInboxSeen(c)
		}
		msgs, err := bm.TakeNewInboxMail(c)
		if err != nil {
			return err
		}
		s.notifyBackgroundMail(c, msgs)
		return nil
	})
	mygo.App.WhenReady(func() {
		go func() {
			if err := background.Schedule(ctx, backgroundMailTask, background.Options{Every: backgroundMailEvery, Network: true}); err != nil {
				log.Printf("maildiver: background mail: %v", err)
			}
		}()
	})
}

// notifyBackgroundMail shows one notification per new mail, or one for several. It runs off the
// UI thread: the app may have been started for the task alone, without a window.
func (s *desktopShell) notifyBackgroundMail(ctx context.Context, msgs []mailengine.StoredMessage) {
	if len(msgs) == 0 || !mygo.NotificationsSupported() {
		return
	}
	a := s.app
	var opts mygo.NotificationOptions
	if len(msgs) == 1 {
		m := msgs[0]
		opts = mygo.NotificationOptions{
			ID: alertMailPrefix + m.ID, Title: shortSender(m.From),
			Subtitle: a.tr("Neue E-Mail", "New Email"), Body: m.Subject, Group: "maildiver-mail",
		}
	} else {
		opts = mygo.NotificationOptions{
			ID:       fmt.Sprintf("%s%d", alertMailBatchPrefix, time.Now().UnixNano()),
			Title:    a.trf("%d neue E-Mails", "%d new emails", len(msgs)),
			Subtitle: shortSender(msgs[0].From), Body: msgs[0].Subject, Group: "maildiver-mail",
		}
	}
	if err := mygo.NewNotification(opts).ShowContext(ctx); err != nil {
		log.Printf("maildiver notification: %v", err)
	}
}
