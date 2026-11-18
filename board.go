package main

import (
	"image/color"
	"strconv"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/Serge-Nook/aegis/internal/core"
)

const (
	colWidth  = 290
	cardWidth = 266
)

type colView struct {
	col   core.Column
	obj   fyne.CanvasObject
	cards []*cardWidget
}

// boardView is the kanban board: columns side by side, cards dragged between them.
type boardView struct {
	u         *ui
	v         *core.BoardView
	cols      []*colView
	overlay   *fyne.Container
	ghost     *fyne.Container
	indicator *canvas.Rectangle
	root      *fyne.Container
}

func newBoardWidget(u *ui, v *core.BoardView) fyne.CanvasObject {
	b := &boardView{u: u, v: v}
	byCol := map[int64][]core.TaskCard{}
	for _, t := range v.Tasks {
		byCol[t.ColumnID] = append(byCol[t.ColumnID], t)
	}
	row := container.NewHBox()
	for _, c := range v.Columns {
		cv := &colView{col: c}
		cv.obj = b.column(cv, byCol[c.ID])
		b.cols = append(b.cols, cv)
		row.Add(cv.obj)
	}
	if v.CanManage && u.canWrite() {
		row.Add(container.NewGridWrap(fyne.NewSize(colWidth, 40),
			widget.NewButtonWithIcon(T("Колонка"), theme.ContentAddIcon(), u.addColumn)))
	}
	b.indicator = canvas.NewRectangle(theme.Color(theme.ColorNamePrimary))
	b.indicator.Hide()
	b.ghost = container.NewStack()
	b.ghost.Hide()
	b.overlay = container.NewWithoutLayout(b.indicator, b.ghost)
	b.root = container.NewStack(container.NewScroll(row), b.overlay)
	return b.root
}

