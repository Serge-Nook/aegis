package main

import (
	"image/color"
	"strconv"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// barChart draws vertical bars in groups (one group per label, one bar per series).
type barChart struct {
	widget.BaseWidget
	title  string
	labels []string
	series [][]float64 // series[s][i]
	colors []color.Color
	names  []string // legend
}

func newBarChart(title string, labels []string, names []string, colors []color.Color, series ...[]float64) *barChart {
	c := &barChart{title: title, labels: labels, series: series, colors: colors, names: names}
	c.ExtendBaseWidget(c)
	return c
}

func (c *barChart) MinSize() fyne.Size { return fyne.NewSize(360, 260) }

func (c *barChart) CreateRenderer() fyne.WidgetRenderer {
	r := &barRenderer{c: c}
	r.build()
	return r
}

type barRenderer struct {
	c       *barChart
	objs    []fyne.CanvasObject
	title   *canvas.Text
	axis    *canvas.Line
	bars    [][]*canvas.Rectangle
	values  [][]*canvas.Text
	labels  []*canvas.Text
	legend  []fyne.CanvasObject
	maxText *canvas.Text
}

func (r *barRenderer) build() {
	c := r.c
	fg := themeColor(theme.ColorNameForeground)
	r.title = canvas.NewText(c.title, fg)
	r.title.TextStyle.Bold = true
	r.axis = canvas.NewLine(themeColor(theme.ColorNameSeparator))
	r.objs = []fyne.CanvasObject{r.title, r.axis}
	for s := range c.series {
		var bs []*canvas.Rectangle
		var vs []*canvas.Text
		for i := range c.labels {
			b := canvas.NewRectangle(c.colors[s%len(c.colors)])
			b.CornerRadius = 2
			v := canvas.NewText(fmtNum(c.series[s][i]), fg)
			v.TextSize = theme.CaptionTextSize()
			v.Alignment = fyne.TextAlignCenter
			bs, vs = append(bs, b), append(vs, v)
			r.objs = append(r.objs, b, v)
		}
		r.bars, r.values = append(r.bars, bs), append(r.values, vs)
	}
	for _, l := range c.labels {
		t := canvas.NewText(l, themeColor(theme.ColorNamePlaceHolder))
		t.TextSize = theme.CaptionTextSize()
		t.Alignment = fyne.TextAlignCenter
		r.labels = append(r.labels, t)
		r.objs = append(r.objs, t)
	}
	if len(c.names) > 1 {
		for s, n := range c.names {
			sw := canvas.NewRectangle(c.colors[s%len(c.colors)])
			t := canvas.NewText(n, fg)
			t.TextSize = theme.CaptionTextSize()
			r.legend = append(r.legend, sw, t)
			r.objs = append(r.objs, sw, t)
		}
	}
}

func fmtNum(v float64) string {
	if v == float64(int64(v)) {
		return strconv.FormatInt(int64(v), 10)
	}
	return strconv.FormatFloat(v, 'f', 1, 64)
}

func (r *barRenderer) Layout(size fyne.Size) {
	c := r.c
	r.title.Move(fyne.NewPos(4, 0))
	top := float32(28)
	lx := size.Width
	for i := len(r.legend) - 2; i >= 0; i -= 2 {
		t := r.legend[i+1].(*canvas.Text)
		tw := t.MinSize().Width
		lx -= tw + 4
		t.Move(fyne.NewPos(lx, 4))
		lx -= 14
		r.legend[i].Resize(fyne.NewSize(10, 10))
		r.legend[i].Move(fyne.NewPos(lx, 8))
		lx -= 10
	}
	bottom := size.Height - 22
	r.axis.Position1 = fyne.NewPos(4, bottom)
	r.axis.Position2 = fyne.NewPos(size.Width-4, bottom)
	maxV := 0.0
	for _, s := range c.series {
		for _, v := range s {
			maxV = max(maxV, v)
		}
	}
	if maxV == 0 {
		maxV = 1
	}
	n := len(c.labels)
	if n == 0 {
		return
	}
	group := (size.Width - 8) / float32(n)
	ns := float32(len(c.series))
	barW := min(group*0.8/ns, 48)
	h := bottom - top - 16
	// thin out labels when they do not fit
	step := 1
	for step < n && group*float32(step) < 40 {
		step++
	}
	for i := 0; i < n; i++ {
		gx := 4 + group*float32(i) + (group-barW*ns)/2
		for s := range c.series {
			v := c.series[s][i]
			bh := float32(v/maxV) * h
			b := r.bars[s][i]
			b.Move(fyne.NewPos(gx+barW*float32(s), bottom-bh))
			b.Resize(fyne.NewSize(max(barW-2, 1), bh))
			t := r.values[s][i]
			t.Hidden = v == 0 || barW < 14
			t.Move(fyne.NewPos(gx+barW*float32(s), bottom-bh-14))
			t.Resize(fyne.NewSize(barW, 12))
		}
		l := r.labels[i]
		l.Hidden = i%step != 0
		l.Move(fyne.NewPos(4+group*float32(i)-20+group/2, bottom+3))
		l.Resize(fyne.NewSize(40, 14))
		if group >= 40 {
			l.Move(fyne.NewPos(4+group*float32(i), bottom+3))
			l.Resize(fyne.NewSize(group, 14))
		}
	}
}

func (r *barRenderer) MinSize() fyne.Size           { return r.c.MinSize() }
func (r *barRenderer) Refresh() {
	r.Layout(r.c.Size())
	for _, o := range r.objs {
		o.Refresh()
	}
}
func (r *barRenderer) Objects() []fyne.CanvasObject { return r.objs }
func (r *barRenderer) Destroy()                     {}
