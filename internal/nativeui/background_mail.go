package nativeui

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/background"

	"github.com/diver80/postdive/internal/backend"
	"github.com/diver80/postdive/internal/config"
	"github.com/diver80/postdive/internal/mailengine"
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

// backgroundRunRecorder keeps the last background wake-up for the settings.
type backgroundRunRecorder interface {
	RecordBackgroundRun(ctx context.Context, at time.Time, runErr error) error
	LastBackgroundRun(ctx context.Context) (mailengine.BackgroundRun, bool, error)
}

// backgroundCalendar is what the task needs of the engine for calendar reminders.
type backgroundCalendar interface {
	TakeDueEventReminders(ctx context.Context, now time.Time, within time.Duration, skipCalendars []string) ([]backend.CalendarEvent, error)
}

// backgroundReminderWithin is how early a wake-up reminds of an event: until the next wake-up
// (about 15 minutes, often later) and a margin, so a reminder never falls between two.
const backgroundReminderWithin = 20 * time.Minute

type workerPauser interface {
	SetWorkersPaused(paused bool)
}

// setupBackgroundMail registers lifecycle/network pause-resume for IMAP IDLE workers on all
// platforms and schedules Android/iOS WorkManager periodic inbox sync when running on mobile.
// Call before mygo.App.Run.
func (s *desktopShell) setupBackgroundMail(ctx context.Context, opts Options) {
	pauser, _ := opts.Direct.(workerPauser)
	mobile := isMobileOS()
	s.app.bgRunsShown = mobile

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
			s.app.emitUpdate(func() { s.app.noteNetworkOffline(isOffline) })
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
			s.app.noteNetworkOffline(false)
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
		if mobile {
			// Undo-Send waits in memory only: send now, before Android may kill the app.
			s.app.emitUpdate(s.app.FlushPendingSendsOnSuspend)
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

	runTask := func(c context.Context) error {
		if ext, ok := opts.Direct.(interface{ SyncBackgroundExtras(context.Context) }); ok {
			ext.SyncBackgroundExtras(c)
		}
		stateMu.Lock()
		inForeground := foreground
		stateMu.Unlock()
		if bc, ok := opts.Direct.(backgroundCalendar); ok && !inForeground {
			// Before the inboxes: a failing mail server must not swallow a meeting's reminder.
			// Open, the app reminds itself (checkUpcomingMeetingReminders).
			s.notifyBackgroundEvents(c, bc)
		}
		if err := bm.SyncInboxes(c); err != nil {
			return err // runs again later
		}
		stateMu.Lock()
		inForeground = foreground
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
	}
	background.Handle(backgroundMailTask, func(c context.Context) error {
		err := runTask(c)
		if rec, ok := opts.Direct.(backgroundRunRecorder); ok {
			// For the settings: shows whether Android still wakes the app, and how it went.
			_ = rec.RecordBackgroundRun(context.WithoutCancel(c), time.Now(), err)
		}
		return err
	})
	mygo.App.WhenReady(func() {
		go func() {
			if err := background.Schedule(ctx, backgroundMailTask, background.Options{Every: backgroundMailEvery, Network: true}); err != nil {
				log.Printf("postdive: background mail: %v", err)
			}
		}()
	})
}

// notifyBackgroundMail shows one notification per new mail, or one for several. It runs off the
// UI thread: the app may have been started for the task alone, without a window.
func (s *desktopShell) notifyBackgroundMail(ctx context.Context, msgs []mailengine.StoredMessage) {
	opts, ok := s.app.backgroundMailNotification(msgs)
	if !ok || !mygo.NotificationsSupported() {
		return
	}
	if err := mygo.NewNotification(opts).ShowContext(ctx); err != nil {
		log.Printf("postdive notification: %v", err)
	}
}

// notifyBackgroundEvents shows a notification for each event whose reminder is due. With
// notifications off it takes none, so they still remind once switched on again in time.
func (s *desktopShell) notifyBackgroundEvents(ctx context.Context, bc backgroundCalendar) {
	cfg, ok := s.app.backgroundConfig()
	if !ok || !mygo.NotificationsSupported() {
		return
	}
	evs, err := bc.TakeDueEventReminders(ctx, time.Now(), backgroundReminderWithin, cfg.App.HiddenCalendars)
	if err != nil {
		log.Printf("postdive: calendar reminders: %v", err)
		return
	}
	for _, opts := range s.app.backgroundEventNotifications(evs, time.Now()) {
		if err := mygo.NewNotification(opts).ShowContext(ctx); err != nil {
			log.Printf("postdive notification: %v", err)
		}
	}
}

// backgroundConfig is the saved configuration, false when notifications are switched off. Safe
// off the UI thread.
func (a *App) backgroundConfig() (config.Config, bool) {
	if a.opts.ConfigLoader == nil {
		return config.Config{}, true
	}
	cfg, err := a.opts.ConfigLoader()
	if err != nil {
		return config.Config{}, true
	}
	return cfg, cfg.App.NotifyEnabled == nil || *cfg.App.NotifyEnabled
}

// backgroundEventNotifications are the reminders of evs as notifications, which open the
// calendar when tapped (HandleNotificationClick).
func (a *App) backgroundEventNotifications(evs []backend.CalendarEvent, now time.Time) []mygo.NotificationOptions {
	out := make([]mygo.NotificationOptions, 0, len(evs))
	for _, ev := range evs {
		start, err := parseEventTime(ev.Start)
		if err != nil {
			continue
		}
		out = append(out, mygo.NotificationOptions{
			ID:       alertCalPrefix + ev.ID,
			Title:    ev.Title,
			Subtitle: a.eventReminderSubtitle(ev, start, now),
			Body:     ev.Calendar,
			Group:    "postdive-calendar",
		})
	}
	return out
}

// backgroundMailNotification is the notification for mail that arrived in the background, or
// false when there is none or the user turned notifications off. Safe off the UI thread: the
// switch is read from the saved settings, not from the UI's state.
func (a *App) backgroundMailNotification(msgs []mailengine.StoredMessage) (mygo.NotificationOptions, bool) {
	if len(msgs) == 0 {
		return mygo.NotificationOptions{}, false
	}
	if _, on := a.backgroundConfig(); !on {
		return mygo.NotificationOptions{}, false
	}
	if len(msgs) == 1 {
		m := msgs[0]
		return mygo.NotificationOptions{
			ID: alertMailPrefix + m.ID, Title: shortSender(m.From),
			Subtitle: a.tr("Neue E-Mail", "New Email"), Body: m.Subject, Group: "postdive-mail",
		}, true
	}
	return mygo.NotificationOptions{
		ID:       fmt.Sprintf("%s%d", alertMailBatchPrefix, time.Now().UnixNano()),
		Title:    a.trf("%d neue E-Mails", "%d new emails", len(msgs)),
		Subtitle: shortSender(msgs[0].From), Body: msgs[0].Subject, Group: "postdive-mail",
	}, true
}
