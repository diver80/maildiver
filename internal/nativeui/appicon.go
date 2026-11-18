package nativeui

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
)

// RenderAppIconPNG generates a crisp macOS-styled squircle application icon PNG
// (suitable for 1024x1024 AppIcon.icns bundling as well as 256x256 Dock/window icons).
func RenderAppIconPNG(size int) []byte {
	if size < 32 {
		size = 32
	}
	img := image.NewNRGBA(image.Rect(0, 0, size, size))
	s := float64(size)

	// macOS Big Sur+ icon grid: ~80.5% body centered with ~9.75% margin for drop shadow.
	margin := s * 0.09765625
	half := (s - 2*margin) / 2
	cx, cy := s/2, s/2
	corner := half * 0.44 // squircle-like continuous curve radius

	// Superellipse-blended rounded rect signed distance (smooth macOS squircle look).
	squircleSDF := func(px, py, ox, oy, hw, hh, r float64) float64 {
		qx := math.Abs(px-ox) - (hw - r)
		qy := math.Abs(py-oy) - (hh - r)
		ax := math.Max(qx, 0)
		ay := math.Max(qy, 0)
		// p=2.35 gives a gentle Apple-style squircle transition into the straight edges.
		const p = 2.35
		outside := math.Pow(math.Pow(ax, p)+math.Pow(ay, p), 1.0/p)
		inside := math.Min(math.Max(qx, qy), 0)
		return outside + inside - r
	}

	rrectSDF := func(px, py, ox, oy, hw, hh, r float64) float64 {
		qx := math.Abs(px-ox) - (hw - r)
		qy := math.Abs(py-oy) - (hh - r)
		return math.Hypot(math.Max(qx, 0), math.Max(qy, 0)) + math.Min(math.Max(qx, qy), 0) - r
	}

	segDist := func(px, py, x0, y0, x1, y1 float64) float64 {
		dx, dy := x1-x0, y1-y0
		l2 := dx*dx + dy*dy
		if l2 == 0 {
			return math.Hypot(px-x0, py-y0)
		}
		t := ((px-x0)*dx + (py-y0)*dy) / l2
		if t < 0 {
			t = 0
		} else if t > 1 {
			t = 1
		}
		return math.Hypot(px-(x0+t*dx), py-(y0+t*dy))
	}

	blendOver := func(dst, src color.NRGBA) color.NRGBA {
		if src.A == 0 {
			return dst
		}
		sa := float64(src.A) / 255.0
		da := float64(dst.A) / 255.0
		outA := sa + da*(1-sa)
		if outA <= 0 {
			return color.NRGBA{}
		}
		blendC := func(dc, sc uint8) uint8 {
			v := (float64(sc)*sa + float64(dc)*da*(1-sa)) / outA
			if v < 0 {
				v = 0
			} else if v > 255 {
				v = 255
			}
			return uint8(math.Round(v))
		}
		return color.NRGBA{
			R: blendC(dst.R, src.R),
			G: blendC(dst.G, src.G),
			B: blendC(dst.B, src.B),
			A: uint8(math.Round(outA * 255)),
		}
	}

	lerp := func(a, b, t float64) float64 {
		if t < 0 {
			t = 0
		} else if t > 1 {
			t = 1
		}
		return a + (b-a)*t
	}
	lerpColor := func(c0, c1 color.NRGBA, t float64) color.NRGBA {
		return color.NRGBA{
			R: uint8(math.Round(lerp(float64(c0.R), float64(c1.R), t))),
			G: uint8(math.Round(lerp(float64(c0.G), float64(c1.G), t))),
			B: uint8(math.Round(lerp(float64(c0.B), float64(c1.B), t))),
			A: uint8(math.Round(lerp(float64(c0.A), float64(c1.A), t))),
		}
	}
	cov := func(d, aa float64) float64 {
		v := 0.5 - d/aa
		if v < 0 {
			return 0
		}
		if v > 1 {
			return 1
		}
		return v
	}

	aa := math.Max(1.0, s/512.0)

	// Palette (Tokyo Night rich gradient + OmaThunder indigo/blue + AI spark)
	bgTop := color.NRGBA{R: 0x2d, G: 0x34, B: 0x54, A: 255}
	bgMid := color.NRGBA{R: 0x1e, G: 0x22, B: 0x36, A: 255}
	bgBot := color.NRGBA{R: 0x13, G: 0x16, B: 0x22, A: 255}
	envStroke := color.NRGBA{R: 0x7a, G: 0xa2, B: 0xf7, A: 255}
	envFill := color.NRGBA{R: 0x24, G: 0x2b, B: 0x45, A: 235}
	sparkBg := color.NRGBA{R: 0x9e, G: 0xce, B: 0x6a, A: 255}
	sparkInner := color.NRGBA{R: 0xf7, G: 0xfa, B: 0xff, A: 255}

	// Envelope geometry (centered slightly lower-left so the AI badge balances top-right)
	envCX, envCY := cx-s*0.015, cy+s*0.025
	envHW, envHH := s*0.235, s*0.165
	envR := s * 0.036
	envStrokeR := s * 0.022

	// AI badge circle at top-right corner of the envelope
	badgeCX, badgeCY := envCX+envHW-s*0.015, envCY-envHH-s*0.01
	badgeR := s * 0.088

	for y := 0; y < size; y++ {
		py := float64(y) + 0.5
		ty := (py - margin) / (2 * half)
		for x := 0; x < size; x++ {
			px := float64(x) + 0.5

			var pxCol color.NRGBA

			// Soft drop shadow beneath squircle
			shadowD := squircleSDF(px, py-s*0.016, cx, cy, half, half, corner)
			if shadowD < s*0.045 && shadowD > -s*0.01 {
				shA := (1.0 - math.Max(0, shadowD)/(s*0.045))
				shA = shA * shA * 0.38
				if shA > 0.002 {
					pxCol = blendOver(pxCol, color.NRGBA{R: 0, G: 0, B: 8, A: uint8(shA * 255)})
				}
			}

			// Main squircle body
			bodyD := squircleSDF(px, py, cx, cy, half, half, corner)
			bodyCov := cov(bodyD, aa)
			if bodyCov > 0 {
				var base color.NRGBA
				if ty < 0.45 {
					base = lerpColor(bgTop, bgMid, ty/0.45)
				} else {
					base = lerpColor(bgMid, bgBot, (ty-0.45)/0.55)
				}
				// Subtle radial glow behind envelope
				glowDist := math.Hypot(px-envCX, py-envCY) / (s * 0.42)
				if glowDist < 1 {
					g := (1 - glowDist) * (1 - glowDist) * 0.22
					base = blendOver(base, color.NRGBA{R: 0x7a, G: 0xa2, B: 0xf7, A: uint8(g * 255)})
				}
				// Subtle inner top-rim highlight
				if bodyD > -s*0.016 && bodyD < 0 {
					rim := (1 - (-bodyD)/(s*0.016)) * (0.14 - 0.08*math.Min(1, math.Max(0, ty)))
					if rim > 0 {
						base = blendOver(base, color.NRGBA{R: 255, G: 255, B: 255, A: uint8(rim * 255)})
					}
				}
				base.A = uint8(math.Round(bodyCov * 255))
				pxCol = blendOver(pxCol, base)
			}

			// Mail envelope fill
			envD := rrectSDF(px, py, envCX, envCY, envHW, envHH, envR)
			if c := cov(envD, aa); c > 0 {
				fc := envFill
				fc.A = uint8(math.Round(c * float64(envFill.A)))
				pxCol = blendOver(pxCol, fc)
			}

			// Mail envelope outline + V flap
			flapD := math.Min(
				segDist(px, py, envCX-envHW+s*0.025, envCY-envHH+s*0.025, envCX, envCY+s*0.032),
				segDist(px, py, envCX+envHW-s*0.025, envCY-envHH+s*0.025, envCX, envCY+s*0.032),
			) - envStrokeR
			outlineD := math.Abs(envD) - envStrokeR
			strokeD := math.Min(outlineD, flapD)
			if c := cov(strokeD, aa); c > 0 {
				sc := envStroke
				// Gentle vertical sheen on envelope lines
				if py < envCY {
					sc = lerpColor(color.NRGBA{R: 0xa9, G: 0xc4, B: 0xff, A: 255}, envStroke, 0.45)
				}
				sc.A = uint8(math.Round(c * 255))
				pxCol = blendOver(pxCol, sc)
			}

			// AI status/spark badge (dark halo cutout + green pill + crisp 4-point star)
			bDist := math.Hypot(px-badgeCX, py-badgeCY)
			haloD := bDist - (badgeR + s*0.024)
			if c := cov(haloD, aa); c > 0 {
				hc := bgMid
				hc.A = uint8(math.Round(c * 245))
				pxCol = blendOver(pxCol, hc)
			}
			badgeD := bDist - badgeR
			if c := cov(badgeD, aa); c > 0 {
				bc := lerpColor(color.NRGBA{R: 0xb4, G: 0xe3, B: 0x80, A: 255}, sparkBg, (py-(badgeCY-badgeR))/(2*badgeR))
				bc.A = uint8(math.Round(c * 255))
				pxCol = blendOver(pxCol, bc)
			}

			// 4-point AI spark inside the green badge
			dx, dy := math.Abs(px-badgeCX), math.Abs(py-badgeCY)
			spR := badgeR * 0.56
			// Star shape: |x|^0.55 + |y|^0.55 <= r^0.55
			starVal := math.Pow(dx/spR, 0.58) + math.Pow(dy/spR, 0.58) - 1.0
			starCov := cov(starVal*spR*0.55, aa)
			if starCov > 0 {
				ic := sparkInner
				ic.A = uint8(math.Round(starCov * 255))
				pxCol = blendOver(pxCol, ic)
			}

			img.SetNRGBA(x, y, pxCol)
		}
	}

	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}
