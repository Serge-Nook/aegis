package main

import (
	"errors"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/Serge-Nook/aegis/internal/core"
)

const dateLayout = "02.01.2006 15:04"

// taskWin is the window of one task: properties, checklist, comments, files and history.
type taskWin struct {
	u       *ui
	id      int64
	boardID int64
	colID   int64
	win     fyne.Window
	d       *core.TaskDetail
	cols    []core.Column
	// choices shown in the stage and responsible dropdowns at the last fill
	colOpts  []core.Column
	respOpts []core.User

	dirty, filling bool
	title          *widget.Entry
	desc           *widget.Entry
	column         *widget.Select
	prio           *widget.Select
	deadline       *widget.Entry
	resp           *widget.Select
	coexec         []int64
	watchers       []int64
	tags           []int64
	coBtn, waBtn   *widget.Button
	tagBtn         *widget.Button
	saveBtn        *widget.Button

	checklist, comments, files, history *fyne.Container
	progress                            *widget.ProgressBar
	tabs                                *container.AppTabs
	actions                             *fyne.Container
}

func (u *ui) openTask(id int64) {
	if tw, ok := u.taskWins[id]; ok {
		tw.win.RequestFocus()
		return
	}
	tw := u.newTaskWin(id, 0, 0)
	tw.reload()
}

func (u *ui) newTask(colID int64) {
	v := u.curView()
	if v == nil {
		dialog.ShowInformation(appName, T("Сначала выберите доску."), u.win)
		return
	}
	if colID == 0 && len(v.Columns) > 0 {
		colID = v.Columns[0].ID
	}
	tw := u.newTaskWin(0, v.Board.ID, colID)
	tw.cols = v.Columns
	tw.fill()
}

func (u *ui) newTaskWin(id, boardID, colID int64) *taskWin {
	tw := &taskWin{u: u, id: id, boardID: boardID, colID: colID}
	tw.win = u.app.NewWindow(T("Задача"))
	tw.win.Resize(fyne.NewSize(860, 720))
	tw.build()
	if id != 0 {
		u.taskWins[id] = tw
	}
	tw.win.SetOnClosed(func() {
		if u.taskWins[tw.id] == tw {
			delete(u.taskWins, tw.id)
		}
	})
	tw.win.Show()
	return tw
}

func (tw *taskWin) changed() {
	if !tw.filling {
		tw.dirty = true
	}
}

