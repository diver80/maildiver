package nativeui

import (
	"context"
	"log"
	"runtime"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type desktopShell struct {
	app       *App
	ctx       context.Context
	mainWin   *mygo.Window
	flyoutWin *mygo.Window
	tray      *mygo.Tray
	quitting  bool
	trayIcon  []byte
	appIcon   []byte
	flyBlurAt time.Time
	mu        sync.Mutex
}

// Run starts the native mygo desktop UI (main window, menu bar status widget, and native notifications).
func Run(ctx context.Context, opts Options) error {
	mygo.App.SetName("OmaThunder")
	if runtime.GOOS == "linux" {
		_ = mygo.App.RequestSingleInstanceLock()
	}

	app := NewApp(opts)
	s := &desktopShell{
		app:      app,
		ctx:      ctx,
		trayIcon: trayIconPNG(),
		appIcon:  RenderAppIconPNG(256),
	}

	mygo.App.SetMenu(s.appMenu())
	mygo.App.OnBeforeQuit(func(*mygo.QuitEvent) { s.quitting = true })
	// With a tray the app keeps running in the menu bar; without one (--no-tray or tray
	// creation failed) closing the last window must quit instead of leaving an invisible process.
	mygo.App.OnWindowAllClosed(func() {
		if s.tray == nil {
			mygo.App.Quit()
		}
	})
	mygo.App.OnActivate(func(hasVisibleWindows bool) {
		if !hasVisibleWindows && s.mainWin != nil {
			s.showMain()
		}
	})
	mygo.App.OnNotificationClick(func(id string) {
		// App state is owned by the UI thread: post instead of touching it from the callback.
		app.emitUpdate(func() {
			s.showMain()
			app.HandleNotificationClick(id)
		})
	})

	mygo.App.WhenReady(func() {
		s.ready(opts)
	})

	go func() {
		<-ctx.Done()
		mygo.RunOnMain(func() {
			s.quitting = true
			if s.tray != nil {
				s.tray.Destroy()
			}
			mygo.App.Quit()
		})
	}()

	return mygo.App.Run()
}

func (s *desktopShell) ready(opts Options) {
	if len(s.appIcon) > 0 {
		_ = mygo.App.Dock.SetIcon(s.appIcon)
	}
	s.mainWin = mygo.NewWindow(mygo.WindowOptions{
		Title:     s.mainTitle(),
		Width:     1380,
		Height:    860,
		MinWidth:  960,
		MinHeight: 620,
		Hidden:    opts.StartHidden,
		StateKey:  "omathunder-main",
		Content:   ui.View(s.app.RenderMain),
	})
	if len(s.appIcon) > 0 {
		_ = s.mainWin.SetIcon(s.appIcon)
	}
	s.mainWin.OnClose(func(e *mygo.CloseEvent) {
		if !s.quitting && !opts.DisableTray && s.tray != nil {
			e.PreventDefault()
			s.hideMain()
		}
	})
	if opts.StartHidden && runtime.GOOS == "darwin" {
		mygo.App.SetActivationPolicy(mygo.ActivationPolicyAccessory)
	}

	s.app.SetHooks(
		func(fn func()) {
			if s.mainWin != nil && !s.mainWin.IsDestroyed() {
				s.mainWin.Update(fn)
			} else {
				mygo.RunOnMain(fn)
			}
			if s.flyoutWin != nil && !s.flyoutWin.IsDestroyed() {
				s.flyoutWin.Invalidate()
			}
		},
		func() { s.updateTray() },
		func() { s.showMain() },
		func() { s.hideFlyout() },
		func(item AlertItem) { s.sendNativeNotification(item) },
	)
	s.app.SetLanguageHook(func() {
		if s.mainWin != nil && !s.mainWin.IsDestroyed() {
			s.mainWin.SetTitle(s.mainTitle())
		}
		mygo.App.SetMenu(s.appMenu())
	})

	if !opts.DisableTray {
		tray, err := mygo.NewTray(mygo.TrayOptions{
			ID:             "omathunder-tray",
			Icon:           s.trayIcon,
			IconIsTemplate: true,
			Title:          s.app.TrayTitle(),
			ToolTip:        s.app.TrayTooltip(),
		})
		if err != nil {
			log.Printf("omathunder tray: %v", err)
		} else {
			s.tray = tray
			s.flyoutWin = mygo.NewFlyout(mygo.FlyoutOptions{
				Tray:         tray,
				Placement:    mygo.PlacementBottom,
				Gap:          6,
				Width:        400,
				Height:       510,
				Focusable:    true,
				Hidden:       true,
				Shadow:       true,
				Vibrancy:     mygo.VibrancyPopover,
				CornerRadius: 12,
				Content:      ui.View(s.app.RenderStatusWidget),
			})
			s.flyoutWin.OnBlur(func() {
				s.mu.Lock()
				s.flyBlurAt = time.Now()
				s.mu.Unlock()
				s.hideFlyout()
			})
			tray.OnClick(func() {
				s.toggleFlyout()
			})
			tray.OnRightClick(func() {
				s.hideFlyout()
				tray.SetMenu(s.trayMenu())
				defer tray.SetMenu(nil)
				tray.PopUpMenu()
			})
			mygo.App.OnWillQuit(func(*mygo.QuitEvent) {
				tray.Destroy()
			})
		}
	}

	if s.tray == nil && opts.StartHidden {
		// Nothing would be visible: no menu bar item to reopen a hidden window.
		s.showMain()
	}

	s.app.RefreshAll(s.ctx)
	s.app.StartEventLoop(s.ctx)
}

func (s *desktopShell) mainTitle() string {
	return s.app.tr("OmaThunder — KI-Mail & Kalender", "OmaThunder — AI Mail & Calendar")
}

func (s *desktopShell) appMenu() *mygo.Menu {
	do := func(fn func()) func(*mygo.MenuItem, *mygo.Window) {
		return func(*mygo.MenuItem, *mygo.Window) {
			if s.mainWin != nil {
				s.mainWin.Update(fn)
			}
		}
	}
	return mygo.NewMenu([]*mygo.MenuItem{
		{Role: mygo.RoleAppMenu, Submenu: []*mygo.MenuItem{
			{Role: mygo.RoleAbout},
			mygo.Separator(),
			{Label: s.app.tr("Command Center öffnen", "Open Command Center"), Click: do(func() { s.showMain() })},
			{Label: s.app.tr("Tages-Briefing (KI)", "Daily Briefing (AI)"), Click: do(func() { s.app.OpenDayBriefing(s.ctx) })},
			mygo.Separator(),
			{Role: mygo.RoleHide},
			{Role: mygo.RoleHideOthers},
			{Role: mygo.RoleUnhide},
			mygo.Separator(),
			{Role: mygo.RoleQuit},
		}},
		{Role: mygo.RoleEditMenu},
		{Role: mygo.RoleWindowMenu},
	})
}

func (s *desktopShell) trayMenu() *mygo.Menu {
	do := func(fn func()) func(*mygo.MenuItem, *mygo.Window) {
		return func(*mygo.MenuItem, *mygo.Window) {
			if s.mainWin != nil {
				s.mainWin.Update(fn)
			}
		}
	}
	return mygo.NewMenu([]*mygo.MenuItem{
		{Label: s.app.TrayTooltip(), Disabled: true},
		mygo.Separator(),
		{Label: s.app.tr("Command Center öffnen", "Open Command Center"), Click: do(func() { s.showMain() })},
		{Label: s.app.tr("Status-Widget anzeigen", "Show Status Widget"), Click: do(func() { s.showFlyout() })},
		{Label: s.app.tr("Tages-Briefing (KI)", "Daily Briefing (AI)"), Click: do(func() { s.app.OpenDayBriefing(s.ctx) })},
		{Label: s.app.tr("Aktualisieren", "Refresh"), Click: do(func() { s.app.RefreshAll(s.ctx) })},
		mygo.Separator(),
		{Role: mygo.RoleQuit},
	})
}

func (s *desktopShell) showMain() {
	if runtime.GOOS == "darwin" {
		mygo.App.SetActivationPolicy(mygo.ActivationPolicyRegular)
	}
	s.hideFlyout()
	if s.mainWin != nil {
		s.mainWin.Show()
		s.mainWin.Focus()
	}
	mygo.App.Focus()
}

func (s *desktopShell) hideMain() {
	if s.mainWin != nil {
		s.mainWin.Hide()
	}
	if runtime.GOOS == "darwin" {
		mygo.App.SetActivationPolicy(mygo.ActivationPolicyAccessory)
	}
}

func (s *desktopShell) toggleFlyout() {
	if s.flyoutWin == nil {
		s.showMain()
		return
	}
	if s.flyoutWin.IsVisible() {
		s.hideFlyout()
		return
	}
	s.mu.Lock()
	sinceBlur := time.Since(s.flyBlurAt)
	s.mu.Unlock()
	if sinceBlur < 350*time.Millisecond {
		return
	}
	s.showFlyout()
}

func (s *desktopShell) showFlyout() {
	if s.flyoutWin == nil {
		return
	}
	s.flyoutWin.Invalidate()
	s.flyoutWin.Show()
	s.flyoutWin.Focus()
}

func (s *desktopShell) hideFlyout() {
	if s.flyoutWin != nil && s.flyoutWin.IsVisible() {
		s.flyoutWin.Hide()
	}
}

func (s *desktopShell) updateTray() {
	if runtime.GOOS == "darwin" {
		mygo.App.SetBadgeCount(s.app.unreadCount())
	}
	if s.tray == nil {
		return
	}
	s.tray.SetTitle(s.app.TrayTitle())
	s.tray.SetToolTip(s.app.TrayTooltip())
	if s.flyoutWin != nil && s.flyoutWin.IsVisible() {
		s.flyoutWin.Invalidate()
	}
}

// sendNativeNotification runs on the UI thread (called from App.pushAlert).
func (s *desktopShell) sendNativeNotification(item AlertItem) {
	// Always show an in-window toast if the main window is visible.
	if s.mainWin != nil && !s.mainWin.IsDestroyed() && s.mainWin.IsVisible() {
		s.app.toast(item.Subtitle+": "+item.Title, "info")
		s.mainWin.Invalidate()
	}
	if !mygo.NotificationsSupported() {
		return
	}
	group := "omathunder-" + item.Kind
	n := mygo.NewNotification(mygo.NotificationOptions{
		ID:       item.ID,
		Title:    item.Title,
		Subtitle: item.Subtitle,
		Body:     item.Body,
		Group:    group,
	})
	go func() {
		if err := n.ShowContext(s.ctx); err != nil {
			log.Printf("omathunder notification: %v", err)
		}
	}()
}
