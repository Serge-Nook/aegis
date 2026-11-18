package main

import (
	"errors"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/canvas"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"

	"github.com/Serge-Nook/aegis/internal/core"
)

// dictWin is a reusable window listing the items of a dictionary with add/edit/delete buttons.
type dictWin struct {
	u      *ui
	win    fyne.Window
	list   *widget.List
	rows   []string
	sel    int
	load   func() ([]string, error)
	edit   func(i int) // i == -1 creates a new item
	delete func(i int)
}

func (u *ui) newDictWin(title string, load func() ([]string, error), edit, del func(int)) *dictWin {
	d := &dictWin{u: u, load: load, edit: edit, delete: del, sel: -1}
	d.win = u.app.NewWindow(title + " — " + appName)
	d.win.Resize(fyne.NewSize(640, 520))
	d.list = widget.NewList(func() int { return len(d.rows) }, func() fyne.CanvasObject { return widget.NewLabel("") },
		func(i widget.ListItemID, o fyne.CanvasObject) { o.(*widget.Label).SetText(d.rows[i]) })
	d.list.OnSelected = func(i widget.ListItemID) { d.sel = i }
	add := widget.NewButtonWithIcon(T("Добавить"), theme.ContentAddIcon(), func() { edit(-1) })
	ed := widget.NewButtonWithIcon(T("Изменить"), theme.DocumentCreateIcon(), func() {
		if d.sel >= 0 && d.sel < len(d.rows) {
			edit(d.sel)
		}
	})
	bar := container.NewHBox(add, ed)
	if del != nil {
		bar.Add(widget.NewButtonWithIcon(T("Удалить"), theme.DeleteIcon(), func() {
			if d.sel < 0 || d.sel >= len(d.rows) {
				return
			}
			i := d.sel
			dialog.ShowConfirm(T("Удаление"), Tf("Удалить «%s»?", d.rows[i]), func(ok bool) {
				if ok {
					del(i)
				}
			}, d.win)
		}))
	}
	if !u.canWrite() {
		for _, o := range bar.Objects {
			o.(*widget.Button).Disable()
		}
	}
	d.list.OnSelected = func(i widget.ListItemID) { d.sel = i }
	d.win.SetContent(container.NewBorder(nil, container.NewPadded(bar), nil, nil, d.list))
	d.reload()
	d.win.Show()
	return d
}

func (d *dictWin) reload() {
	async(d.load, func(rows []string, err error) {
		if err != nil {
			d.u.handleErr(err)
			return
		}
		d.rows = rows
		d.sel = -1
		d.list.UnselectAll()
		d.list.Refresh()
	})
}

// change runs an edit, then refreshes the dictionary window and the main window.
func (d *dictWin) change(fn func() error) {
	async(func() (bool, error) { return true, fn() }, func(_ bool, err error) {
		if err != nil {
			d.u.handleErr(err)
		}
		d.reload()
		d.u.refreshAll()
	})
}

func (u *ui) usersCheck(selected []int64) (*widget.CheckGroup, func() []int64) {
	u.mu.Lock()
	users := append([]core.User(nil), u.users...)
	u.mu.Unlock()
	var opts, sel []string
	ids := map[string]int64{}
	for _, x := range users {
		l := userLabel(x)
		opts = append(opts, l)
		ids[l] = x.ID
		for _, id := range selected {
			if id == x.ID {
				sel = append(sel, l)
			}
		}
	}
	cg := widget.NewCheckGroup(opts, nil)
	cg.SetSelected(sel)
	return cg, func() []int64 {
		var out []int64
		for _, l := range cg.Selected {
			out = append(out, ids[l])
		}
		return out
	}
}