func (tw *taskWin) build() {
	u := tw.u
	tw.title = widget.NewEntry()
	tw.title.OnChanged = func(string) { tw.changed() }
	tw.desc = widget.NewMultiLineEntry()
	tw.desc.Wrapping = fyne.TextWrapWord
	tw.desc.SetMinRowsVisible(6)
	tw.desc.OnChanged = func(string) { tw.changed() }
	tw.column = widget.NewSelect(nil, func(string) { tw.changed() })
	tw.prio = widget.NewSelect(priorityNames(), func(string) { tw.changed() })
	tw.deadline = widget.NewEntry()
	tw.deadline.SetPlaceHolder(T("ДД.ММ.ГГГГ ЧЧ:ММ"))
	tw.deadline.OnChanged = func(string) { tw.changed() }
	cal := widget.NewButtonWithIcon("", theme.CalendarIcon(), nil)
	cal.OnTapped = func() {
		start := time.Now()
		if t, err := time.ParseInLocation(dateLayout, tw.deadline.Text, time.Local); err == nil {
			start = t
		}
		var pop *widget.PopUp
		c := widget.NewCalendar(start, func(t time.Time) {
			hh, mm := 18, 0
			if old, err := time.ParseInLocation(dateLayout, tw.deadline.Text, time.Local); err == nil {
				hh, mm = old.Hour(), old.Minute()
			}
			tw.deadline.SetText(time.Date(t.Year(), t.Month(), t.Day(), hh, mm, 0, 0, time.Local).Format(dateLayout))
			pop.Hide()
		})
		pop = widget.NewPopUp(c, tw.win.Canvas())
		pop.ShowAtPosition(fyne.CurrentApp().Driver().AbsolutePositionForObject(cal).Add(fyne.NewPos(0, cal.Size().Height)))
	}
	clr := widget.NewButtonWithIcon("", theme.ContentClearIcon(), func() { tw.deadline.SetText("") })
	tw.resp = widget.NewSelect(nil, func(string) { tw.changed() })
	tw.coBtn = widget.NewButton("", func() { tw.pickUsers(T("Соисполнители"), &tw.coexec, tw.coBtn) })
	tw.waBtn = widget.NewButton("", func() { tw.pickUsers(T("Наблюдатели"), &tw.watchers, tw.waBtn) })
	tw.tagBtn = widget.NewButton("", tw.pickTags)
	tw.saveBtn = widget.NewButtonWithIcon(T("Сохранить"), theme.DocumentSaveIcon(), tw.save)
	tw.saveBtn.Importance = widget.HighImportance
	tw.actions = container.NewHBox()

	form := widget.NewForm(
		widget.NewFormItem(T("Название"), tw.title),
		widget.NewFormItem(T("Описание"), tw.desc),
		widget.NewFormItem(T("Этап"), tw.column),
		widget.NewFormItem(T("Приоритет"), tw.prio),
		widget.NewFormItem(T("Срок"), container.NewBorder(nil, nil, nil, container.NewHBox(cal, clr), tw.deadline)),
		widget.NewFormItem(T("Ответственный"), tw.resp),
		widget.NewFormItem(T("Соисполнители"), tw.coBtn),
		widget.NewFormItem(T("Наблюдатели"), tw.waBtn),
		widget.NewFormItem(T("Теги"), tw.tagBtn),
	)
	main := container.NewBorder(nil, container.NewBorder(nil, nil, tw.actions, tw.saveBtn), nil, nil, container.NewVScroll(form))

	tw.checklist = container.NewVBox()
	tw.progress = widget.NewProgressBar()
	newItem := widget.NewEntry()
	newItem.SetPlaceHolder(T("Новый пункт чек-листа…"))
	addItem := func() {
		txt := strings.TrimSpace(newItem.Text)
		if txt == "" || tw.id == 0 {
			return
		}
		newItem.SetText("")
		tw.run(func() error {
			_, err := u.ss.api.SaveSubtask(core.Subtask{TaskID: tw.id, Text: txt, Pos: 1 << 30})
			return err
		})
	}
	newItem.OnSubmitted = func(string) { addItem() }
	checkTab := container.NewBorder(tw.progress,
		container.NewBorder(nil, nil, nil, widget.NewButtonWithIcon("", theme.ContentAddIcon(), addItem), newItem),
		nil, nil, container.NewVScroll(tw.checklist))

	tw.comments = container.NewVBox()
	msg := widget.NewMultiLineEntry()
	msg.Wrapping = fyne.TextWrapWord
	msg.SetMinRowsVisible(3)
	msg.SetPlaceHolder(T("Комментарий… Упомяните коллегу через @логин"))
	send := widget.NewButtonWithIcon(T("Отправить"), theme.MailSendIcon(), func() {
		txt := strings.TrimSpace(msg.Text)
		if txt == "" || tw.id == 0 {
			return
		}
		tw.run(func() error {
			_, err := u.ss.api.AddComment(tw.id, txt)
			if err == nil {
				fyne.Do(func() { msg.SetText("") })
			}
			return err
		})
	})
	mention := widget.NewButtonWithIcon("@", theme.AccountIcon(), func() {
		tw.pickMention(func(login string) { msg.SetText(msg.Text + "@" + login + " ") })
	})
	commTab := container.NewBorder(nil, container.NewBorder(nil, nil, nil, container.NewVBox(send, mention), msg), nil, nil,
		container.NewVScroll(tw.comments))

	tw.files = container.NewVBox()
	addFile := widget.NewButtonWithIcon(T("Прикрепить файл…"), theme.ContentAddIcon(), tw.attach)
	filesTab := container.NewBorder(nil, container.NewHBox(addFile), nil, nil, container.NewVScroll(tw.files))

	tw.history = container.NewVBox()
	tw.tabs = container.NewAppTabs(
		container.NewTabItemWithIcon(T("Основное"), theme.DocumentIcon(), main),
		container.NewTabItemWithIcon(T("Чек-лист"), theme.CheckButtonCheckedIcon(), checkTab),
		container.NewTabItemWithIcon(T("Комментарии"), theme.MailComposeIcon(), commTab),
		container.NewTabItemWithIcon(T("Файлы"), theme.FileIcon(), filesTab),
		container.NewTabItemWithIcon(T("История"), theme.HistoryIcon(), container.NewVScroll(tw.history)),
	)
	tw.win.SetContent(container.NewPadded(tw.tabs))
}

