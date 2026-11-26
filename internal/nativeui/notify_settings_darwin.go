//go:build darwin && !ios

package nativeui

import (
	"os/exec"
	"time"

	"github.com/ebitengine/purego/objc"
)

// macBundleID is PostDive's bundle identifier (mygo.json), which names its page in System Settings.
const macBundleID = "dev.postdive.app"

// macNotifyStatus asks UNUserNotificationCenter how PostDive's notifications show. Only call it in
// the packaged app: the center throws outside a bundle.
func macNotifyStatus() sysNotifyStatus {
	cls := objc.GetClass("UNUserNotificationCenter")
	if cls == 0 {
		return sysNotifyUnknown
	}
	center := objc.ID(cls).Send(objc.RegisterName("currentNotificationCenter"))
	if center == 0 {
		return sysNotifyUnknown
	}
	got := make(chan sysNotifyStatus, 1)
	blk := objc.NewBlock(func(_ objc.Block, settings objc.ID) {
		if settings == 0 {
			got <- sysNotifyUnknown
			return
		}
		auth := objc.Send[int](settings, objc.RegisterName("authorizationStatus"))
		style := objc.Send[int](settings, objc.RegisterName("alertStyle"))
		got <- classifyMacNotifySettings(auth, style)
	})
	center.Send(objc.RegisterName("getNotificationSettingsWithCompletionHandler:"), blk)
	blk.Release() // the center keeps its own copy until it calls back
	select {
	case st := <-got:
		return st
	case <-time.After(3 * time.Second):
		return sysNotifyUnknown
	}
}

// openMacNotifySettings opens PostDive's page in System Settings → Notifications, or the Focus
// settings.
func openMacNotifySettings(focus bool) {
	url := "x-apple.systempreferences:com.apple.Notifications-Settings.extension?id=" + macBundleID
	if focus {
		url = "x-apple.systempreferences:com.apple.Focus-Settings.extension"
	}
	_ = exec.Command("open", url).Start()
}
