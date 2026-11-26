//go:build !darwin || ios

package nativeui

// Only macOS reports how notifications show.
func macNotifyStatus() sysNotifyStatus { return sysNotifyUnknown }

func openMacNotifySettings(bool) {}