// run performs a change and reloads the task window and the board.
func (tw *taskWin) run(fn func() error) {
	async(func() (bool, error) { return true, fn() }, func(_ bool, err error) {
		if err != nil {
			tw.u.handleErr(err)
		}
		tw.reload()
		tw.u.reloadBoard()
	})
}

func (tw *taskWin) reload() {
	if tw.id == 0 {
		return
	}
	u := tw.u
	async(func() (*core.TaskDetail, error) {
		d, err := u.ss.api.Task(tw.id)
		if err != nil {
			return nil, err
		}
		f := core.AnyFilter()
		f.Query = "#-1"
		if v, err := u.ss.api.BoardView(d.Task.BoardID, f); err == nil {
			tw.cols = v.Columns
		}
		return d, nil
	}, func(d *core.TaskDetail, err error) {
		if err != nil {
			if core.CodeOf(err) == core.CodeNotFound {
				tw.win.Close()
				return
			}
			u.handleErr(err)
			return
		}
		tw.d = d
		tw.boardID, tw.colID = d.Task.BoardID, d.Task.ColumnID
		if !tw.dirty {
			tw.fill()
		}
		tw.fillTabs()
	})
}

func (tw *taskWin) editable() bool {
	return tw.u.canWrite() && (tw.d == nil || tw.d.CanEdit)
}

// fill copies the task into the form (or prepares an empty form for a new task).
func (tw *taskWin) fill() {
	u := tw.u
	tw.filling = true
	defer func() { tw.filling = false }()
	t := core.Task{}
	t.Priority = core.PriorityMedium
	if tw.d != nil {
		t = tw.d.Task
		tw.win.SetTitle("#" + strconv.FormatInt(t.ID, 10) + " " + t.Title + " — " + appName)
	} else {
		tw.win.SetTitle(T("Новая задача") + " — " + appName)
	}
	tw.title.SetText(t.Title)
	tw.desc.SetText(t.Description)
	tw.colOpts = tw.cols
	var names []string
	for _, c := range tw.cols {
		names = append(names, c.Name)
	}
	tw.column.Options = names
	for i, c := range tw.cols {
		if c.ID == tw.colID {
			tw.column.SetSelectedIndex(i)
		}
	}
	tw.prio.SetSelectedIndex(clampPrio(t.Priority))
	if t.Deadline > 0 {
		tw.deadline.SetText(time.UnixMilli(t.Deadline).Format(dateLayout))
	} else {
		tw.deadline.SetText("")
	}
	users := tw.respUsers()
	tw.respOpts = users
	opts := []string{T("— не назначен —")}
	sel := 0
	for i, x := range users {
		opts = append(opts, userLabel(x))
		if x.ID == t.ResponsibleID {
			sel = i + 1
		}
	}
	tw.resp.Options = opts
	tw.resp.SetSelectedIndex(sel)
	tw.coexec, tw.watchers, tw.tags = append([]int64(nil), t.CoExecutors...), append([]int64(nil), t.Watchers...), append([]int64(nil), t.Tags...)
	tw.coBtn.SetText(tw.namesOf(tw.coexec))
	tw.waBtn.SetText(tw.namesOf(tw.watchers))
	tw.tagBtn.SetText(tw.tagNames())

	ed := tw.editable()
	for _, w := range []fyne.Disableable{tw.title, tw.desc, tw.column, tw.prio, tw.deadline, tw.resp, tw.coBtn, tw.waBtn, tw.tagBtn, tw.saveBtn} {
		if ed {
			w.Enable()
		} else {
			w.Disable()
		}
	}
	tw.actions.Objects = nil
	if tw.d != nil && u.canWrite() {
		arch := T("В архив")
		if t.Archived {
			arch = T("Вернуть из архива")
		}
		tw.actions.Add(widget.NewButtonWithIcon(arch, theme.FolderIcon(), func() {
			tw.run(func() error { return u.ss.api.ArchiveTask(tw.id, !t.Archived) })
		}))
		tw.actions.Add(widget.NewButtonWithIcon(T("Дублировать"), theme.ContentCopyIcon(), tw.copyTask))
		if !ed {
			tw.actions.Objects[0].(*widget.Button).Disable()
		}
	}
	if tw.d != nil {
		tw.actions.Add(widget.NewLabel(Tf("Автор: %s, создана %s", u.userName(t.AuthorID), fmtTime(t.CreatedAt))))
	}
	tw.actions.Refresh()
	tw.dirty = false
}