func (u *ui) showUsers() {
	var users []core.User
	var d *dictWin
	d = u.newDictWin(T("Пользователи"), func() ([]string, error) {
		var err error
		users, err = u.ss.api.Users()
		var rows []string
		for _, x := range users {
			r := userLabel(x) + " — " + roleName(x.Role)
			if !x.Active {
				r += " — " + T("заблокирован")
			}
			rows = append(rows, r)
		}
		return rows, err
	}, func(i int) {
		in := core.UserInput{Role: core.RoleExecutor, Active: true}
		if i >= 0 {
			x := users[i]
			in = core.UserInput{ID: x.ID, Login: x.Login, FullName: x.FullName, Email: x.Email, Role: x.Role, Active: x.Active, Departments: x.Departments}
		}
		login, name, mail, pass := widget.NewEntry(), widget.NewEntry(), widget.NewEntry(), widget.NewPasswordEntry()
		login.SetText(in.Login)
		name.SetText(in.FullName)
		mail.SetText(in.Email)
		if i >= 0 {
			pass.SetPlaceHolder(T("оставьте пустым, чтобы не менять"))
		}
		var roles []string
		for _, r := range core.Roles {
			roles = append(roles, roleName(r))
		}
		role := widget.NewSelect(roles, nil)
		for k, r := range core.Roles {
			if r == in.Role {
				role.SetSelectedIndex(k)
			}
		}
		active := widget.NewCheck(T("Активен (может входить)"), nil)
		active.SetChecked(in.Active)
		u.mu.Lock()
		depts := append([]core.Department(nil), u.depts...)
		u.mu.Unlock()
		var dn, dsel []string
		for _, dp := range depts {
			dn = append(dn, dp.Name)
			for _, id := range in.Departments {
				if id == dp.ID {
					dsel = append(dsel, dp.Name)
				}
			}
		}
		dg := widget.NewCheckGroup(dn, nil)
		dg.SetSelected(dsel)
		title := T("Новый пользователь")
		if i >= 0 {
			title = T("Пользователь")
		}
		fd := dialog.NewForm(title, T("Сохранить"), T("Отмена"), []*widget.FormItem{
			widget.NewFormItem(T("Логин"), login), widget.NewFormItem(T("ФИО"), name), widget.NewFormItem("E-mail", mail),
			widget.NewFormItem(T("Пароль"), pass), widget.NewFormItem(T("Роль"), role), widget.NewFormItem("", active),
			widget.NewFormItem(T("Отделы"), container.NewGridWrap(fyne.NewSize(320, 140), container.NewVScroll(dg))),
		}, func(ok bool) {
			if !ok {
				return
			}
			in.Login, in.FullName, in.Email, in.Password = strings.TrimSpace(login.Text), strings.TrimSpace(name.Text), strings.TrimSpace(mail.Text), pass.Text
			in.Role = core.Roles[max(role.SelectedIndex(), 0)]
			in.Active = active.Checked
			in.Departments = nil
			for _, n := range dg.Selected {
				for _, dp := range depts {
					if dp.Name == n {
						in.Departments = append(in.Departments, dp.ID)
					}
				}
			}
			d.change(func() error { _, err := u.ss.api.SaveUser(in); return err })
		}, d.win)
		fd.Resize(fyne.NewSize(520, 560))
		fd.Show()
	}, nil)
}

func (u *ui) showDepartments() {
	var depts []core.Department
	var d *dictWin
	d = u.newDictWin(T("Отделы"), func() ([]string, error) {
		var err error
		depts, err = u.ss.api.Departments()
		var rows []string
		for _, x := range depts {
			r := x.Name + " — " + Tf("сотрудников: %d", len(x.Members))
			if x.HeadID != 0 {
				r += ", " + T("руководитель") + ": " + u.userName(x.HeadID)
			}
			rows = append(rows, r)
		}
		return rows, err
	}, func(i int) {
		dp := core.Department{}
		if i >= 0 {
			dp = depts[i]
		}
		name := widget.NewEntry()
		name.SetText(dp.Name)
		u.mu.Lock()
		users := append([]core.User(nil), u.users...)
		u.mu.Unlock()
		opts := []string{T("— не назначен —")}
		head := widget.NewSelect(nil, nil)
		sel := 0
		for k, x := range users {
			opts = append(opts, userLabel(x))
			if x.ID == dp.HeadID {
				sel = k + 1
			}
		}
		head.Options = opts
		head.SetSelectedIndex(sel)
		members, get := u.usersCheck(dp.Members)
		fd := dialog.NewForm(T("Отдел"), T("Сохранить"), T("Отмена"), []*widget.FormItem{
			widget.NewFormItem(T("Название"), name), widget.NewFormItem(T("Руководитель"), head),
			widget.NewFormItem(T("Сотрудники"), container.NewGridWrap(fyne.NewSize(340, 220), container.NewVScroll(members))),
		}, func(ok bool) {
			if !ok {
				return
			}
			dp.Name = strings.TrimSpace(name.Text)
			dp.HeadID = 0
			if k := head.SelectedIndex(); k > 0 {
				dp.HeadID = users[k-1].ID
			}
			dp.Members = get()
			d.change(func() error { _, err := u.ss.api.SaveDepartment(dp); return err })
		}, d.win)
		fd.Resize(fyne.NewSize(520, 480))
		fd.Show()
	}, func(i int) {
		id := depts[i].ID
		d.change(func() error { return u.ss.api.DeleteDepartment(id) })
	})
}

