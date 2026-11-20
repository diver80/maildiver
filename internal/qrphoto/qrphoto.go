// Package qrphoto takes a photo with the phone's camera app, to read a QR code from it. On
// Android it brings its own small plugin (see android/QRPhotoPlugin.kt): mygo's imagepicker
// reports "no camera" on Android 11 and later, as its check for a camera app needs a manifest
// entry an app cannot add. Elsewhere it uses imagepicker.
package qrphoto

import (
	"context"
	"runtime"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/plugins/imagepicker"
)

const pluginID = "qrphoto"

// Capture shows the camera app and returns the path of the JPEG taken, "" when the user went
// back.
func Capture(ctx context.Context) (string, error) {
	if runtime.GOOS != "android" {
		return imagepicker.Capture(ctx)
	}
	var r struct {
		Path string `json:"path"`
	}
	err := mygo.Native(pluginID).Call(ctx, "capture", nil, &r)
	return r.Path, err
}

// PickPhoto lets the user choose a photo they took before (a screenshot of the code, say).
func PickPhoto(ctx context.Context) (string, error) {
	paths, err := imagepicker.Pick(ctx, imagepicker.Options{Kind: imagepicker.Images, Limit: 1})
	if err != nil || len(paths) == 0 {
		return "", err
	}
	return paths[0], nil
}