func (tw *taskWin) activeUsers() []core.User {
	u := tw.u
	u.mu.Lock()
	defer u.mu.Unlock()
	var out []core.User
	for _, x := range u.users {
		if x.Active {
			out = append(out, x)
		}
	}
	return out
}

// respUsers are the choices for the responsible user: active users plus the
// current one even if disabled, so that saving does not drop him silently.
func (tw *taskWin) respUsers() []core.User {
	out := tw.activeUsers()
	if tw.d == nil || tw.d.Task.ResponsibleID == 0 {
		return out
	}
	for _, x := range out {
		if x.ID == tw.d.Task.ResponsibleID {
			return out
		}
	}
	u := tw.u
	u.mu.Lock()
	defer u.mu.Unlock()
	for _, x := range u.users {
		if x.ID == tw.d.Task.ResponsibleID {
			out = append(out, x)
		}
	}
	return out
}

func (tw *taskWin) namesOf(ids []int64) string {
	if len(ids) == 0 {
		return T("— нет —")
	}
	var s []string
	for _, id := range ids {
		s = append(s, tw.u.userName(id))
	}
	return shorten(strings.Join(s, ", "), 80)
}

func (tw *taskWin) tagNames() string {
	if len(tw.tags) == 0 {
		return T("— нет —")
	}
	u := tw.u
	u.mu.Lock()
	defer u.mu.Unlock()
	var s []string
	for _, id := range tw.tags {
		s = append(s, u.tagByID[id].Name)
	}
	return shorten(strings.Join(s, ", "), 80)
}

func (tw *taskWin) pickUsers(title string, ids *[]int64, btn *widget.Button) {
	users := tw.activeUsers()
	var opts []string
	byLabel := map[string]int64{}
	var selected []string
	for _, x := range users {
		l := userLabel(x)
		opts = append(opts, l)
		byLabel[l] = x.ID
		for _, id := range *ids {
			if id == x.ID {
				selected = append(selected, l)
			}
		}
	}
	cg := widget.NewCheckGroup(opts, nil)
	cg.SetSelected(selected)
	d := dialog.NewCustomConfirm(title, T("Готово"), T("Отмена"), container.NewVScroll(cg), func(ok bool) {
		if !ok {
			return
		}
		var out []int64
		for _, l := range cg.Selected {
			out = append(out, byLabel[l])
		}
		*ids = out
		btn.SetText(tw.namesOf(out))
		tw.changed()
	}, tw.win)
	d.Resize(fyne.NewSize(420, 460))
	d.Show()
}

func (tw *taskWin) pickTags() {
	u := tw.u
	u.mu.Lock()
	tags := append([]core.Tag(nil), u.tags...)
	u.mu.Unlock()
	var opts, selected []string
	byName := map[string]int64{}
	for _, t := range tags {
		opts = append(opts, t.Name)
		byName[t.Name] = t.ID
		for _, id := range tw.tags {
			if id == t.ID {
				selected = append(selected, t.Name)
			}
		}
	}
	if len(opts) == 0 {
		dialog.ShowInformation(T("Теги"), T("Теги ещё не созданы (меню «Справочники → Теги»)."), tw.win)
		return
	}
	cg := widget.NewCheckGroup(opts, nil)
	cg.SetSelected(selected)
	d := dialog.NewCustomConfirm(T("Теги"), T("Готово"), T("Отмена"), container.NewVScroll(cg), func(ok bool) {
		if !ok {
			return
		}
		var out []int64
		for _, n := range cg.Selected {
			out = append(out, byName[n])
		}
		tw.tags = out
		tw.tagBtn.SetText(tw.tagNames())
		tw.changed()
	}, tw.win)
	d.Resize(fyne.NewSize(360, 420))
	d.Show()
}

func (tw *taskWin) pickMention(done func(login string)) {
	users := tw.activeUsers()
	list := widget.NewList(func() int { return len(users) }, func() fyne.CanvasObject { return widget.NewLabel("") },
		func(i widget.ListItemID, o fyne.CanvasObject) { o.(*widget.Label).SetText(userLabel(users[i])) })
	var d dialog.Dialog
	list.OnSelected = func(i widget.ListItemID) { done(users[i].Login); d.Hide() }
	d = dialog.NewCustom(T("Упомянуть"), T("Отмена"), list, tw.win)
	d.Resize(fyne.NewSize(380, 420))
	d.Show()
}

