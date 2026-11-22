package nativeui

import (
	"context"
	"fmt"
	"log"
	"sync"
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
		stateMu       sync.Mutex
		pauseMu       sync.Mutex
		foreground    = !mobile
		lifecycleSeen bool
		suspended     bool
		online        = true
		resumeTimer   *time.Timer
		timerGen      uint64
	)

	desiredRunLocked := func() bool {
		if !online || suspended {
			return false
		}
		if mobile && !foreground {
			return false
		}
		return true
	}

	// applyWorkersState serializes SetWorkersPaused calls under pauseMu and re-reads the latest
	// desired state under stateMu so concurrent pause/resume transitions can never invert order.
	applyWorkersState := func() {
		if pauser == nil {
			return
		}
		pauseMu.Lock()
		defer pauseMu.Unlock()
		stateMu.Lock()
		wantPaused := !desiredRunLocked()
		stateMu.Unlock()
		pauser.SetWorkersPaused(wantPaused)
	}

	reconcileWorkers := func(settle time.Duration, forceRecycle bool) {
		if pauser == nil {
			return
		}
		stateMu.Lock()
		if resumeTimer != nil {
			resumeTimer.Stop()
			resumeTimer = nil
		}
		timerGen++
		gen := timerGen
		shouldRun := desiredRunLocked()
		isOffline := !online
		if !shouldRun {
			stateMu.Unlock()
			go applyWorkersState()
			s.app.emitUpdate(func() {
				s.app.networkOffline = isOffline
				if isOffline {
					s.app.clearTransientSyncErrors()
				}
			})
			return
		}
		if settle > 0 {
			resumeTimer = time.AfterFunc(settle, func() {
				stateMu.Lock()
				stale := timerGen != gen
				stateMu.Unlock()
				if !stale {
					applyWorkersState()
				}
			})
		}
		stateMu.Unlock()

		// Immediately clear stale "no such host" / socket errors from the previous suspend/offline
		// window and wait briefly for Android's netd / OS routing to unfreeze before dialing IMAP.
		s.app.emitUpdate(func() {
			s.app.networkOffline = false
			s.app.clearTransientSyncErrors()
		})
		if forceRecycle && settle > 0 {
			// Wi-Fi <-> cellular handoff: drop dead sockets immediately; timer resumes cleanly.
			go func() {
				pauseMu.Lock()
				pauser.SetWorkersPaused(true)
				pauseMu.Unlock()
			}()
			return
		}
		if settle <= 0 {
			go applyWorkersState()
		}
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
		stateMu.Lock()
		foreground = true
		lifecycleSeen = true
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
		reconcileWorkers(settle, false)
	}
	onSleep := func() {
		stateMu.Lock()
		if mobile {
			foreground = false
		}
		lifecycleSeen = true
		suspended = true
		stateMu.Unlock()
		if seen != nil {
			seen()
		}
		reconcileWorkers(0, false)
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
		handoff := mobile && prev && st.Online
		if st.Online != prev || !st.Online || handoff {
			settle := 250 * time.Millisecond
			if mobile {
				settle = 500 * time.Millisecond
			}
			reconcileWorkers(settle, handoff)
		}
	})

	mygo.App.WhenReady(func() {
		go func() {
			st := mygo.Network.Status()
			active := !mobile || mygo.App.IsActive()
			stateMu.Lock()
			online = st.Online
			if mobile && !lifecycleSeen && active {
				foreground = true
			}
			stateMu.Unlock()
			settle := time.Duration(0)
			if mobile {
				settle = 300 * time.Millisecond
			}
			reconcileWorkers(settle, false)
		}()
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
		stateMu.Lock()
		inForeground := foreground
		stateMu.Unlock()
		if inForeground {
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
