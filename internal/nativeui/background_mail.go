package nativeui

import (
	"context"
	"fmt"
	"log"
	"sync"
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

type workerPauser interface {
	SetWorkersPaused(paused bool)
}

// setupBackgroundMail registers lifecycle/network pause-resume for IMAP IDLE workers on all
// platforms and schedules Android/iOS WorkManager periodic inbox sync when running on mobile.
// Call before mygo.App.Run.
func (s *desktopShell) setupBackgroundMail(ctx context.Context, opts Options) {
	pauser, _ := opts.Direct.(workerPauser)
	mobile := isMobileOS()

	var (
		stateMu     sync.Mutex
		foreground  atomic.Bool
		suspended   bool
		online      = true
		resumeTimer *time.Timer
	)
	if !mobile {
		foreground.Store(true)
	}

	reconcileWorkers := func(settle time.Duration) {
		if pauser == nil {
			return
		}
		stateMu.Lock()
		if resumeTimer != nil {
			resumeTimer.Stop()
			resumeTimer = nil
		}
		shouldRun := online && !suspended
		if mobile {
			shouldRun = shouldRun && foreground.Load()
		}
		isOffline := !online
		if !shouldRun {
			stateMu.Unlock()
			pauser.SetWorkersPaused(true)
			s.app.emitUpdate(func() {
				s.app.networkOffline = isOffline
				if isOffline {
					s.app.clearTransientSyncErrors()
				}
			})
			return
		}
		// Immediately clear stale "no such host" / socket errors from the previous suspend/offline
		// window and wait briefly for Android's netd / OS routing to unfreeze before dialing IMAP.
		s.app.emitUpdate(func() {
			s.app.networkOffline = false
			s.app.clearTransientSyncErrors()
		})
		if settle <= 0 {
			stateMu.Unlock()
			go pauser.SetWorkersPaused(false)
			return
		}
		resumeTimer = time.AfterFunc(settle, func() {
			stateMu.Lock()
			ok := online && !suspended && (!mobile || foreground.Load())
			stateMu.Unlock()
			if ok {
				pauser.SetWorkersPaused(false)
			}
		})
		stateMu.Unlock()
	}

	var seen func()
	if bm, ok := opts.Direct.(backgroundMail); ok && mobile {
		seen = func() {
			go func() {
				c, cancel := context.WithTimeout(ctx, 10*time.Second)
				defer cancel()
				_ = bm.MarkInboxSeen(c)
			}()
		}
	}

	onWake := func() {
		foreground.Store(true)
		stateMu.Lock()
		suspended = false
		stateMu.Unlock()
		if seen != nil {
			seen()
		}
		settle := 250 * time.Millisecond
		if mobile {
			// Give Android netd / eBPF UID firewall a moment to restore network after onResume.
			settle = 500 * time.Millisecond
		}
		reconcileWorkers(settle)
	}
	onSleep := func() {
		if mobile {
			foreground.Store(false)
		}
		stateMu.Lock()
		suspended = true
		stateMu.Unlock()
		if seen != nil {
			seen()
		}
		reconcileWorkers(0)
	}

	mygo.App.OnActivate(func(bool) { onWake() })
	mygo.App.OnDidBecomeActive(func() {
		if mobile {
			onWake()
		}
	})
	mygo.Power.OnResume(func() { onWake() })
	mygo.Power.OnSuspend(func() { onSleep() })
	mygo.Network.OnChanged(func(st mygo.NetworkStatus) {
		stateMu.Lock()
		prev := online
		online = st.Online
		stateMu.Unlock()
		if st.Online != prev || !st.Online {
			settle := 250 * time.Millisecond
			if mobile {
				settle = 500 * time.Millisecond
			}
			reconcileWorkers(settle)
		}
	})

	mygo.App.WhenReady(func() {
		if mobile {
			go func() {
				st := mygo.Network.Status()
				stateMu.Lock()
				online = st.Online
				stateMu.Unlock()
				if mygo.App.IsActive() {
					foreground.Store(true)
				}
				reconcileWorkers(300 * time.Millisecond)
			}()
		}
	})

	if !mobile {
		return
	}
	bm, ok := opts.Direct.(backgroundMail)
	if !ok {
		return
	}

	background.Handle(backgroundMailTask, func(c context.Context) error {
		if ext, ok := opts.Direct.(interface{ SyncBackgroundExtras(context.Context) }); ok {
			ext.SyncBackgroundExtras(c)
		}
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