func (u *ui) showTags() {
	var tags []core.Tag
	var d *dictWin
	d = u.newDictWin(T("Теги"), func() ([]string, error) {
		var err error
		tags, err = u.ss.api.Tags()
		var rows []string
		for _, t := range tags {
			rows = append(rows, t.Name+"  ("+t.Color+")")
		}
		return rows, err
	}, func(i int) {
		t := core.Tag{Color: tagPalette[len(tags)%len(tagPalette)]}
		if i >= 0 {
			t = tags[i]
		}
		name := widget.NewEntry()
		name.SetText(t.Name)
		swatch := canvas.NewRectangle(parseHex(t.Color))
		swatch.SetMinSize(fyne.NewSize(28, 28))
		swatch.CornerRadius = 4
		col := widget.NewSelect(tagPalette, func(s string) { swatch.FillColor = parseHex(s); swatch.Refresh() })
		col.SetSelected(t.Color)
		dialog.ShowForm(T("Тег"), T("Сохранить"), T("Отмена"), []*widget.FormItem{
			widget.NewFormItem(T("Название"), name), widget.NewFormItem(T("Цвет"), container.NewBorder(nil, nil, swatch, nil, col)),
		}, func(ok bool) {
			if ok {
				t.Name, t.Color = strings.TrimSpace(name.Text), col.Selected
				d.change(func() error { _, err := u.ss.api.SaveTag(t); return err })
			}
		}, d.win)
	}, func(i int) {
		id := tags[i].ID
		d.change(func() error { return u.ss.api.DeleteTag(id) })
	})
}

func (u *ui) showTemplates() {
	var tpls []core.Template
	var d *dictWin
	d = u.newDictWin(T("Шаблоны досок"), func() ([]string, error) {
		var err error
		tpls, err = u.ss.api.Templates()
		var rows []string
		for _, t := range tpls {
			rows = append(rows, t.Name+": "+strings.Join(t.Columns, " → "))
		}
		return rows, err
	}, func(i int) {
		t := core.Template{}
		if i >= 0 {
			t = tpls[i]
		}
		name, cols := widget.NewEntry(), widget.NewMultiLineEntry()
		name.SetText(t.Name)
		cols.SetText(strings.Join(t.Columns, "\n"))
		cols.SetMinRowsVisible(6)
		fd := dialog.NewForm(T("Шаблон"), T("Сохранить"), T("Отмена"), []*widget.FormItem{
			widget.NewFormItem(T("Название"), name), formItemHint(T("Колонки"), cols, T("по одной на строку; последняя считается «Готово»")),
		}, func(ok bool) {
			if !ok {
				return
			}
			t.Name, t.Columns = strings.TrimSpace(name.Text), nil
			for _, l := range strings.Split(cols.Text, "\n") {
				if l = strings.TrimSpace(l); l != "" {
					t.Columns = append(t.Columns, l)
				}
			}
			d.change(func() error { _, err := u.ss.api.SaveTemplate(t); return err })
		}, d.win)
		fd.Resize(fyne.NewSize(480, 380))
		fd.Show()
	}, func(i int) {
		id := tpls[i].ID
		d.change(func() error { return u.ss.api.DeleteTemplate(id) })
	})
}

func intEntry(v int) *widget.Entry {
	e := widget.NewEntry()
	e.SetText(strconv.Itoa(v))
	return e
}

