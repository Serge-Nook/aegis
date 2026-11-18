//go:build ignore

// genicon draws build/icon.png: a blue shield with a white check mark.
package main

import (
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
)

func main() {
	const n = 512
	img := image.NewNRGBA(image.Rect(0, 0, n, n))
	inShield := func(x, y float64) bool {
		// shield: flat top with rounded corners, sides curving to a point at the bottom
		if y < 0.08 || y > 0.94 {
			return false
		}
		half := 0.38
		if y > 0.45 {
			t := (y - 0.45) / (0.94 - 0.45)
			half = 0.38 * math.Sqrt(1-t*t)
		}
		return math.Abs(x-0.5) <= half
	}
	dist := func(px, py, ax, ay, bx, by float64) float64 {
		dx, dy := bx-ax, by-ay
		t := math.Max(0, math.Min(1, ((px-ax)*dx+(py-ay)*dy)/(dx*dx+dy*dy)))
		return math.Hypot(px-ax-t*dx, py-ay-t*dy)
	}
	const ss = 4
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			var cov, white float64
			for sy := 0; sy < ss; sy++ {
				for sx := 0; sx < ss; sx++ {
					fx := (float64(x) + (float64(sx)+0.5)/ss) / n
					fy := (float64(y) + (float64(sy)+0.5)/ss) / n
					if !inShield(fx, fy) {
						continue
					}
					cov++
					if math.Min(dist(fx, fy, 0.32, 0.50, 0.45, 0.64), dist(fx, fy, 0.45, 0.64, 0.70, 0.36)) < 0.045 {
						white++
					}
				}
			}
			if cov == 0 {
				continue
			}
			fy := float64(y) / n
			r := 0x1f + (0x0d-0x1f)*fy
			g := 0x6f + (0x3c-0x6f)*fy
			b := 0xeb + (0x9e-0xeb)*fy
			w := white / cov
			c := color.NRGBA{uint8(r + (255-r)*w), uint8(g + (255-g)*w), uint8(b + (255-b)*w), uint8(255 * cov / (ss * ss))}
			img.SetNRGBA(x, y, c)
		}
	}
	f, _ := os.Create("build/icon.png")
	defer f.Close()
	png.Encode(f, img)
}
