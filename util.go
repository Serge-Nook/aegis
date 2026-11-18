package main

import (
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"

	"github.com/Serge-Nook/aegis/internal/core"
)

func fmtTime(ms int64) string {
	if ms == 0 {
		return ""
	}
	return time.UnixMilli(ms).Format("02.01.2006 15:04")
}

func fmtDate(ms int64) string {
	if ms == 0 {
		return ""
	}
	return time.UnixMilli(ms).Format("02.01.2006")
}

func fmtSize(n int64) string {
	switch {
	case n >= 1<<20:
		return fmt.Sprintf("%.1f %s", float64(n)/(1<<20), T("МБ"))
	case n >= 1<<10:
		return fmt.Sprintf("%.0f %s", float64(n)/(1<<10), T("КБ"))
	}
	return fmt.Sprintf("%d %s", n, T("Б"))
}

func roleName(r core.Role) string {
	switch r {
	case core.RoleAdmin:
		return T("Администратор")
	case core.RoleHead:
		return T("Руководитель")
	case core.RoleExecutor:
		return T("Исполнитель")
	case core.RoleObserver:
		return T("Наблюдатель")
	}
	return string(r)
}

func priorityNames() []string {
	return []string{T("Низкий"), T("Средний"), T("Высокий"), T("Критический")}
}

func priorityName(p core.Priority) string {
	n := priorityNames()
	if p < 0 || int(p) >= len(n) {
		return ""
	}
	return n[p]
}

var errTexts = map[string]string{
	core.CodeForbidden:    "Недостаточно прав для этого действия.",
	core.CodeNotFound:     "Объект не найден — возможно, его удалил другой пользователь.",
	core.CodeAuth:         "Неверный логин или пароль, либо учётная запись отключена.",
	core.CodeTooLarge:     "Файл больше допустимого размера.",
	core.CodeEditExpired:  "Время на редактирование комментария истекло.",
	core.CodeColumnNotEmp: "В колонке есть задачи: выберите колонку, куда их перенести.",
	core.CodeOffline:      "Нет связи с сервером.",
}

var msgTexts = map[string]string{
	"login_taken":    "Такой логин уже занят.",
	"tag_taken":      "Такой тег уже есть.",
	"backup":         "Файл не является резервной копией «Эгиды».",
	"column":         "Колонка не найдена.",
	"login":          "Логин: латинские буквы, цифры, точка, дефис или подчёркивание.",
	"name":           "Укажите название.",
	"no_columns":     "Нужна хотя бы одна колонка.",
	"no_email":       "У вашей учётной записи не указан e-mail.",
	"password_short": "Пароль должен быть не короче 4 символов.",
	"priority":       "Неверный приоритет.",
	"role":           "Неверная роль.",
	"self_demote":    "Нельзя снять с себя роль администратора или отключить свою учётную запись.",
	"settings":       "Неверные значения настроек.",
	"template":       "Шаблон не найден.",
	"text":           "Введите текст.",
	"title":          "Укажите название задачи.",
}

func errText(err error) string {
	var ce *core.Error
	if errors.As(err, &ce) {
		if t, ok := msgTexts[ce.Msg]; ok {
			return T(t)
		}
		if t, ok := errTexts[ce.Code]; ok {
			return T(t)
		}
		if ce.Msg != "" {
			return ce.Msg
		}
	}
	return err.Error()
}

func showErr(err error, w fyne.Window) {
	if core.CodeOf(err) == core.CodeInternal {
		log.Printf("error: %v", err)
	}
	dialog.ShowError(errors.New(errText(err)), w)
}

func shorten(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n-1]) + "…"
}

// async runs fn in the background and then done (on the UI goroutine).
func async[T any](fn func() (T, error), done func(T, error)) {
	go func() {
		v, err := fn()
		fyne.Do(func() { done(v, err) })
	}()
}