func (u *ui) showSettings() {
	async(func() (*core.Settings, error) { return u.ss.api.Settings() }, func(s *core.Settings, err error) {
		if err != nil {
			u.handleErr(err)
			return
		}
		w := u.app.NewWindow(T("Настройки системы") + " — " + appName)
		edit, warn, maxMB := intEntry(s.CommentEditMinutes), intEntry(s.DeadlineWarnHours), intEntry(s.MaxAttachmentMB)
		en := widget.NewCheck(T("Отправлять уведомления по e-mail"), nil)
		en.SetChecked(s.EmailEnabled)
		host, port, user, pass, from := widget.NewEntry(), intEntry(s.SMTPPort), widget.NewEntry(), widget.NewPasswordEntry(), widget.NewEntry()
		host.SetText(s.SMTPHost)
		user.SetText(s.SMTPUser)
		pass.SetText(s.SMTPPassword)
		from.SetText(s.SMTPFrom)
		tls := widget.NewCheck(T("SSL/TLS сразу (обычно порт 465); иначе STARTTLS"), nil)
		tls.SetChecked(s.SMTPTLS)
		collect := func() (core.Settings, error) {
			ns := *s
			var errs []string
			num := func(e *widget.Entry, dst *int) {
				n, err := strconv.Atoi(strings.TrimSpace(e.Text))
				if err != nil || n < 0 {
					errs = append(errs, e.Text)
				}
				*dst = n
			}
			num(edit, &ns.CommentEditMinutes)
			num(warn, &ns.DeadlineWarnHours)
			num(maxMB, &ns.MaxAttachmentMB)
			num(port, &ns.SMTPPort)
			ns.EmailEnabled, ns.SMTPHost, ns.SMTPUser, ns.SMTPPassword, ns.SMTPFrom, ns.SMTPTLS =
				en.Checked, strings.TrimSpace(host.Text), strings.TrimSpace(user.Text), pass.Text, strings.TrimSpace(from.Text), tls.Checked
			if len(errs) > 0 {
				return ns, errors.New(T("Числовые поля должны содержать целые неотрицательные числа."))
			}
			return ns, nil
		}
		form := widget.NewForm(
			formItemHint(T("Правка комментария, мин"), edit, T("0 — без ограничения")),
			formItemHint(T("Предупреждать о сроке за, ч"), warn, ""),
			widget.NewFormItem(T("Макс. размер вложения, МБ"), maxMB),
			widget.NewFormItem("", en),
			widget.NewFormItem(T("SMTP-сервер"), host), widget.NewFormItem(T("Порт"), port),
			widget.NewFormItem(T("Пользователь"), user), widget.NewFormItem(T("Пароль"), pass),
			widget.NewFormItem(T("Адрес отправителя"), from), widget.NewFormItem("", tls),
		)
		test := widget.NewButtonWithIcon(T("Отправить тестовое письмо себе"), theme.MailSendIcon(), func() {
			ns, err := collect()
			if err != nil {
				showErr(err, w)
				return
			}
			async(func() (bool, error) { return true, u.ss.api.TestMail(ns) }, func(_ bool, err error) {
				if err != nil {
					showErr(err, w)
					return
				}
				dialog.ShowInformation(appName, T("Письмо отправлено."), w)
			})
		})
		save := widget.NewButtonWithIcon(T("Сохранить"), theme.DocumentSaveIcon(), func() {
			ns, err := collect()
			if err != nil {
				showErr(err, w)
				return
			}
			async(func() (bool, error) { return true, u.ss.api.SaveSettings(ns) }, func(_ bool, err error) {
				if err != nil {
					showErr(err, w)
					return
				}
				w.Close()
			})
		})
		save.Importance = widget.HighImportance
		w.SetContent(container.NewBorder(nil, container.NewPadded(container.NewHBox(test, save)), nil, nil, container.NewVScroll(container.NewPadded(form))))
		w.Resize(fyne.NewSize(640, 600))
		w.Show()
	})
}