func (tw *taskWin) save() {
	u := tw.u
	in := core.TaskInput{ID: tw.id, BoardID: tw.boardID, ColumnID: tw.colID, Title: strings.TrimSpace(tw.title.Text),
		Description: tw.desc.Text, Priority: core.Priority(max(tw.prio.SelectedIndex(), 0)),
		CoExecutors: tw.coexec, Watchers: tw.watchers, Tags: tw.tags}
	if in.Title == "" {
		dialog.ShowError(errors.New(T("Укажите название задачи.")), tw.win)
		return
	}
	if s := strings.TrimSpace(tw.deadline.Text); s != "" {
		t, err := time.ParseInLocation(dateLayout, s, time.Local)
		if err != nil {
			if t, err = time.ParseInLocation("02.01.2006", s, time.Local); err == nil {
				t = t.Add(18 * time.Hour)
			}
		}
		if err != nil {
			dialog.ShowError(errors.New(T("Срок должен быть в формате ДД.ММ.ГГГГ ЧЧ:ММ.")), tw.win)
			return
		}
		in.Deadline = t.UnixMilli()
	}
	if i := tw.resp.SelectedIndex(); i > 0 {
		in.ResponsibleID = tw.respOpts[i-1].ID
	}
	newCol := tw.colID
	if i := tw.column.SelectedIndex(); i >= 0 && i < len(tw.colOpts) {
		newCol = tw.colOpts[i].ID
	}
	if tw.id == 0 {
		in.ColumnID = newCol
	}
	async(func() (int64, error) {
		id, err := u.ss.api.SaveTask(in)
		if err == nil && tw.id != 0 && newCol != tw.colID {
			err = u.ss.api.MoveTask(id, newCol, 1<<30)
		}
		return id, err
	}, func(id int64, err error) {
		if err != nil {
			u.handleErr(err)
			return
		}
		tw.dirty = false
		if tw.id == 0 {
			tw.id = id
			u.taskWins[id] = tw
		}
		tw.reload()
		u.reloadBoard()
	})
}

func (tw *taskWin) copyTask() {
	u := tw.u
	u.mu.Lock()
	boards := append([]core.Board(nil), u.boards...)
	u.mu.Unlock()
	var names []string
	sel := 0
	for i, b := range boards {
		names = append(names, b.Name)
		if b.ID == tw.boardID {
			sel = i
		}
	}
	s := widget.NewSelect(names, nil)
	if len(names) > 0 {
		s.SetSelectedIndex(sel)
	}
	dialog.ShowForm(T("Дублировать задачу"), T("Создать копию"), T("Отмена"), []*widget.FormItem{widget.NewFormItem(T("На доску"), s)}, func(ok bool) {
		if !ok || s.SelectedIndex() < 0 {
			return
		}
		b := boards[s.SelectedIndex()].ID
		async(func() (int64, error) { return u.ss.api.CopyTask(tw.id, b) }, func(id int64, err error) {
			if err != nil {
				u.handleErr(err)
				return
			}
			u.reloadBoard()
			u.openTask(id)
		})
	}, tw.win)
}

