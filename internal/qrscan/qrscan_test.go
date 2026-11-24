package qrscan

import (
	"image"
	"image/color"
	"image/draw"
	"testing"

	qr "github.com/skip2/go-qrcode"
)

// A QR code as the desktop shows it, photographed small inside a large, grey photo.
func TestDecodeCodeInsideLargePhoto(t *testing.T) {
	want := "postdive-pair://v1?h=192.168.1.20:41234&k=abcdefghijklmnopqrstuvwxyz0123456789ABCDEFG"
	code, err := qr.New(want, qr.Medium)
	if err != nil {
		t.Fatal(err)
	}
	small := code.Image(420)
	photo := image.NewRGBA(image.Rect(0, 0, 4000, 3000))
	draw.Draw(photo, photo.Bounds(), &image.Uniform{color.RGBA{180, 180, 175, 255}}, image.Point{}, draw.Src)
	draw.Draw(photo, image.Rect(1500, 1100, 1920, 1520), small, image.Point{}, draw.Src)
	got, err := Decode(photo)
	if err != nil || got != want {
		t.Fatalf("got %q, %v", got, err)
	}
	if _, err := Decode(image.NewRGBA(image.Rect(0, 0, 300, 300))); err != ErrNoCode {
		t.Fatalf("empty photo: %v", err)
	}
}