// periodEntries returns from/to date entries (ДД.ММ.ГГГГ) and a parser yielding unix ms (to is exclusive).
func periodEntries(days int) (*widget.Entry, *widget.Entry, func() (int64, int64, error)) {
	from, to := widget.NewEntry(), widget.NewEntry()
	from.SetPlaceHolder(T("ДД.ММ.ГГГГ"))
	to.SetPlaceHolder(T("ДД.ММ.ГГГГ"))
	if days > 0 {
		from.SetText(time.Now().AddDate(0, 0, -days).Format("02.01.2006"))
	}
	return from, to, func() (int64, int64, error) {
		var f, t int64
		if s := strings.TrimSpace(from.Text); s != "" {
			d, err := time.ParseInLocation("02.01.2006", s, time.Local)
			if err != nil {
				return 0, 0, errors.New(T("Дата должна быть в формате ДД.ММ.ГГГГ."))
			}
			f = d.UnixMilli()
		}
		if s := strings.TrimSpace(to.Text); s != "" {
			d, err := time.ParseInLocation("02.01.2006", s, time.Local)
			if err != nil {
				return 0, 0, errors.New(T("Дата должна быть в формате ДД.ММ.ГГГГ."))
			}
			t = d.AddDate(0, 0, 1).UnixMilli()
		}
		return f, t, nil
	}
}

func (u *ui) userSelect() (*widget.Select, func() int64) {
	u.mu.Lock()
	users := append([]core.User(nil), u.users...)
	u.mu.Unlock()
	opts := []string{T("— все —")}
	for _, x := range users {
		opts = append(opts, userLabel(x))
	}
	s := widget.NewSelect(opts, nil)
	s.SetSelectedIndex(0)
	return s, func() int64 {
		if i := s.SelectedIndex(); i > 0 {
			return users[i-1].ID
		}
		return 0
	}
}

func (u *ui) showAudit() {
	w := u.app.NewWindow(T("Журнал аудита") + " — " + appName)
	from, to, period := periodEntries(7)
	usr, uid := u.userSelect()
	var rows []core.HistoryEntry
	list := widget.NewList(func() int { return len(rows) }, func() fyne.CanvasObject {
		l := widget.NewLabel("")
		l.Truncation = fyne.TextTruncateEllipsis
		return l
	}, func(i widget.ListItemID, o fyne.CanvasObject) {
		h := rows[i]
		s := fmtTime(h.Created) + "  " + u.userName(h.UserID) + ": " + historyText(h)
		if h.TaskID != 0 {
			s += "  [#" + strconv.FormatInt(h.TaskID, 10) + "]"
		}
		o.(*widget.Label).SetText(s)
	})
	list.OnSelected = func(i widget.ListItemID) {
		list.UnselectAll()
		if rows[i].TaskID != 0 {
			u.openTask(rows[i].TaskID)
		}
	}
	count := widget.NewLabel("")
	load := func() {
		f, t, err := period()
		if err != nil {
			showErr(err, w)
			return
		}
		af := core.AuditFilter{From: f, To: t, UserID: uid(), Limit: 2000}
		async(func() ([]core.HistoryEntry, error) { return u.ss.api.AuditLog(af) }, func(r []core.HistoryEntry, err error) {
			if err != nil {
				showErr(err, w)
				return
			}
			rows = r
			count.SetText(Tf("Записей: %d", len(r)))
			list.Refresh()
		})
	}
	exp := widget.NewButtonWithIcon(T("Экспорт CSV"), theme.DownloadIcon(), func() {
		data := [][]string{{T("Время"), T("Пользователь"), T("Объект"), T("Действие"), T("Задача")}}
		for _, h := range rows {
			task := ""
			if h.TaskID != 0 {
				task = "#" + strconv.FormatInt(h.TaskID, 10)
			}
			data = append(data, []string{fmtTime(h.Created), u.userName(h.UserID), h.Entity, historyText(h), task})
		}
		saveBytes(w, "audit-"+time.Now().Format("2006-01-02")+".csv", toCSV(data))
	})
	bar := container.NewHBox(widget.NewLabel(T("С")), container.NewGridWrap(fyne.NewSize(120, 36), from), widget.NewLabel(T("по")),
		container.NewGridWrap(fyne.NewSize(120, 36), to), container.NewGridWrap(fyne.NewSize(240, 36), usr),
		widget.NewButtonWithIcon(T("Показать"), theme.SearchIcon(), load), exp, count)
	w.SetContent(container.NewBorder(container.NewPadded(bar), nil, nil, nil, list))
	w.Resize(fyne.NewSize(1100, 640))
	w.Show()
	load()
}

func formItemHint(label string, w fyne.CanvasObject, hint string) *widget.FormItem {
	fi := widget.NewFormItem(label, w)
	fi.HintText = hint
	return fi
}