func (b *boardView) column(cv *colView, tasks []core.TaskCard) fyne.CanvasObject {
	u := b.u
	name := widget.NewLabelWithStyle(cv.col.Name+"  ("+strconv.Itoa(len(tasks))+")", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	var right []fyne.CanvasObject
	if cv.col.IsDone {
		right = append(right, widget.NewIcon(theme.ConfirmIcon()))
	}
	if b.v.CanCreate && u.canWrite() {
		right = append(right, widget.NewButtonWithIcon("", theme.ContentAddIcon(), func() { u.newTask(cv.col.ID) }))
	}
	if b.v.CanManage && u.canWrite() {
		var menuBtn *widget.Button
		menuBtn = widget.NewButtonWithIcon("", theme.MoreVerticalIcon(), func() {
			idx := 0
			for i, c := range b.v.Columns {
				if c.ID == cv.col.ID {
					idx = i
				}
			}
			doneLabel := T("Считать задачи завершёнными")
			if cv.col.IsDone {
				doneLabel = T("Не считать задачи завершёнными")
			}
			m := fyne.NewMenu("",
				fyne.NewMenuItem(T("Переименовать…"), func() { u.renameColumn(cv.col) }),
				fyne.NewMenuItem(doneLabel, func() {
					c := cv.col
					c.IsDone = !c.IsDone
					u.do(func() error { _, err := u.ss.api.SaveColumn(c); return err })
				}),
				fyne.NewMenuItem(T("Сдвинуть влево"), func() { u.do(func() error { return u.ss.api.MoveColumn(cv.col.ID, idx-1) }) }),
				fyne.NewMenuItem(T("Сдвинуть вправо"), func() { u.do(func() error { return u.ss.api.MoveColumn(cv.col.ID, idx+1) }) }),
				fyne.NewMenuItemSeparator(),
				fyne.NewMenuItem(T("Удалить колонку…"), func() { u.deleteColumn(cv.col, b.v.Columns) }))
			m.Items[2].Disabled = idx == 0
			m.Items[3].Disabled = idx == len(b.v.Columns)-1
			pos := fyne.CurrentApp().Driver().AbsolutePositionForObject(menuBtn).Add(fyne.NewPos(0, menuBtn.Size().Height))
			widget.ShowPopUpMenuAtPosition(m, u.win.Canvas(), pos)
		})
		menuBtn.Importance = widget.LowImportance
		right = append(right, menuBtn)
	}
	header := container.NewBorder(nil, nil, nil, container.NewHBox(right...), name)
	cards := container.NewVBox()
	for _, t := range tasks {
		c := newCard(b, t)
		cv.cards = append(cv.cards, c)
		cards.Add(c)
	}
	bg := canvas.NewRectangle(themeColor(theme.ColorNameInputBackground))
	bg.CornerRadius = 8
	body := container.NewBorder(container.NewPadded(header), nil, nil, nil, container.NewVScroll(container.NewPadded(cards)))
	return container.NewGridWrap(fyne.NewSize(colWidth, 10000), container.NewStack(bg, body))
}

type cardWidget struct {
	widget.BaseWidget
	b        *boardView
	t        core.TaskCard
	dragging bool
	grab     fyne.Position
}

func newCard(b *boardView, t core.TaskCard) *cardWidget {
	c := &cardWidget{b: b, t: t}
	c.ExtendBaseWidget(c)
	return c
}

func (c *cardWidget) content() fyne.CanvasObject {
	u, t := c.b.u, c.t
	now := time.Now().UnixMilli()
	num := canvas.NewText("#"+strconv.FormatInt(t.ID, 10)+"  "+priorityName(t.Priority), priorityColors[clampPrio(t.Priority)])
	num.TextSize = theme.CaptionTextSize()
	num.TextStyle.Bold = true
	title := widget.NewLabelWithStyle(t.Title, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	title.Wrapping = fyne.TextWrapWord
	items := []fyne.CanvasObject{num, title}
	if len(t.Tags) > 0 {
		tags := container.NewHBox()
		u.mu.Lock()
		for _, id := range t.Tags {
			if tg, ok := u.tagByID[id]; ok {
				tags.Add(tagChip(tg))
			}
		}
		u.mu.Unlock()
		items = append(items, tags)
	}
	var info []string
	if t.ResponsibleID != 0 {
		r := u.userName(t.ResponsibleID)
		if n := len(t.CoExecutors); n > 0 {
			r += " +" + strconv.Itoa(n)
		}
		info = append(info, r)
	} else {
		info = append(info, T("без исполнителя"))
	}
	if t.SubTotal > 0 {
		info = append(info, T("Чек-лист")+" "+strconv.Itoa(t.SubDone)+"/"+strconv.Itoa(t.SubTotal))
	}
	if t.Comments > 0 {
		info = append(info, T("Комм.")+" "+strconv.Itoa(t.Comments))
	}
	if t.Attachments > 0 {
		info = append(info, T("Влож.")+" "+strconv.Itoa(t.Attachments))
	}
	infoT := canvas.NewText(joinDot(info), themeColor(theme.ColorNamePlaceHolder))
	infoT.TextSize = theme.CaptionTextSize()
	items = append(items, infoT)
	if t.Deadline > 0 {
		col := themeColor(theme.ColorNamePlaceHolder)
		label := T("Срок") + ": " + fmtTime(t.Deadline)
		if t.Overdue(now) {
			col = themeColor(theme.ColorNameError)
			label += " — " + T("просрочено")
		} else if t.CompletedAt > 0 {
			label += " ✓"
		}
		dl := canvas.NewText(label, col)
		dl.TextSize = theme.CaptionTextSize()
		dl.TextStyle.Bold = t.Overdue(now)
		items = append(items, dl)
	}
	bg := canvas.NewRectangle(themeColor(theme.ColorNameBackground))
	bg.CornerRadius = 6
	bg.StrokeWidth = 1
	bg.StrokeColor = themeColor(theme.ColorNameSeparator)
	if t.Overdue(now) {
		bg.StrokeColor = themeColor(theme.ColorNameError)
	}
	strip := canvas.NewRectangle(priorityColors[clampPrio(t.Priority)])
	strip.SetMinSize(fyne.NewSize(4, 4))
	return container.NewStack(bg, container.NewBorder(nil, nil, strip, nil, container.NewPadded(container.NewVBox(items...))))
}

func joinDot(s []string) string {
	out := ""
	for i, x := range s {
		if i > 0 {
			out += " · "
		}
		out += x
	}
	return out
}

func clampPrio(p core.Priority) int { return min(max(int(p), 0), 3) }

func tagChip(tg core.Tag) fyne.CanvasObject {
	c := parseHex(tg.Color)
	bg := canvas.NewRectangle(color.NRGBA{c.R, c.G, c.B, 0x40})
	bg.CornerRadius = 4
	txt := canvas.NewText(tg.Name, themeColor(theme.ColorNameForeground))
	txt.TextSize = theme.CaptionTextSize()
	return container.NewStack(bg, container.New(layout.NewCustomPaddedLayout(1, 1, 5, 5), txt))
}

func (c *cardWidget) CreateRenderer() fyne.WidgetRenderer {
	return widget.NewSimpleRenderer(container.NewGridWrap(fyne.NewSize(cardWidth, 0), c.content()))
}

func (c *cardWidget) MinSize() fyne.Size {
	m := c.BaseWidget.MinSize()
	return fyne.NewSize(cardWidth, m.Height)
}

func (c *cardWidget) Tapped(*fyne.PointEvent) { c.b.u.openTask(c.t.ID) }

func (c *cardWidget) TappedSecondary(e *fyne.PointEvent) {
	u, b := c.b.u, c.b
	var moveItems []*fyne.MenuItem
	for _, col := range b.v.Columns {
		col := col
		it := fyne.NewMenuItem(col.Name, func() {
			u.do(func() error { return u.ss.api.MoveTask(c.t.ID, col.ID, 1<<30) })
		})
		it.Disabled = col.ID == c.t.ColumnID || !u.canWrite()
		moveItems = append(moveItems, it)
	}
	move := fyne.NewMenuItem(T("Переместить в"), nil)
	move.ChildMenu = fyne.NewMenu("", moveItems...)
	dup := fyne.NewMenuItem(T("Дублировать"), func() {
		u.do(func() error { _, err := u.ss.api.CopyTask(c.t.ID, c.t.BoardID); return err })
	})
	arch := fyne.NewMenuItem(T("В архив"), func() { u.do(func() error { return u.ss.api.ArchiveTask(c.t.ID, !c.t.Archived) }) })
	if c.t.Archived {
		arch.Label = T("Вернуть из архива")
	}
	dup.Disabled, arch.Disabled = !u.canWrite(), !u.canWrite()
	m := fyne.NewMenu("", fyne.NewMenuItem(T("Открыть"), func() { u.openTask(c.t.ID) }), move, dup, arch)
	widget.ShowPopUpMenuAtPosition(m, u.win.Canvas(), e.AbsolutePosition)
}

func (c *cardWidget) Dragged(e *fyne.DragEvent) {
	b := c.b
	if !b.u.canWrite() {
		return
	}
	d := fyne.CurrentApp().Driver()
	origin := d.AbsolutePositionForObject(b.overlay)
	if !c.dragging {
		c.dragging = true
		c.grab = e.AbsolutePosition.Subtract(d.AbsolutePositionForObject(c))
		b.ghost.Objects = []fyne.CanvasObject{container.NewGridWrap(fyne.NewSize(cardWidth, c.Size().Height), c.content())}
		b.ghost.Resize(fyne.NewSize(cardWidth, c.Size().Height))
		b.ghost.Show()
	}
	b.ghost.Move(e.AbsolutePosition.Subtract(origin).Subtract(c.grab))
	cv, idx := b.target(e.AbsolutePosition, c)
	if cv == nil {
		b.indicator.Hide()
		return
	}
	cp := d.AbsolutePositionForObject(cv.obj).Subtract(origin)
	y := cp.Y + 48
	vis := visibleCards(cv, c)
	if len(vis) > 0 {
		if idx < len(vis) {
			y = d.AbsolutePositionForObject(vis[idx]).Subtract(origin).Y - 3
		} else {
			last := vis[len(vis)-1]
			y = d.AbsolutePositionForObject(last).Subtract(origin).Y + last.Size().Height + 3
		}
	}
	b.indicator.Move(fyne.NewPos(cp.X+8, y))
	b.indicator.Resize(fyne.NewSize(colWidth-16, 3))
	b.indicator.Show()
	b.overlay.Refresh()
}

func (c *cardWidget) DragEnd() {
	b := c.b
	if !c.dragging {
		return
	}
	c.dragging = false
	b.ghost.Hide()
	b.indicator.Hide()
	pos := b.ghost.Position().Add(c.grab).Add(fyne.CurrentApp().Driver().AbsolutePositionForObject(b.overlay))
	cv, idx := b.target(pos, c)
	if cv == nil {
		return
	}
	vis := visibleCards(cv, c)
	p := 1 << 30
	if idx < len(vis) {
		p = vis[idx].t.Pos
		if cv.col.ID == c.t.ColumnID && c.t.Pos < p {
			p--
		}
	}
	if cv.col.ID == c.t.ColumnID && p == c.t.Pos {
		return
	}
	u := b.u
	u.do(func() error { return u.ss.api.MoveTask(c.t.ID, cv.col.ID, p) })
}

func visibleCards(cv *colView, skip *cardWidget) []*cardWidget {
	var out []*cardWidget
	for _, c := range cv.cards {
		if c != skip {
			out = append(out, c)
		}
	}
	return out
}

// target finds the column under the pointer and the insertion index among its cards.
func (b *boardView) target(abs fyne.Position, skip *cardWidget) (*colView, int) {
	d := fyne.CurrentApp().Driver()
	for _, cv := range b.cols {
		p := d.AbsolutePositionForObject(cv.obj)
		if abs.X < p.X || abs.X > p.X+colWidth {
			continue
		}
		idx := 0
		for _, c := range visibleCards(cv, skip) {
			cp := d.AbsolutePositionForObject(c)
			if abs.Y > cp.Y+c.Size().Height/2 {
				idx++
			}
		}
		return cv, idx
	}
	return nil, 0
}

// do runs a change in the background; the board refreshes via polling, and at once here.
func (u *ui) do(fn func() error) {
	async(func() (bool, error) { return true, fn() }, func(_ bool, err error) {
		if err != nil {
			u.handleErr(err)
		}
		u.reloadBoard()
	})
}

func (u *ui) addColumn() {
	v := u.curView()
	if v == nil {
		return
	}
	e := widget.NewEntry()
	done := widget.NewCheck(T("Задачи в колонке считаются завершёнными"), nil)
	dialog.ShowForm(T("Новая колонка"), T("Добавить"), T("Отмена"),
		[]*widget.FormItem{widget.NewFormItem(T("Название"), e), widget.NewFormItem("", done)}, func(ok bool) {
			if ok {
				u.do(func() error {
					_, err := u.ss.api.SaveColumn(core.Column{BoardID: v.Board.ID, Name: e.Text, Pos: len(v.Columns), IsDone: done.Checked})
					return err
				})
			}
		}, u.win)
}

func (u *ui) renameColumn(c core.Column) {
	e := widget.NewEntry()
	e.SetText(c.Name)
	dialog.ShowForm(T("Переименовать колонку"), T("Сохранить"), T("Отмена"), []*widget.FormItem{widget.NewFormItem(T("Название"), e)}, func(ok bool) {
		if ok {
			c.Name = e.Text
			u.do(func() error { _, err := u.ss.api.SaveColumn(c); return err })
		}
	}, u.win)
}

func (u *ui) deleteColumn(c core.Column, all []core.Column) {
	var names []string
	var ids []int64
	for _, x := range all {
		if x.ID != c.ID {
			names = append(names, x.Name)
			ids = append(ids, x.ID)
		}
	}
	sel := widget.NewSelect(names, nil)
	if len(names) > 0 {
		sel.SetSelectedIndex(0)
	}
	dialog.ShowForm(Tf("Удалить колонку «%s»?", c.Name), T("Удалить"), T("Отмена"),
		[]*widget.FormItem{widget.NewFormItem(T("Перенести задачи в"), sel)}, func(ok bool) {
			if !ok {
				return
			}
			var to int64
			if i := sel.SelectedIndex(); i >= 0 {
				to = ids[i]
			}
			u.do(func() error { return u.ss.api.DeleteColumn(c.ID, to) })
		}, u.win)
}
