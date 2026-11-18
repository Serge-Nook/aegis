package main

import (
	"fmt"
	"image/color"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/theme"
)

const (
	prefDark = "darkTheme"
	prefLang = "lang"
)

type variantTheme struct {
	fyne.Theme
	variant fyne.ThemeVariant
}

func (t variantTheme) Color(n fyne.ThemeColorName, _ fyne.ThemeVariant) color.Color {
	return t.Theme.Color(n, t.variant)
}

func applyTheme(a fyne.App, dark bool) {
	v := theme.VariantLight
	if dark {
		v = theme.VariantDark
	}
	a.Settings().SetTheme(variantTheme{Theme: theme.DefaultTheme(), variant: v})
}

func isDark(a fyne.App) bool {
	return a.Preferences().BoolWithFallback(prefDark, a.Settings().ThemeVariant() == theme.VariantDark)
}

func themeColor(n fyne.ThemeColorName) color.Color {
	a := fyne.CurrentApp()
	return a.Settings().Theme().Color(n, a.Settings().ThemeVariant())
}

var priorityColors = [4]color.NRGBA{
	{0x8a, 0x94, 0xa6, 0xff}, // low
	{0x3b, 0x82, 0xf6, 0xff}, // medium
	{0xf5, 0x9e, 0x0b, 0xff}, // high
	{0xdc, 0x26, 0x26, 0xff}, // critical
}

var tagPalette = []string{"#3b82f6", "#22c55e", "#f59e0b", "#ef4444", "#a855f7", "#ec4899", "#14b8a6", "#64748b"}

func parseHex(s string) color.NRGBA {
	c := color.NRGBA{0x64, 0x74, 0x8b, 0xff}
	if len(s) == 7 && s[0] == '#' {
		var r, g, b uint8
		if _, err := fmt.Sscanf(s[1:], "%02x%02x%02x", &r, &g, &b); err == nil {
			c = color.NRGBA{r, g, b, 0xff}
		}
	}
	return c
}
