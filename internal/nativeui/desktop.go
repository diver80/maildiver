package nativeui

import (
	"context"
	"fmt"
	"log"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

type desktopShell struct {
	app             *App
	ctx             context.Context
	mainWin         *mygo.Window
	flyoutWin       *mygo.Window
	composeWin      *mygo.Window
	eventWin        *mygo.Window
	composeWinSeq   uint64
	eventWinSeq     uint64
	composeWinShown bool
	eventWinShown   bool
	tray            *mygo.Tray
	quitting        bool
	trayIcon        []byte
	appIcon         []byte
	flyBlurAt       time.Time
	mu              sync.Mutex
}

// Run starts the native mygo desktop UI (main window, menu bar status widget, and native notifications).
func Run(ctx context.Context, opts Options) error {
	mygo.App.SetName("MailDiver")
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

	isMobile := runtime.GOOS == "android"
	if !isMobile {
		mygo.App.SetMenu(s.appMenu())
	}
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
	mygo.App.OnOpenURL(func(rawURL string) {
		if len(rawURL) >= 7 && strings.EqualFold(rawURL[:7], "mailto:") {
			if s.mainWin == nil {
				app.pendingMailto = rawURL
				return
			}
			app.emitUpdate(func() {
				s.showMain()
				app.OpenComposeFromMailto(rawURL)
			})
		}
	})
	mygo.App.OnSecondInstance(func(args []string, _ string) {
		for _, arg := range args {
			if len(arg) >= 7 && strings.EqualFold(arg[:7], "mailto:") {
				u := arg
				app.emitUpdate(func() {
					s.showMain()
					app.OpenComposeFromMailto(u)
				})
				return
			}
		}
		app.emitUpdate(func() { s.showMain() })
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
	isMobile := runtime.GOOS == "android"
	if !isMobile && len(s.appIcon) > 0 {
		_ = mygo.App.Dock.SetIcon(s.appIcon)
	}
	winOpts := mygo.WindowOptions{
		Title:    s.mainTitle(),
		Width:    1380,
		Height:   860,
		Hidden:   opts.StartHidden && !isMobile,
		StateKey: "maildiver-main",
		Content:  ui.View(s.app.RenderMain),
	}
	if !isMobile {
		winOpts.MinWidth = 960
		winOpts.MinHeight = 620
	}
	s.mainWin = mygo.NewWindow(winOpts)
	if !isMobile && len(s.appIcon) > 0 {
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
			wrapped := func() {
				fn()
				if s.flyoutWin != nil && !s.flyoutWin.IsDestroyed() {
					s.flyoutWin.Invalidate()
				}
				if s.composeWin != nil && !s.composeWin.IsDestroyed() && s.composeWinShown {
					s.composeWin.Invalidate()
				}
				if s.eventWin != nil && !s.eventWin.IsDestroyed() && s.eventWinShown {
					s.eventWin.Invalidate()
				}
				s.syncAuxWindows()
			}
			if s.mainWin != nil && !s.mainWin.IsDestroyed() {
				s.mainWin.Update(wrapped)
			} else {
				mygo.RunOnMain(wrapped)
			}
		},
		func() { s.updateTray() },
		func() { s.showMain() },
		func() { s.hideFlyout() },
		func(item AlertItem) { s.sendNativeNotification(item) },
	)
	if !isMobile {
		s.app.SetAuxWindowHook(func() {
			mygo.RunOnMain(func() { s.syncAuxWindows() })
		})
	}
	s.app.SetLanguageHook(func() {
		if s.mainWin != nil && !s.mainWin.IsDestroyed() {
			s.mainWin.SetTitle(s.mainTitle())
		}
		if s.composeWin != nil && !s.composeWin.IsDestroyed() {
			s.composeWin.SetTitle(s.app.ComposeWindowTitle())
			s.composeWin.Invalidate()
		}
		if s.eventWin != nil && !s.eventWin.IsDestroyed() {
			s.eventWin.SetTitle(s.app.EventWindowTitle())
			s.eventWin.Invalidate()
		}
		if !isMobile {
			mygo.App.SetMenu(s.appMenu())
		}
	})
	s.app.SetFilePickerHook(func(req FilePickRequest, onDone func(paths []string)) {
		go s.runFileDialog(req, onDone)
	})
	s.app.SetPrintHook(func(title, htmlDoc string) {
		s.printHTMLDocument(title, htmlDoc)
	})
	if !isMobile && mygo.WebViewAvailable() {
		previewHost := newMygoPreviewHost(s.mainWin, s.app)
		s.app.SetWebPreviewHost(previewHost)
		mygo.App.OnWillQuit(func(*mygo.QuitEvent) {
			previewHost.Destroy()
		})
	}

	if !opts.DisableTray && !isMobile {
		tray, err := mygo.NewTray(mygo.TrayOptions{
			ID:             "maildiver-tray",
			Icon:           s.trayIcon,
			IconIsTemplate: true,
			Title:          s.app.TrayTitle(),
			ToolTip:        s.app.TrayTooltip(),
		})
		if err != nil {
			log.Printf("maildiver tray: %v", err)
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
	if s.app.pendingMailto != "" {
		u := s.app.pendingMailto
		s.app.pendingMailto = ""
		s.app.OpenComposeFromMailto(u)
	}
}

// syncAuxWindows creates, shows, focuses, or hides the detached Compose and Calendar Event
// native windows on desktop so the main window remains usable alongside them. Must run on main thread.
func (s *desktopShell) syncAuxWindows() {
	if runtime.GOOS == "android" || s.quitting {
		return
	}
	s.syncComposeWindow()
	s.syncEventWindow()
}

func (s *desktopShell) syncComposeWindow() {
	if s.app.composeOpen {
		needFocus := !s.composeWinShown || s.composeWinSeq != s.app.composeWindowSeq
		s.composeWinSeq = s.app.composeWindowSeq
		if s.composeWin == nil || s.composeWin.IsDestroyed() {
			win := mygo.NewWindow(mygo.WindowOptions{
				Title:     s.app.ComposeWindowTitle(),
				Width:     720,
				Height:    640,
				MinWidth:  540,
				MinHeight: 460,
				StateKey:  "maildiver-compose",
				Content:   ui.View(s.app.RenderComposeWindow),
			})
			if win == nil {
				return
			}
			if len(s.appIcon) > 0 {
				_ = win.SetIcon(s.appIcon)
			}
			win.OnClose(func(e *mygo.CloseEvent) {
				if s.quitting {
					return
				}
				e.PreventDefault()
				if s.app.TryCloseComposeWindow() {
					s.composeWinShown = false
					win.Hide()
					if s.mainWin != nil && !s.mainWin.IsDestroyed() {
						s.mainWin.Invalidate()
					}
				} else {
					win.Invalidate()
				}
			})
			s.composeWin = win
			s.composeWinShown = true
			return
		}
		s.composeWin.SetTitle(s.app.ComposeWindowTitle())
		if needFocus {
			s.composeWinShown = true
			s.composeWin.Show()
			s.composeWin.Focus()
		}
		s.composeWin.Invalidate()
		return
	}
	if s.composeWin != nil && !s.composeWin.IsDestroyed() && s.composeWinShown {
		s.composeWinShown = false
		s.composeWin.Hide()
	}
}

func (s *desktopShell) syncEventWindow() {
	if s.app.eventOpen {
		needFocus := !s.eventWinShown || s.eventWinSeq != s.app.eventWindowSeq
		s.eventWinSeq = s.app.eventWindowSeq
		if s.eventWin == nil || s.eventWin.IsDestroyed() {
			win := mygo.NewWindow(mygo.WindowOptions{
				Title:     s.app.EventWindowTitle(),
				Width:     560,
				Height:    580,
				MinWidth:  460,
				MinHeight: 460,
				StateKey:  "maildiver-event",
				Content:   ui.View(s.app.RenderEventWindow),
			})
			if win == nil {
				return
			}
			if len(s.appIcon) > 0 {
				_ = win.SetIcon(s.appIcon)
			}
			win.OnClose(func(e *mygo.CloseEvent) {
				if s.quitting {
					return
				}
				e.PreventDefault()
				s.app.eventOpen = false
				s.eventWinShown = false
				win.Hide()
				if s.mainWin != nil && !s.mainWin.IsDestroyed() {
					s.mainWin.Invalidate()
				}
			})
			s.eventWin = win
			s.eventWinShown = true
			return
		}
		s.eventWin.SetTitle(s.app.EventWindowTitle())
		if needFocus {
			s.eventWinShown = true
			s.eventWin.Show()
			s.eventWin.Focus()
		}
		s.eventWin.Invalidate()
		return
	}
	if s.eventWin != nil && !s.eventWin.IsDestroyed() && s.eventWinShown {
		s.eventWinShown = false
		s.eventWin.Hide()
	}
}

// printHTMLDocument opens a sandboxed print-preview window and invokes the OS print dialog,
// falling back to a temporary HTML page in the system browser when WebView is unavailable.
func (s *desktopShell) printHTMLDocument(title, htmlDoc string) {
	if runtime.GOOS == "android" || !mygo.WebViewAvailable() {
		if err := openPrintHTMLFallback(htmlDoc); err != nil {
			s.app.toast(s.app.tr("Drucken fehlgeschlagen: ", "Print failed: ")+err.Error(), "error")
		}
		return
	}
	win := mygo.NewWindow(mygo.WindowOptions{
		Title:  s.app.tr("Druckvorschau — ", "Print Preview — ") + title,
		Width:  820,
		Height: 880,
		Page: mygo.PageOptions{
			Untrusted: true,
		},
	})
	if win == nil {
		_ = openPrintHTMLFallback(htmlDoc)
		return
	}
	p := win.Page()
	if p == nil {
		win.Close()
		_ = openPrintHTMLFallback(htmlDoc)
		return
	}
	// Block all navigations and popups inside the untrusted print-preview window.
	p.OnWillNavigate(func(e *mygo.NavigateEvent) {
		if e.URL != "about:blank" {
			e.PreventDefault()
		}
	})
	p.SetWindowOpenHandler(func(mygo.WindowOpenRequest) *mygo.WindowOptions {
		return nil
	})
	printed := false
	p.OnDidFinishLoad(func() {
		if printed || win.IsDestroyed() {
			return
		}
		printed = true
		p.Print()
	})
	p.LoadHTML(htmlDoc, "about:blank")
}

func (s *desktopShell) mainTitle() string {
	return s.app.tr("MailDiver — E-Mail, Kalender & Kontakte", "MailDiver — Mail, Calendar & Contacts")
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
	group := "maildiver-" + item.Kind
	n := mygo.NewNotification(mygo.NotificationOptions{
		ID:       item.ID,
		Title:    item.Title,
		Subtitle: item.Subtitle,
		Body:     item.Body,
		Group:    group,
	})
	go func() {
		if err := n.ShowContext(s.ctx); err != nil {
			log.Printf("maildiver notification: %v", err)
		}
	}()
}

// runFileDialog displays a native OS open/save dialog in a background goroutine
// and delivers the chosen path(s) back onto the UI thread.
func (s *desktopShell) runFileDialog(req FilePickRequest, onDone func(paths []string)) {
	var filters []mygo.FileFilter
	if len(req.Extensions) > 0 {
		name := req.FilterName
		if name == "" {
			name = "Dateien"
		}
		filters = []mygo.FileFilter{{Name: name, Extensions: req.Extensions}}
	}
	var parent *mygo.Window
	if s.mainWin != nil && !s.mainWin.IsDestroyed() {
		parent = s.mainWin
	}

	var paths []string
	var err error
	if req.Mode == "save" {
		var p string
		p, err = mygo.Dialog.Save(mygo.SaveDialogOptions{
			Parent:            parent,
			Title:             req.Title,
			DefaultPath:       req.DefaultPath,
			Filters:           filters,
			ShowHiddenFiles:   req.ShowHidden,
			CreateDirectories: true,
		})
		if err == nil && p != "" {
			paths = []string{p}
		}
	} else {
		paths, err = mygo.Dialog.Open(mygo.OpenDialogOptions{
			Parent:          parent,
			Title:           req.Title,
			DefaultPath:     req.DefaultPath,
			Filters:         filters,
			Directory:       req.Mode == "dir",
			Multiple:        req.Mode == "open-multi",
			ShowHiddenFiles: req.ShowHidden,
		})
	}
	if err != nil {
		log.Printf("maildiver dialog: %v", err)
		return
	}
	if len(paths) == 0 || onDone == nil {
		return
	}
	s.app.emitUpdate(func() {
		onDone(paths)
	})
}

// mygoPreviewHost embeds a native mygo.WebView inside the main window for the mail split preview panel.
// It uses a dedicated persistent session ("maildiver-preview") so Atlassian/Jira/Confluence/GitHub logins
// stay authenticated, while setting Untrusted: true so remote pages can never call Go bindings or access local files.
type mygoPreviewHost struct {
	mu  sync.Mutex
	win *mygo.Window
	app *App
	wv  *mygo.WebView
}

func newMygoPreviewHost(win *mygo.Window, app *App) *mygoPreviewHost {
	return &mygoPreviewHost{win: win, app: app}
}

func (h *mygoPreviewHost) ensure() (*mygo.WebView, error) {
	h.mu.Lock()
	if h.wv != nil && !h.wv.IsDestroyed() {
		wv := h.wv
		h.mu.Unlock()
		return wv, nil
	}
	win := h.win
	h.mu.Unlock()

	if win == nil || win.IsDestroyed() {
		return nil, fmt.Errorf("main window unavailable")
	}

	pageOpts := mygo.PageOptions{
		Session:         mygo.NewSession("maildiver-preview"),
		Untrusted:       true,
		SwipeNavigation: true,
	}
	if runtime.GOOS == "darwin" {
		pageOpts.UserAgentSuffix = "Version/17.0 Safari/605.1.15"
	}
	wv, err := win.NewWebView(mygo.WebViewOptions{Page: pageOpts})
	if err != nil {
		return nil, err
	}

	p := wv.Page()
	// Allow only http(s) and about:blank navigations (including SSO redirects like Atlassian/Microsoft/Google),
	// while blocking file://, javascript:, data:, or custom schemes.
	p.OnWillNavigate(func(e *mygo.NavigateEvent) {
		if e.URL == "about:blank" || isSafeWebURL(e.URL) {
			return
		}
		e.PreventDefault()
	})
	// Never spawn external popup windows from embedded preview pages; open user-clicked links in-pane.
	p.SetWindowOpenHandler(func(req mygo.WindowOpenRequest) *mygo.WindowOptions {
		if req.UserInitiated && isSafeWebURL(req.URL) {
			_ = p.LoadURL(req.URL)
		}
		return nil
	})
	p.OnDidNavigate(func(u string) {
		if wv.IsDestroyed() {
			return
		}
		loading := p.IsLoading()
		h.app.emitUpdate(func() {
			h.app.NotifyPreviewNavigated(u, loading, "")
		})
	})
	p.OnDidFinishLoad(func() {
		if wv.IsDestroyed() {
			return
		}
		u := p.URL()
		h.app.emitUpdate(func() {
			h.app.NotifyPreviewNavigated(u, false, "")
		})
	})
	p.OnDidFailLoad(func(le *mygo.LoadError) {
		if wv.IsDestroyed() {
			return
		}
		msg := ""
		if le != nil {
			// Ignore cancelled loads across platforms (WKWebView -999/102, WebKitGTK 302, WebView2 14, Chromium -3).
			switch le.Code {
			case -999, 102, 302, 14, -3:
				return
			}
			msg = le.Error()
		}
		u := p.URL()
		h.app.emitUpdate(func() {
			h.app.NotifyPreviewNavigated(u, false, msg)
		})
	})

	h.mu.Lock()
	if h.wv != nil && !h.wv.IsDestroyed() {
		// Another goroutine raced and created one; keep existing and discard this duplicate.
		existing := h.wv
		h.mu.Unlock()
		wv.Destroy()
		return existing, nil
	}
	h.wv = wv
	h.mu.Unlock()
	return wv, nil
}

func (h *mygoPreviewHost) Destroy() {
	h.mu.Lock()
	wv := h.wv
	h.wv = nil
	h.mu.Unlock()
	if wv != nil && !wv.IsDestroyed() {
		wv.Destroy()
	}
}

func (h *mygoPreviewHost) page() *mygo.Page {
	h.mu.Lock()
	wv := h.wv
	h.mu.Unlock()
	if wv == nil || wv.IsDestroyed() {
		return nil
	}
	return wv.Page()
}

func (h *mygoPreviewHost) Widget() ui.NativeWebView {
	h.mu.Lock()
	wv := h.wv
	h.mu.Unlock()
	if wv == nil || wv.IsDestroyed() {
		return nil
	}
	return wv
}

func (h *mygoPreviewHost) LoadURL(rawURL string) error {
	if !isSafeWebURL(rawURL) {
		return fmt.Errorf("invalid url")
	}
	wv, err := h.ensure()
	if err != nil {
		return err
	}
	return wv.Page().LoadURL(rawURL)
}

func (h *mygoPreviewHost) GoBack() {
	if p := h.page(); p != nil {
		p.GoBack()
	}
}

func (h *mygoPreviewHost) GoForward() {
	if p := h.page(); p != nil {
		p.GoForward()
	}
}

func (h *mygoPreviewHost) Reload() {
	if p := h.page(); p != nil {
		p.Reload()
	}
}

func (h *mygoPreviewHost) CanGoBack() bool {
	if p := h.page(); p != nil {
		return p.CanGoBack()
	}
	return false
}

func (h *mygoPreviewHost) CanGoForward() bool {
	if p := h.page(); p != nil {
		return p.CanGoForward()
	}
	return false
}

func (h *mygoPreviewHost) IsLoading() bool {
	if p := h.page(); p != nil {
		return p.IsLoading()
	}
	return false
}

func (h *mygoPreviewHost) CurrentURL() string {
	if p := h.page(); p != nil {
		return p.URL()
	}
	return ""
}