func (tw *taskWin) fillTabs() {
	u, d := tw.u, tw.d
	ed := tw.editable()
	// checklist
	tw.checklist.Objects = nil
	done := 0
	for i, st := range d.Subtasks {
		st, i := st, i
		if st.Done {
			done++
		}
		chk := widget.NewCheck(st.Text, func(v bool) {
			if v == st.Done {
				return
			}
			s := st
			s.Done = v
			tw.run(func() error { _, err := u.ss.api.SaveSubtask(s); return err })
		})
		chk.Checked = st.Done
		btns := container.NewHBox()
		if ed {
			btns.Add(lowBtn(theme.MoveUpIcon(), i > 0, func() { tw.run(func() error { return u.ss.api.MoveSubtask(st.ID, i-1) }) }))
			btns.Add(lowBtn(theme.MoveDownIcon(), i < len(d.Subtasks)-1, func() { tw.run(func() error { return u.ss.api.MoveSubtask(st.ID, i+1) }) }))
			btns.Add(lowBtn(theme.DocumentCreateIcon(), true, func() {
				e := widget.NewEntry()
				e.SetText(st.Text)
				dialog.ShowForm(T("Пункт чек-листа"), T("Сохранить"), T("Отмена"), []*widget.FormItem{widget.NewFormItem(T("Текст"), e)}, func(ok bool) {
					if ok {
						s := st
						s.Text = e.Text
						tw.run(func() error { _, err := u.ss.api.SaveSubtask(s); return err })
					}
				}, tw.win)
			}))
			btns.Add(lowBtn(theme.DeleteIcon(), true, func() { tw.run(func() error { return u.ss.api.DeleteSubtask(st.ID) }) }))
		} else {
			chk.Disable()
		}
		tw.checklist.Add(container.NewBorder(nil, nil, nil, btns, chk))
	}
	if len(d.Subtasks) == 0 {
		tw.checklist.Add(widget.NewLabel(T("Пунктов пока нет.")))
	}
	tw.progress.Max = float64(max(len(d.Subtasks), 1))
	tw.progress.SetValue(float64(done))
	tw.tabs.Items[1].Text = T("Чек-лист") + " " + strconv.Itoa(done) + "/" + strconv.Itoa(len(d.Subtasks))

	// comments
	tw.comments.Objects = nil
	for _, c := range d.Comments {
		c := c
		head := u.userName(c.AuthorID) + " · " + fmtTime(c.CreatedAt)
		if c.EditedAt > 0 {
			head += " · " + T("изменён") + " " + fmtTime(c.EditedAt)
		}
		h := widget.NewLabelWithStyle(head, fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
		body := widget.NewLabel(c.Text)
		body.Wrapping = fyne.TextWrapWord
		btns := container.NewHBox()
		mine := c.AuthorID == u.me().ID
		if u.canWrite() && (mine || u.isAdmin()) {
			if mine {
				btns.Add(lowBtn(theme.DocumentCreateIcon(), true, func() {
					e := widget.NewMultiLineEntry()
					e.SetText(c.Text)
					e.SetMinRowsVisible(4)
					dd := dialog.NewForm(T("Изменить комментарий"), T("Сохранить"), T("Отмена"), []*widget.FormItem{widget.NewFormItem("", e)}, func(ok bool) {
						if ok {
							tw.run(func() error { return u.ss.api.EditComment(c.ID, e.Text) })
						}
					}, tw.win)
					dd.Resize(fyne.NewSize(520, 260))
					dd.Show()
				}))
			}
			btns.Add(lowBtn(theme.DeleteIcon(), true, func() {
				dialog.ShowConfirm(T("Удалить комментарий"), T("Удалить комментарий?"), func(ok bool) {
					if ok {
						tw.run(func() error { return u.ss.api.DeleteComment(c.ID) })
					}
				}, tw.win)
			}))
		}
		tw.comments.Add(widget.NewCard("", "", container.NewBorder(container.NewBorder(nil, nil, nil, btns, h), nil, nil, nil, body)))
	}
	if len(d.Comments) == 0 {
		tw.comments.Add(widget.NewLabel(T("Комментариев пока нет.")))
	}
	tw.tabs.Items[2].Text = T("Комментарии") + " " + strconv.Itoa(len(d.Comments))

	// files
	tw.files.Objects = nil
	for _, a := range d.Attachments {
		a := a
		lbl := widget.NewLabel(a.Name + "  (" + fmtSize(a.Size) + ", " + u.userName(a.AuthorID) + ", " + fmtTime(a.CreatedAt) + ")")
		lbl.Truncation = fyne.TextTruncateEllipsis
		btns := container.NewHBox(widget.NewButtonWithIcon(T("Сохранить…"), theme.DownloadIcon(), func() {
			async(func() ([]byte, error) { return u.ss.api.Attachment(a.ID) }, func(b []byte, err error) {
				if err != nil {
					u.handleErr(err)
					return
				}
				saveBytes(tw.win, a.Name, b)
			})
		}))
		if ed || a.AuthorID == u.me().ID && u.canWrite() {
			btns.Add(lowBtn(theme.DeleteIcon(), true, func() {
				dialog.ShowConfirm(T("Удалить файл"), Tf("Удалить файл «%s»?", a.Name), func(ok bool) {
					if ok {
						tw.run(func() error { return u.ss.api.DeleteAttachment(a.ID) })
					}
				}, tw.win)
			}))
		}
		tw.files.Add(container.NewBorder(nil, nil, widget.NewIcon(theme.FileIcon()), btns, lbl))
	}
	if len(d.Attachments) == 0 {
		tw.files.Add(widget.NewLabel(T("Файлов нет.")))
	}
	tw.tabs.Items[3].Text = T("Файлы") + " " + strconv.Itoa(len(d.Attachments))

	// history
	tw.history.Objects = nil
	hist := append([]core.HistoryEntry(nil), d.History...)
	sort.Slice(hist, func(i, j int) bool { return hist[i].Created > hist[j].Created })
	for _, h := range hist {
		l := widget.NewLabel(fmtTime(h.Created) + "  " + u.userName(h.UserID) + ": " + historyText(h))
		l.Wrapping = fyne.TextWrapWord
		tw.history.Add(l)
	}
	tw.tabs.Refresh()
}

func lowBtn(icon fyne.Resource, enabled bool, fn func()) *widget.Button {
	b := widget.NewButtonWithIcon("", icon, fn)
	b.Importance = widget.LowImportance
	if !enabled {
		b.Disable()
	}
	return b
}

func (tw *taskWin) attach() {
	u := tw.u
	if tw.id == 0 {
		dialog.ShowInformation(appName, T("Сначала сохраните задачу."), tw.win)
		return
	}
	d := dialog.NewFileOpen(func(r fyne.URIReadCloser, err error) {
		if err != nil || r == nil {
			return
		}
		data, err := io.ReadAll(r)
		r.Close()
		if err != nil {
			showErr(err, tw.win)
			return
		}
		name := r.URI().Name()
		tw.run(func() error { _, err := u.ss.api.AddAttachment(tw.id, name, data); return err })
	}, tw.win)
	d.Show()
	d.Resize(fyne.NewSize(760, 520))
}

var fieldNames = map[string]string{
	"title": "название", "description": "описание", "priority": "приоритет", "deadline": "срок",
	"responsible": "ответственный", "coexecutors": "соисполнители", "watchers": "наблюдатели", "tags": "теги",
}

// historyText renders an audit log entry in the current language.
func historyText(h core.HistoryEntry) string {
	d := h.Details
	switch h.Entity + "." + h.Action {
	case "task.create":
		return Tf("создал(а) задачу «%s»", d)
	case "task.update":
		var f []string
		for _, x := range strings.Split(d, ",") {
			if n, ok := fieldNames[x]; ok {
				f = append(f, T(n))
			}
		}
		return T("изменил(а)") + ": " + strings.Join(f, ", ")
	case "task.move":
		from, to, _ := strings.Cut(d, "\x1f")
		return Tf("переместил(а) задачу: %s → %s", from, to)
	case "task.copy":
		return Tf("создал(а) копию задачи %s", d)
	case "task.archive":
		return T("отправил(а) задачу в архив")
	case "task.unarchive":
		return T("вернул(а) задачу из архива")
	case "subtask.subtask_add":
		return Tf("добавил(а) пункт чек-листа «%s»", d)
	case "subtask.subtask_edit":
		return Tf("изменил(а) пункт чек-листа «%s»", d)
	case "subtask.subtask_done":
		return Tf("выполнил(а) пункт «%s»", d)
	case "subtask.subtask_undone":
		return Tf("снял(а) отметку с пункта «%s»", d)
	case "subtask.subtask_delete":
		return Tf("удалил(а) пункт чек-листа «%s»", d)
	case "comment.comment":
		return Tf("прокомментировал(а): %s", d)
	case "comment.comment_edit":
		return Tf("изменил(а) комментарий: %s", d)
	case "comment.comment_delete":
		return Tf("удалил(а) комментарий: %s", d)
	case "attachment.attach":
		return Tf("прикрепил(а) файл «%s»", d)
	case "attachment.attach_delete":
		return Tf("удалил(а) файл «%s»", d)
	}
	ent := map[string]string{"board": "доска", "column": "колонка", "user": "пользователь", "department": "отдел", "settings": "настройки", "task": "задача"}[h.Entity]
	act := map[string]string{"create": "создание", "update": "изменение", "delete": "удаление", "move": "перемещение",
		"archive": "в архив", "unarchive": "из архива", "password": "смена пароля"}[h.Action]
	if ent == "" {
		ent = h.Entity
	}
	if act == "" {
		act = h.Action
	}
	s := T(ent) + ": " + T(act)
	if d != "" {
		s += " «" + strings.ReplaceAll(d, "\x1f", " → ") + "»"
	}
	return s
}
