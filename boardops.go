package main

import (
	"errors"
	"io"

	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/widget"

	"github.com/Serge-Nook/aegis/internal/core"
)

// deptSelect returns a select of departments ("" = none) and a getter of the chosen ID.
func (u *ui) deptSelect(cur int64) (*widget.Select, func() int64) {
	u.mu.Lock()
	depts := append([]core.Department(nil), u.depts...)
	u.mu.Unlock()
	opts := []string{T("— без отдела —")}
	for _, d := range depts {
		opts = append(opts, d.Name)
	}
	s := widget.NewSelect(opts, nil)
	s.SetSelectedIndex(0)
	for i, d := range depts {
		if d.ID == cur {
			s.SetSelectedIndex(i + 1)
		}
	}
	return s, func() int64 {
		if i := s.SelectedIndex(); i > 0 {
			return depts[i-1].ID
		}
		return 0
	}
}

func (u *ui) editBoard(b *core.Board) {
	isNew := b == nil
	if isNew {
		b = &core.Board{}
	}
	name, desc, proj := widget.NewEntry(), widget.NewMultiLineEntry(), widget.NewEntry()
	name.SetText(b.Name)
	desc.SetText(b.Description)
	desc.SetMinRowsVisible(3)
	proj.SetText(b.Project)
	dept, deptID := u.deptSelect(b.DeptID)
	items := []*widget.FormItem{widget.NewFormItem(T("Название"), name), widget.NewFormItem(T("Описание"), desc),
		widget.NewFormItem(T("Отдел"), dept), widget.NewFormItem(T("Проект"), proj)}
	var tplSel *widget.Select
	var tpls []core.Template
	if isNew {
		tpls, _ = u.ss.api.Templates()
		var names []string
		for _, t := range tpls {
			names = append(names, t.Name+": "+strings.Join(t.Columns, " → "))
		}
		tplSel = widget.NewSelect(names, nil)
		if len(names) > 0 {
			tplSel.SetSelectedIndex(0)
		}
		items = append(items, widget.NewFormItem(T("Шаблон колонок"), tplSel))
	}
	title := T("Свойства доски")
	if isNew {
		title = T("Новая доска")
	}
	d := dialog.NewForm(title, T("Сохранить"), T("Отмена"), items, func(ok bool) {
		if !ok {
			return
		}
		nb := *b
		nb.Name, nb.Description, nb.Project, nb.DeptID = strings.TrimSpace(name.Text), desc.Text, strings.TrimSpace(proj.Text), deptID()
		var tpl int64
		if tplSel != nil && tplSel.SelectedIndex() >= 0 {
			tpl = tpls[tplSel.SelectedIndex()].ID
		}
		async(func() (int64, error) { return u.ss.api.SaveBoard(nb, tpl) }, func(id int64, err error) {
			if err != nil {
				u.handleErr(err)
				return
			}
			u.boardID = id
			u.refreshAll()
		})
	}, u.win)
	d.Resize(fyne.NewSize(620, 420))
	d.Show()
}

func (u *ui) saveAsTemplate() {
	v := u.curView()
	if v == nil {
		return
	}
	e := widget.NewEntry()
	e.SetText(v.Board.Name)
	dialog.ShowForm(T("Сохранить колонки доски как шаблон"), T("Сохранить"), T("Отмена"), []*widget.FormItem{widget.NewFormItem(T("Название шаблона"), e)}, func(ok bool) {
		if ok {
			u.do(func() error { _, err := u.ss.api.SaveBoardAsTemplate(v.Board.ID, e.Text); return err })
		}
	}, u.win)
}

func (u *ui) toggleArchiveBoard() {
	v := u.curView()
	if v == nil {
		return
	}
	async(func() (bool, error) { return true, u.ss.api.ArchiveBoard(v.Board.ID, !v.Board.Archived) }, func(_ bool, err error) {
		if err != nil {
			u.handleErr(err)
		}
		u.refreshAll()
	})
}

func (u *ui) deleteBoard() {
	v := u.curView()
	if v == nil {
		return
	}
	dialog.ShowConfirm(T("Удалить доску"), Tf("Удалить доску «%s» вместе со всеми задачами? Это действие нельзя отменить — для временного скрытия используйте архив.", v.Board.Name), func(ok bool) {
		if !ok {
			return
		}
		async(func() (bool, error) { return true, u.ss.api.DeleteBoard(v.Board.ID) }, func(_ bool, err error) {
			if err != nil {
				u.handleErr(err)
			}
			u.boardID = 0
			u.refreshAll()
		})
	}, u.win)
}

func (u *ui) changePassword() {
	old, p1, p2 := widget.NewPasswordEntry(), widget.NewPasswordEntry(), widget.NewPasswordEntry()
	dialog.ShowForm(T("Смена пароля"), T("Сменить"), T("Отмена"), []*widget.FormItem{
		widget.NewFormItem(T("Текущий пароль"), old), widget.NewFormItem(T("Новый пароль"), p1), widget.NewFormItem(T("Повтор пароля"), p2),
	}, func(ok bool) {
		if !ok {
			return
		}
		if p1.Text != p2.Text {
			dialog.ShowError(errors.New(T("Пароли не совпадают.")), u.win)
			return
		}
		async(func() (bool, error) { return true, u.ss.api.ChangePassword(old.Text, p1.Text) }, func(_ bool, err error) {
			if err != nil {
				u.handleErr(err)
				return
			}
			dialog.ShowInformation(appName, T("Пароль изменён."), u.win)
		})
	}, u.win)
}

func (u *ui) backup() {
	d := dialog.NewFileSave(func(w fyne.URIWriteCloser, err error) {
		if err != nil || w == nil {
			return
		}
		async(func() ([]byte, error) { return u.ss.api.Backup() }, func(data []byte, err error) {
			defer w.Close()
			if err == nil {
				_, err = w.Write(data)
			}
			if err != nil {
				u.handleErr(err)
				return
			}
			dialog.ShowInformation(appName, T("Резервная копия сохранена.")+"\n"+w.URI().Path(), u.win)
		})
	}, u.win)
	d.SetFileName("egida-" + time.Now().Format("2006-01-02-1504") + ".egida-backup")
	d.Show()
}

func (u *ui) restore() {
	dialog.ShowConfirm(T("Восстановление"), T("Все текущие данные будут заменены данными из резервной копии. Продолжить?"), func(ok bool) {
		if !ok {
			return
		}
		dialog.ShowFileOpen(func(r fyne.URIReadCloser, err error) {
			if err != nil || r == nil {
				return
			}
			data, err := io.ReadAll(r)
			r.Close()
			if err != nil {
				showErr(err, u.win)
				return
			}
			async(func() (bool, error) { return true, u.ss.api.Restore(data) }, func(_ bool, err error) {
				if err != nil {
					u.handleErr(err)
					return
				}
				dialog.ShowInformation(appName, T("Данные восстановлены. Войдите заново."), u.win)
				u.logout()
			})
		}, u.win)
	}, u.win)
}

func saveBytes(w fyne.Window, name string, data []byte) {
	d := dialog.NewFileSave(func(wc fyne.URIWriteCloser, err error) {
		if err != nil || wc == nil {
			return
		}
		defer wc.Close()
		if _, err := wc.Write(data); err != nil {
			showErr(err, w)
		}
	}, w)
	d.SetFileName(name)
	d.Show()
}
