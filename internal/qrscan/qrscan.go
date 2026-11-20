// Package qrscan reads a QR code from a photo, as a phone's camera takes it.
package qrscan

import (
	"errors"
	"fmt"
	"image"
	_ "image/jpeg" // camera photos
	_ "image/png"
	"os"

	"github.com/makiuchi-d/gozxing"
	"github.com/makiuchi-d/gozxing/qrcode"
	"golang.org/x/image/draw"
)

// ErrNoCode says the photo shows no readable QR code.
var ErrNoCode = errors.New("no QR code found")

// maxSide bounds the photo's longer side for decoding: a phone's 12-megapixel photo decodes
// slowly and no better, while a QR code filling a fraction of it keeps plenty of pixels.
const maxSide = 1600

// DecodeFile returns the text of the QR code in the image at path.
func DecodeFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return "", fmt.Errorf("read photo: %w", err)
	}
	return Decode(img)
}

// Decode returns the text of the QR code in img, trying a smaller copy first.
func Decode(img image.Image) (string, error) {
	var tries []image.Image
	if small := shrink(img, maxSide); small != img {
		tries = append(tries, small)
	}
	tries = append(tries, img)
	hints := map[gozxing.DecodeHintType]any{gozxing.DecodeHintType_TRY_HARDER: true}
	for _, im := range tries {
		bmp, err := gozxing.NewBinaryBitmapFromImage(im)
		if err != nil {
			continue
		}
		res, err := qrcode.NewQRCodeReader().Decode(bmp, hints)
		if err == nil && res.GetText() != "" {
			return res.GetText(), nil
		}
	}
	return "", ErrNoCode
}

func shrink(img image.Image, side int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= side && h <= side {
		return img
	}
	if w >= h {
		h, w = h*side/w, side
	} else {
		w, h = w*side/h, side
	}
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.ApproxBiLinear.Scale(dst, dst.Bounds(), img, b, draw.Src, nil)
	return dst
}
