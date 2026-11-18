package main

import (
	"image/color"
	"strconv"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/Serge-Nook/aegis/internal/core"
)

func newTable(header []string, rows *[][]string, widths []float32, onSelect func(row int)) *widget.Table {
	t := widget.NewTableWithHeaders(
		func() (int, int) { return len(*rows), len(header) },
		func() fyne.CanvasObject {
			l := widget.NewLabel("")
			l.Truncation = fyne.TextTruncateEllipsis
			return l
		},
		func(id widget.TableCellID, o fyne.CanvasObject) { o.(*widget.Label).SetText((*rows)[id.Row][id.Col]) })
	t.ShowHeaderColumn = false
	t.CreateHeader = func() fyne.CanvasObject {
		return widget.NewLabelWithStyle("", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	}
	t.UpdateHeader = func(id widget.TableCellID, o fyne.CanvasObject) {
		if id.Row < 0 && id.Col >= 0 {
			o.(*widget.Label).SetText(header[id.Col])
		}
	}
	for i, w := range widths {
		t.SetColumnWidth(i, w)
	}
	if onSelect != nil {
		t.OnSelected = func(id widget.TableCellID) {
			t.UnselectAll()
			if id.Row >= 0 && id.Row < len(*rows) {
				onSelect(id.Row)
			}
		}
	}
	return t
}

func pct(v float64) string { return strconv.FormatFloat(v, 'f', 1, 64) + " %" }
func hours(v float64) string {
	if v == 0 {
		return "—"
	}
	if v >= 48 {
		return strconv.FormatFloat(v/24, 'f', 1, 64) + " " + T("дн.")
	}
	return strconv.FormatFloat(v, 'f', 1, 64) + " " + T("ч")
}

func statsHeader() []string {
	return []string{T("Название"), T("Всего"), T("Выполнено"), T("В работе"), T("Просрочено"), T("% просрочки"), T("Ср. время выполнения")}
}

func statsRow(g core.GroupStats) []string {
	return []string{g.Name, strconv.Itoa(g.Total), strconv.Itoa(g.Done), strconv.Itoa(g.InProgress), strconv.Itoa(g.Overdue), pct(g.OverduePct), hours(g.AvgHours)}
}

func statsAny(g core.GroupStats) []any {
	return []any{g.Name, g.Total, g.Done, g.InProgress, g.Overdue, g.OverduePct, g.AvgHours}
}

func (u *ui) showReports() {
	w := u.app.NewWindow(T("Аналитика") + " — " + appName)
	from, to, period := periodEntries(30)
	dept, deptID := u.deptSelect(0)
	dept.Options[0] = T("— все отделы —")
	usr, uid := u.userSelect()
	u.mu.Lock()
	boards := append([]core.Board(nil), u.boards...)
	tags := append([]core.Tag(nil), u.tags...)
	u.mu.Unlock()
	bOpts := []string{T("— все доски —")}
	for _, b := range boards {
		bOpts = append(bOpts, b.Name)
	}
	board := widget.NewSelect(bOpts, nil)
	board.SetSelectedIndex(0)
	prio := widget.NewSelect(append([]string{T("— любой приоритет —")}, priorityNames()...), nil)
	prio.SetSelectedIndex(0)
	tOpts := []string{T("— любой тег —")}
	for _, t := range tags {
		tOpts = append(tOpts, t.Name)
	}
	tag := widget.NewSelect(tOpts, nil)
	tag.SetSelectedIndex(0)

	var rep *core.Report
	var deptRows, userRows, taskRows [][]string
	summary := container.NewVBox()
	deptT := newTable(statsHeader(), &deptRows, []float32{240, 80, 100, 90, 100, 110, 170}, nil)
	userT := newTable(statsHeader(), &userRows, []float32{240, 80, 100, 90, 100, 110, 170}, nil)
	taskHdr := []string{"№", T("Название"), T("Доска"), T("Этап"), T("Отдел"), T("Приоритет"), T("Ответственный"), T("Срок"), T("Создана"), T("Завершена"), T("Просрочена")}
	taskT := newTable(taskHdr, &taskRows, []float32{60, 260, 140, 120, 140, 100, 160, 130, 130, 130, 90}, func(r int) {
		u.openTask(rep.Tasks[r].ID)
	})
	tabs := container.NewAppTabs(
		container.NewTabItemWithIcon(T("Сводка"), theme.InfoIcon(), container.NewVScroll(summary)),
		container.NewTabItemWithIcon(T("По отделам"), theme.HomeIcon(), deptT),
		container.NewTabItemWithIcon(T("По исполнителям"), theme.AccountIcon(), userT),
		container.NewTabItemWithIcon(T("Задачи"), theme.ListIcon(), taskT),
	)

	yes := func(b bool) string {
		if b {
			return T("да")
		}
		return ""
	}
	show := func(r *core.Report) {
		rep = r
		deptRows, userRows, taskRows = nil, nil, nil
		for _, g := range r.Depts {
			deptRows = append(deptRows, statsRow(g))
		}
		for _, g := range r.Users {
			userRows = append(userRows, statsRow(g))
		}
		for _, t := range r.Tasks {
			taskRows = append(taskRows, []string{"#" + strconv.FormatInt(t.ID, 10), t.Title, t.Board, t.Column, t.Department,
				priorityName(t.Priority), t.Responsible, fmtTime(t.Deadline), fmtTime(t.CreatedAt), fmtTime(t.CompletedAt), yes(t.Overdue)})
		}
		deptT.Refresh()
		userT.Refresh()
		taskT.Refresh()

		tot := r.Total
		card := func(title, value string) fyne.CanvasObject {
			v := widget.NewLabelWithStyle(value, fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
			v.SizeName = theme.SizeNameHeadingText
			return widget.NewCard("", "", container.NewVBox(v, widget.NewLabelWithStyle(title, fyne.TextAlignCenter, fyne.TextStyle{})))
		}
		cards := container.NewGridWithColumns(6,
			card(T("Всего задач"), strconv.Itoa(tot.Total)), card(T("Выполнено"), strconv.Itoa(tot.Done)),
			card(T("В работе"), strconv.Itoa(tot.InProgress)), card(T("Просрочено"), strconv.Itoa(tot.Overdue)),
			card(T("% просрочки"), pct(tot.OverduePct)), card(T("Ср. время"), hours(tot.AvgHours)))
		primary := theme.Color(theme.ColorNamePrimary)
		var sl []string
		var sv []float64
		for _, s := range tot.ByStage {
			sl, sv = append(sl, shorten(s.Stage, 14)), append(sv, float64(s.Count))
		}
		stage := newBarChart(T("Задачи по этапам"), sl, nil, []color.Color{primary}, sv)
		var pv []float64
		for _, n := range r.ByPriority {
			pv = append(pv, float64(n))
		}
		prioChart := newBarChart(T("Задачи по приоритету"), priorityNames(), nil, []color.Color{priorityColors[2]}, pv)
		var dl []string
		var dc, dd []float64
		for _, p := range r.Dynamics {
			dl = append(dl, time.UnixMilli(p.Day).Format("02.01"))
			dc, dd = append(dc, float64(p.Created)), append(dd, float64(p.Completed))
		}
		dyn := newBarChart(T("Динамика: создано и выполнено по дням"), dl, []string{T("создано"), T("выполнено")},
			[]color.Color{primary, color.NRGBA{0x22, 0xc5, 0x5e, 0xff}}, dc, dd)
		var ul []string
		var ud, uo []float64
		for i, g := range r.Users {
			if i == 12 {
				break
			}
			ul = append(ul, shorten(g.Name, 14))
			ud, uo = append(ud, float64(g.Done)), append(uo, float64(g.Overdue))
		}
		users := newBarChart(T("Исполнители: выполнено и просрочено"), ul, []string{T("выполнено"), T("просрочено")},
			[]color.Color{color.NRGBA{0x22, 0xc5, 0x5e, 0xff}, color.NRGBA{0xef, 0x44, 0x44, 0xff}}, ud, uo)
		summary.Objects = []fyne.CanvasObject{cards, container.NewGridWithColumns(2, stage, prioChart), dyn, users}
		summary.Refresh()
	}
	build := func() {
		f, t, err := period()
		if err != nil {
			showErr(err, w)
			return
		}
		rf := core.ReportFilter{From: f, To: t, DeptID: deptID(), UserID: uid(), Priority: prio.SelectedIndex() - 1}
		if i := board.SelectedIndex(); i > 0 {
			rf.BoardID = boards[i-1].ID
		}
		if i := tag.SelectedIndex(); i > 0 {
			rf.TagID = tags[i-1].ID
		}
		async(func() (*core.Report, error) { return u.ss.api.Report(rf) }, func(r *core.Report, err error) {
			if err != nil {
				showErr(err, w)
				return
			}
			show(r)
		})
	}
	stamp := func() string { return time.Now().Format("2006-01-02") }
	csvBtn := widget.NewButtonWithIcon("CSV", theme.DownloadIcon(), func() {
		if rep == nil {
			return
		}
		saveBytes(w, "egida-report-"+stamp()+".csv", toCSV(append([][]string{taskHdr}, taskRows...)))
	})
	xlsBtn := widget.NewButtonWithIcon("Excel", theme.DownloadIcon(), func() {
		if rep == nil {
			return
		}
		hdr := func(h []string) []any {
			out := make([]any, len(h))
			for i, s := range h {
				out[i] = s
			}
			return out
		}
		sh := []sheet{{Name: T("Сводка")}, {Name: T("По отделам")}, {Name: T("По исполнителям")}, {Name: T("Задачи")}}
		sh[0].Rows = [][]any{hdr(statsHeader()), statsAny(rep.Total)}
		for _, s := range rep.Total.ByStage {
			sh[0].Rows = append(sh[0].Rows, []any{T("Этап") + ": " + s.Stage, s.Count})
		}
		sh[1].Rows = [][]any{hdr(statsHeader())}
		for _, g := range rep.Depts {
			sh[1].Rows = append(sh[1].Rows, statsAny(g))
		}
		sh[2].Rows = [][]any{hdr(statsHeader())}
		for _, g := range rep.Users {
			sh[2].Rows = append(sh[2].Rows, statsAny(g))
		}
		sh[3].Rows = [][]any{hdr(taskHdr)}
		for i, t := range rep.Tasks {
			r := taskRows[i]
			sh[3].Rows = append(sh[3].Rows, []any{t.ID, t.Title, t.Board, t.Column, t.Department, r[5], t.Responsible, r[7], r[8], r[9], r[10]})
		}
		data, err := toXLSX(sh)
		if err != nil {
			showErr(err, w)
			return
		}
		saveBytes(w, "egida-report-"+stamp()+".xlsx", data)
	})
	sz := func(o fyne.CanvasObject, wd float32) fyne.CanvasObject {
		return container.NewGridWrap(fyne.NewSize(wd, 36), o)
	}
	bar := container.NewVBox(
		container.NewHBox(widget.NewLabel(T("Период: с")), sz(from, 120), widget.NewLabel(T("по")), sz(to, 120),
			sz(dept, 200), sz(usr, 220), sz(board, 200)),
		container.NewHBox(sz(prio, 200), sz(tag, 180), widget.NewButtonWithIcon(T("Построить"), theme.ViewRefreshIcon(), build),
			widget.NewLabel(T("Экспорт:")), csvBtn, xlsBtn),
	)
	w.SetContent(container.NewBorder(container.NewPadded(bar), nil, nil, nil, tabs))
	w.Resize(fyne.NewSize(1240, 820))
	w.Show()
	build()
}
