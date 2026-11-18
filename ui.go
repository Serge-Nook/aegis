package main

import (
	"log"
	"sort"
	"strconv"
	"strings"
	"sync"
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

const prefOffline = "offlinePolicy" // "readonly" or "wait"

type ui struct {
	app fyne.App
	win fyne.Window
	ss  *session

	mu       sync.Mutex
	users    []core.User
	userByID map[int64]core.User
	depts    []core.Department
	tags     []core.Tag
	tagByID  map[int64]core.Tag
	boards   []core.Board
	view     *core.BoardView

	showArchived bool
	boardID      int64
	filter       core.Filter
	rev          int64
	unread       int
	offline      bool
	pollStop     chan struct{}

	boardList *widget.List
	boardArea *fyne.Container
	banner    *fyne.Container
	bell      *widget.Button
	status    *widget.Label
	boardHdr  *widget.Label
	taskWins  map[int64]*taskWin
	waitDlg   dialog.Dialog
}

func newUI(a fyne.App) *ui {
	lang = a.Preferences().StringWithFallback(prefLang, "ru")
	applyTheme(a, isDark(a))
	u := &ui{app: a, win: a.NewWindow(appName), taskWins: map[int64]*taskWin{}, filter: core.AnyFilter()}
	u.win.Resize(fyne.NewSize(1280, 800))
	u.win.SetMaster()
	u.win.SetCloseIntercept(func() {
		if u.ss != nil && u.ss.mode == modeLocal && u.ss.ln != nil {
			dialog.ShowConfirm(appName, T("Этот компьютер — сервер «Эгиды». После закрытия программы другие пользователи потеряют доступ к данным. Закрыть?"),
				func(ok bool) {
					if ok {
						u.app.Quit()
					}
				}, u.win)
			return
		}
		u.app.Quit()
	})
	u.showConnect()
	return u
}

func (u *ui) shutdown() {
	u.stopPolling()
	u.ss.close()
}

func (u *ui) setLang(code string) {
	lang = code
	u.app.Preferences().SetString(prefLang, code)
	if u.ss == nil {
		u.showConnect()
		return
	}
	for _, tw := range u.taskWins {
		tw.win.Close()
	}
	u.buildMain()
	u.refreshAll()
}

func (u *ui) me() *core.User { return u.ss.me }

func (u *ui) isAdmin() bool { return u.me().Role == core.RoleAdmin }

func (u *ui) canManage() bool { return u.me().Role == core.RoleAdmin || u.me().Role == core.RoleHead }

// canWrite is false for observers and while the server is unreachable.
func (u *ui) canWrite() bool { return !u.offline && u.me().Role != core.RoleObserver }

func (u *ui) userName(id int64) string {
	if id == 0 {
		return ""
	}
	u.mu.Lock()
	defer u.mu.Unlock()
	if x, ok := u.userByID[id]; ok {
		return displayName(x)
	}
	return "#" + strconv.FormatInt(id, 10)
}

func displayName(x core.User) string {
	if x.FullName != "" {
		return x.FullName
	}
	return x.Login
}

func userLabel(x core.User) string { return displayName(x) + " (" + x.Login + ")" }

func (u *ui) deptName(id int64) string {
	u.mu.Lock()
	defer u.mu.Unlock()
	for _, d := range u.depts {
		if d.ID == id {
			return d.Name
		}
	}
	return ""
}

func (u *ui) startMain(s *session) {
	u.ss = s
	u.offline = false
	u.rev = -1
	u.boardID = int64(u.app.Preferences().Int("board"))
	u.filter = core.AnyFilter()
	log.Printf("login %s (%s), %s", s.me.Login, s.me.Role, s.where)
	u.buildMain()
	u.refreshAll()
	u.startPolling()
}

func (u *ui) buildMain() {
	u.win.SetTitle(appName + " — " + displayName(*u.me()))
	u.buildMenu()

	search := widget.NewEntry()
	search.SetPlaceHolder(T("Поиск: название, описание, #номер, тег"))
	search.SetText(u.filter.Query)
	search.OnChanged = func(s string) {
		u.filter.Query = s
		u.reloadBoard()
	}
	search.OnSubmitted = func(s string) {
		if n, err := strconv.ParseInt(strings.TrimPrefix(strings.TrimSpace(s), "#"), 10, 64); err == nil && strings.HasPrefix(strings.TrimSpace(s), "#") {
			u.openTask(n)
		}
	}
	quick := func(label string, ptr *bool) *widget.Check {
		c := widget.NewCheck(label, func(b bool) {
			*ptr = b
			u.reloadBoard()
		})
		c.Checked = *ptr
		return c
	}
	globalSearch := widget.NewButtonWithIcon(T("Везде"), theme.SearchIcon(), func() { u.showSearch(search.Text) })
	u.bell = widget.NewButtonWithIcon("0", theme.MailComposeIcon(), u.showNotifications)
	userBtn := widget.NewButtonWithIcon(displayName(*u.me()), theme.AccountIcon(), nil)
	userBtn.OnTapped = func() {
		m := fyne.NewMenu("",
			fyne.NewMenuItem(roleName(u.me().Role), nil),
			fyne.NewMenuItemSeparator(),
			fyne.NewMenuItem(T("Сменить пароль…"), u.changePassword),
			fyne.NewMenuItem(T("Выйти из учётной записи"), u.logout))
		m.Items[0].Disabled = true
		pos := fyne.CurrentApp().Driver().AbsolutePositionForObject(userBtn).Add(fyne.NewPos(0, userBtn.Size().Height))
		widget.ShowPopUpMenuAtPosition(m, u.win.Canvas(), pos)
	}
	top := container.NewBorder(nil, nil, nil, container.NewHBox(u.bell, userBtn),
		container.NewBorder(nil, nil, nil, container.NewHBox(
			quick(T("Мои"), &u.filter.Mine), quick(T("Просроченные"), &u.filter.Overdue),
			quick(T("Без исполнителя"), &u.filter.Unassigned), quick(T("Высокий приоритет"), &u.filter.HighPriority),
			globalSearch), search))

	u.boardList = widget.NewList(
		func() int { u.mu.Lock(); defer u.mu.Unlock(); return len(u.boards) },
		func() fyne.CanvasObject {
			return container.NewVBox(widget.NewLabelWithStyle("", fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), widget.NewLabel(""))
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			u.mu.Lock()
			if i >= len(u.boards) {
				u.mu.Unlock()
				return
			}
			b := u.boards[i]
			u.mu.Unlock()
			box := o.(*fyne.Container)
			name := box.Objects[0].(*widget.Label)
			name.SetText(b.Name)
			name.TextStyle = fyne.TextStyle{Bold: true, Italic: b.Archived}
			sub := strings.TrimSpace(strings.Join(nonEmpty(u.deptName(b.DeptID), b.Project), " · "))
			if b.Archived {
				sub = strings.TrimSpace(T("в архиве") + " " + sub)
			}
			box.Objects[1].(*widget.Label).SetText(sub)
		})
	u.boardList.OnSelected = func(i widget.ListItemID) {
		u.mu.Lock()
		if i >= len(u.boards) {
			u.mu.Unlock()
			return
		}
		id := u.boards[i].ID
		u.mu.Unlock()
		if id != u.boardID {
			u.boardID = id
			u.app.Preferences().SetInt("board", int(id))
			u.reloadBoard()
		}
	}
	newBoard := widget.NewButtonWithIcon(T("Доска"), theme.ContentAddIcon(), func() { u.editBoard(nil) })
	if !u.canManage() {
		newBoard.Disable()
	}
	arch := widget.NewCheck(T("Показать архив"), func(b bool) {
		u.showArchived = b
		u.refreshAll()
	})
	arch.Checked = u.showArchived
	left := container.NewBorder(
		container.NewBorder(nil, nil, widget.NewLabelWithStyle(T("Доски"), fyne.TextAlignLeading, fyne.TextStyle{Bold: true}), newBoard),
		arch, nil, nil, u.boardList)

	bannerText := canvas.NewText(T("Нет связи с сервером. Данные доступны только для просмотра, идёт переподключение…"), theme.Color(theme.ColorNameForegroundOnError))
	bannerText.TextStyle.Bold = true
	u.banner = container.NewStack(canvas.NewRectangle(theme.Color(theme.ColorNameError)), container.NewPadded(bannerText))
	u.banner.Hidden = !u.offline
	u.boardHdr = widget.NewLabelWithStyle("", fyne.TextAlignLeading, fyne.TextStyle{Bold: true})
	u.boardArea = container.NewStack(widget.NewLabel(T("Выберите доску слева или создайте новую.")))
	center := container.NewBorder(container.NewVBox(u.banner, u.boardHdr), nil, nil, nil, u.boardArea)
	split := container.NewHSplit(left, center)
	split.Offset = 0.18
	u.status = widget.NewLabel(u.ss.where)
	u.win.SetContent(container.NewBorder(container.NewPadded(top), u.status, nil, nil, split))
}

func nonEmpty(s ...string) []string {
	var out []string
	for _, x := range s {
		if x != "" {
			out = append(out, x)
		}
	}
	return out
}

func (u *ui) buildMenu() {
	file := fyne.NewMenu(T("Файл"),
		fyne.NewMenuItem(T("Сменить пароль…"), u.changePassword),
		fyne.NewMenuItem(T("Выйти из учётной записи"), u.logout))
	if u.isAdmin() {
		file.Items = append(file.Items, fyne.NewMenuItemSeparator(),
			fyne.NewMenuItem(T("Резервная копия…"), u.backup),
			fyne.NewMenuItem(T("Восстановить из копии…"), u.restore))
	}
	if items := u.appImageMenuItems(); len(items) > 0 {
		file.Items = append(file.Items, fyne.NewMenuItemSeparator())
		file.Items = append(file.Items, items...)
	}
	board := fyne.NewMenu(T("Доска"),
		fyne.NewMenuItem(T("Новая задача…"), func() { u.newTask(0) }),
		fyne.NewMenuItem(T("Обновить"), u.refreshAll))
	if u.canManage() {
		board.Items = append(board.Items, fyne.NewMenuItemSeparator(),
			fyne.NewMenuItem(T("Новая доска…"), func() { u.editBoard(nil) }),
			fyne.NewMenuItem(T("Свойства доски…"), func() {
				if v := u.curView(); v != nil {
					b := v.Board
					u.editBoard(&b)
				}
			}),
			fyne.NewMenuItem(T("Добавить колонку…"), func() { u.addColumn() }),
			fyne.NewMenuItem(T("Сохранить как шаблон…"), u.saveAsTemplate),
			fyne.NewMenuItem(T("Архивировать / вернуть из архива"), u.toggleArchiveBoard),
			fyne.NewMenuItem(T("Удалить доску…"), u.deleteBoard))
	}
	tasks := fyne.NewMenu(T("Задачи"),
		fyne.NewMenuItem(T("Поиск по всем доскам…"), func() { u.showSearch("") }),
		fyne.NewMenuItem(T("Уведомления…"), u.showNotifications))
	reports := fyne.NewMenu(T("Отчёты"), fyne.NewMenuItem(T("Аналитика…"), u.showReports))
	if u.canManage() {
		reports.Items = append(reports.Items, fyne.NewMenuItem(T("Журнал аудита…"), u.showAudit))
	}
	menus := []*fyne.Menu{file, board, tasks, reports}
	if u.canManage() {
		admin := fyne.NewMenu(T("Справочники"),
			fyne.NewMenuItem(T("Отделы…"), u.showDepartments),
			fyne.NewMenuItem(T("Теги…"), u.showTags),
			fyne.NewMenuItem(T("Шаблоны досок…"), u.showTemplates))
		if u.isAdmin() {
			admin.Items = append([]*fyne.MenuItem{fyne.NewMenuItem(T("Пользователи…"), u.showUsers)}, admin.Items...)
			admin.Items = append(admin.Items, fyne.NewMenuItemSeparator(), fyne.NewMenuItem(T("Настройки системы…"), u.showSettings))
		}
		menus = append(menus, admin)
	}
	langItems := []*fyne.MenuItem{}
	for _, l := range languages {
		l := l
		it := fyne.NewMenuItem(l.Name, func() { u.setLang(l.Code) })
		it.Checked = l.Code == lang
		langItems = append(langItems, it)
	}
	langMenu := fyne.NewMenuItem(T("Язык"), nil)
	langMenu.ChildMenu = fyne.NewMenu("", langItems...)
	dark := fyne.NewMenuItem(T("Тёмная тема"), nil)
	dark.Checked = isDark(u.app)
	dark.Action = func() {
		d := !isDark(u.app)
		u.app.Preferences().SetBool(prefDark, d)
		applyTheme(u.app, d)
		u.buildMenu()
		u.reloadBoard()
	}
	offRO := fyne.NewMenuItem(T("При потере связи: только просмотр"), nil)
	offWait := fyne.NewMenuItem(T("При потере связи: ждать подключения"), nil)
	pol := u.app.Preferences().StringWithFallback(prefOffline, "readonly")
	offRO.Checked, offWait.Checked = pol == "readonly", pol == "wait"
	offRO.Action = func() { u.app.Preferences().SetString(prefOffline, "readonly"); u.buildMenu() }
	offWait.Action = func() { u.app.Preferences().SetString(prefOffline, "wait"); u.buildMenu() }
	view := fyne.NewMenu(T("Вид"), dark, langMenu, fyne.NewMenuItemSeparator(), offRO, offWait)
	help := fyne.NewMenu(T("Справка"), fyne.NewMenuItem(T("О программе"), u.about))
	menus = append(menus, view, help)
	u.win.SetMainMenu(fyne.NewMainMenu(menus...))
}

func (u *ui) logout() {
	for _, tw := range u.taskWins {
		tw.win.Close()
	}
	u.showConnect()
}

func (u *ui) curView() *core.BoardView {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.view
}

// refreshAll reloads the dictionaries, the board list and the current board.
func (u *ui) refreshAll() {
	ss := u.ss
	if ss == nil {
		return
	}
	go func() {
		users, err1 := ss.api.Users()
		depts, err2 := ss.api.Departments()
		tags, err3 := ss.api.Tags()
		boards, err4 := ss.api.Boards(u.showArchived)
		for _, err := range []error{err1, err2, err3, err4} {
			if err != nil {
				fyne.Do(func() { u.handleErr(err) })
				return
			}
		}
		u.mu.Lock()
		u.users, u.depts, u.tags, u.boards = users, depts, tags, boards
		u.userByID = map[int64]core.User{}
		for _, x := range users {
			u.userByID[x.ID] = x
		}
		u.tagByID = map[int64]core.Tag{}
		for _, t := range tags {
			u.tagByID[t.ID] = t
		}
		found := false
		for _, b := range boards {
			found = found || b.ID == u.boardID
		}
		if !found {
			u.boardID = 0
			if len(boards) > 0 {
				u.boardID = boards[0].ID
			}
		}
		u.mu.Unlock()
		fyne.Do(func() {
			if u.ss != ss {
				return
			}
			u.boardList.Refresh()
			u.mu.Lock()
			sel := -1
			for i, b := range u.boards {
				if b.ID == u.boardID {
					sel = i
				}
			}
			u.mu.Unlock()
			if sel >= 0 {
				u.boardList.Select(sel)
			}
			u.reloadBoard()
		})
	}()
}

func (u *ui) reloadBoard() {
	ss, id, f := u.ss, u.boardID, u.filter
	if ss == nil || u.boardArea == nil {
		return
	}
	if id == 0 {
		u.mu.Lock()
		u.view = nil
		u.mu.Unlock()
		u.boardHdr.SetText("")
		u.boardArea.Objects = []fyne.CanvasObject{container.NewCenter(widget.NewLabel(T("Выберите доску слева или создайте новую.")))}
		u.boardArea.Refresh()
		return
	}
	async(func() (*core.BoardView, error) { return ss.api.BoardView(id, f) }, func(v *core.BoardView, err error) {
		if u.ss != ss || u.boardID != id {
			return
		}
		if err != nil {
			u.handleErr(err)
			return
		}
		u.mu.Lock()
		u.view = v
		u.mu.Unlock()
		hdr := v.Board.Name
		if d := u.deptName(v.Board.DeptID); d != "" {
			hdr += " · " + d
		}
		if v.Board.Project != "" {
			hdr += " · " + v.Board.Project
		}
		if v.Board.Archived {
			hdr += " · " + T("в архиве")
		}
		if v.Board.Description != "" {
			hdr += "\n" + v.Board.Description
		}
		u.boardHdr.SetText(hdr)
		u.boardArea.Objects = []fyne.CanvasObject{newBoardWidget(u, v)}
		u.boardArea.Refresh()
	})
}

// handleErr switches to offline mode on network errors and shows other errors.
func (u *ui) handleErr(err error) {
	if core.CodeOf(err) == core.CodeOffline || isNetErr(err) {
		u.setOffline(true)
		return
	}
	showErr(err, u.win)
}

func isNetErr(err error) bool {
	s := err.Error()
	return strings.Contains(s, "connection refused") || strings.Contains(s, "broken pipe") || strings.Contains(s, "i/o timeout") ||
		strings.Contains(s, "connection reset") || strings.Contains(s, "no route to host") || strings.Contains(s, "network is unreachable") ||
		strings.Contains(s, "conn closed") || strings.Contains(s, "failed to connect")
}

func (u *ui) setOffline(off bool) {
	if u.offline == off || u.ss == nil {
		return
	}
	u.offline = off
	log.Printf("offline=%v", off)
	if u.banner != nil {
		u.banner.Hidden = !off
		u.banner.Refresh()
	}
	if off && u.app.Preferences().StringWithFallback(prefOffline, "readonly") == "wait" {
		pb := widget.NewProgressBarInfinite()
		u.waitDlg = dialog.NewCustomWithoutButtons(T("Нет связи с сервером"), container.NewVBox(
			widget.NewLabel(T("Ожидание подключения… Изменения станут доступны, как только связь восстановится.")), pb,
			widget.NewButton(T("Отключиться"), func() { u.waitDlg.Hide(); u.waitDlg = nil; u.logout() })), u.win)
		u.waitDlg.Show()
	}
	if !off {
		if u.waitDlg != nil {
			u.waitDlg.Hide()
			u.waitDlg = nil
		}
		u.rev = -1
	}
	u.reloadBoard()
	for _, tw := range u.taskWins {
		tw.reload()
	}
}

func (u *ui) stopPolling() {
	if u.pollStop != nil {
		close(u.pollStop)
		u.pollStop = nil
	}
}

// startPolling checks the data revision every 2 s and reloads what changed.
func (u *ui) startPolling() {
	u.stopPolling()
	stop := make(chan struct{})
	u.pollStop = stop
	ss := u.ss
	go func() {
		tick := time.NewTicker(2 * time.Second)
		defer tick.Stop()
		lastDeadline := time.Time{}
		for {
			select {
			case <-stop:
				return
			case <-tick.C:
			}
			if ss.svc != nil && ss.ln == nil && time.Since(lastDeadline) > 5*time.Minute {
				lastDeadline = time.Now()
				if err := ss.svc.CheckDeadlines(); err != nil {
					log.Printf("deadlines: %v", err)
				}
			}
			p, err := ss.api.Poll()
			fyne.Do(func() {
				if u.ss != ss {
					return
				}
				if err != nil {
					if core.CodeOf(err) == core.CodeOffline || isNetErr(err) {
						u.setOffline(true)
					} else {
						log.Printf("poll: %v", err)
					}
					return
				}
				u.setOffline(false)
				if p.Unread != u.unread {
					if p.Unread > u.unread && u.rev >= 0 {
						u.popNotification()
					}
					u.unread = p.Unread
					u.bell.SetText(strconv.Itoa(p.Unread))
					if p.Unread > 0 {
						u.bell.Importance = widget.WarningImportance
					} else {
						u.bell.Importance = widget.MediumImportance
					}
					u.bell.Refresh()
				}
				if p.Rev != u.rev {
					first := u.rev < 0
					u.rev = p.Rev
					if !first {
						u.refreshAll()
						for _, tw := range u.taskWins {
							tw.reload()
						}
					}
				}
			})
		}
	}()
}

func (u *ui) popNotification() {
	ss := u.ss
	async(func() ([]core.Notification, error) { return ss.api.Notifications(true, 1) }, func(ns []core.Notification, err error) {
		if err == nil && len(ns) > 0 {
			u.app.SendNotification(fyne.NewNotification(appName, u.notifText(ns[0])))
		}
	})
}

func (u *ui) notifText(n core.Notification) string {
	title, extra, _ := strings.Cut(n.Text, "\x1f")
	who := u.userName(n.ActorID)
	task := "#" + strconv.FormatInt(n.TaskID, 10) + " «" + title + "»"
	switch n.Type {
	case core.NotifyAssigned:
		return Tf("%s назначил(а) вам задачу %s", who, task)
	case core.NotifyComment:
		return Tf("%s прокомментировал(а) задачу %s", who, task)
	case core.NotifyMention:
		return Tf("%s упомянул(а) вас в задаче %s", who, task)
	case core.NotifyDeadline:
		return Tf("Приближается срок задачи %s", task)
	case core.NotifyMoved:
		return Tf("%s переместил(а) задачу %s на этап «%s»", who, task, extra)
	}
	return task
}

func (u *ui) showNotifications() {
	ss := u.ss
	w := u.app.NewWindow(T("Уведомления"))
	var items []core.Notification
	list := widget.NewList(func() int { return len(items) },
		func() fyne.CanvasObject {
			l := widget.NewLabel("")
			l.Wrapping = fyne.TextWrapWord
			return container.NewBorder(nil, nil, nil, widget.NewLabel(""), l)
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			n := items[i]
			c := o.(*fyne.Container)
			l := c.Objects[0].(*widget.Label)
			l.SetText(u.notifText(n))
			l.TextStyle.Bold = !n.Read
			l.Refresh()
			c.Objects[1].(*widget.Label).SetText(fmtTime(n.CreatedAt))
		})
	load := func() {
		async(func() ([]core.Notification, error) { return ss.api.Notifications(false, 200) }, func(ns []core.Notification, err error) {
			if err != nil {
				u.handleErr(err)
				return
			}
			items = ns
			list.Refresh()
		})
	}
	list.OnSelected = func(i widget.ListItemID) {
		n := items[i]
		list.UnselectAll()
		go ss.api.MarkRead([]int64{n.ID})
		items[i].Read = true
		list.RefreshItem(i)
		u.openTask(n.TaskID)
	}
	readAll := widget.NewButtonWithIcon(T("Отметить все прочитанными"), theme.ConfirmIcon(), func() {
		async(func() (bool, error) { return true, ss.api.MarkRead(nil) }, func(_ bool, err error) {
			if err != nil {
				u.handleErr(err)
			}
			load()
		})
	})
	w.SetContent(container.NewBorder(nil, container.NewHBox(layout.NewSpacer(), readAll, widget.NewButton(T("Закрыть"), w.Close)), nil, nil, list))
	w.Resize(fyne.NewSize(640, 560))
	load()
	w.Show()
}

func (u *ui) showSearch(q string) {
	ss := u.ss
	w := u.app.NewWindow(T("Поиск по всем доскам"))
	entry := widget.NewEntry()
	entry.SetPlaceHolder(T("Название, описание, #номер или тег"))
	entry.SetText(q)
	f := core.AnyFilter()
	var res []core.TaskCard
	boardName := map[int64]string{}
	u.mu.Lock()
	for _, b := range u.boards {
		boardName[b.ID] = b.Name
	}
	u.mu.Unlock()
	list := widget.NewList(func() int { return len(res) },
		func() fyne.CanvasObject {
			return container.NewBorder(nil, nil, nil, widget.NewLabel(""), widget.NewLabel(""))
		},
		func(i widget.ListItemID, o fyne.CanvasObject) {
			t := res[i]
			c := o.(*fyne.Container)
			c.Objects[0].(*widget.Label).SetText("#" + strconv.FormatInt(t.ID, 10) + "  " + t.Title)
			c.Objects[1].(*widget.Label).SetText(strings.Join(nonEmpty(boardName[t.BoardID], u.userName(t.ResponsibleID), priorityName(t.Priority)), " · "))
		})
	list.OnSelected = func(i widget.ListItemID) { list.UnselectAll(); u.openTask(res[i].ID) }
	count := widget.NewLabel("")
	run := func() {
		f.Query = entry.Text
		ff := f
		async(func() ([]core.TaskCard, error) { return ss.api.Search(ff) }, func(r []core.TaskCard, err error) {
			if err != nil {
				u.handleErr(err)
				return
			}
			sort.Slice(r, func(i, j int) bool { return r[i].ID > r[j].ID })
			res = r
			count.SetText(Tf("Найдено: %d", len(r)))
			list.Refresh()
		})
	}
	entry.OnChanged = func(string) { run() }
	chk := func(label string, p *bool) *widget.Check {
		return widget.NewCheck(label, func(b bool) { *p = b; run() })
	}
	top := container.NewVBox(entry, container.NewHBox(chk(T("Мои"), &f.Mine), chk(T("Просроченные"), &f.Overdue),
		chk(T("Без исполнителя"), &f.Unassigned), chk(T("Высокий приоритет"), &f.HighPriority), chk(T("Включая архив"), &f.IncludeArchived)))
	w.SetContent(container.NewBorder(top, count, nil, nil, list))
	w.Resize(fyne.NewSize(760, 600))
	w.Show()
	w.Canvas().Focus(entry)
	run()
}

func (u *ui) about() {
	site := widget.NewHyperlink("nookbat.ru", nil)
	site.SetURLFromString(appSite)
	d := dialog.NewCustom(T("О программе"), T("Закрыть"), container.NewVBox(
		container.NewCenter(container.NewGridWrap(fyne.NewSize(96, 96), canvas.NewImageFromResource(appIcon))),
		widget.NewLabelWithStyle(appName+" "+appVersion, fyne.TextAlignCenter, fyne.TextStyle{Bold: true}),
		widget.NewLabelWithStyle(T("Система управления задачами и этапами работы отделов"), fyne.TextAlignCenter, fyne.TextStyle{}),
		widget.NewLabelWithStyle(T("Автор")+": "+appAuthor, fyne.TextAlignCenter, fyne.TextStyle{}),
		container.NewCenter(container.NewHBox(widget.NewLabel(T("Сайт автора")+":"), site)),
	), u.win)
	d.Show()
}
