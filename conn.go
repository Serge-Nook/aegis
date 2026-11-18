package main

import (
	"errors"
	"fmt"
	"log"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/layout"
	"fyne.io/fyne/v2/widget"

	"github.com/Serge-Nook/aegis/internal/core"
	"github.com/Serge-Nook/aegis/internal/remote"
)

const (
	modeLocal  = "local"
	modeClient = "client"
	modePG     = "pg"
)

// session is an open connection: core.API plus whatever must be closed.
type session struct {
	api    core.API
	mode   string
	svc    *core.Service
	client *remote.Client
	ln     net.Listener
	me     *core.User
	where  string // shown in the status bar
}

func (s *session) close() {
	if s == nil {
		return
	}
	if s.client != nil {
		s.client.Logout()
	}
	if s.ln != nil {
		s.ln.Close()
	}
	if s.svc != nil {
		s.svc.Close()
	}
}

type connParams struct {
	mode                         string
	dbPath, port, name           string
	addr                         string
	pgHost, pgPort, pgDB, pgUser string
	pgPass, pgSSL                string
	login, password              string
}

// open connects without logging in and reports whether the first administrator must be created.
func (p connParams) open() (*session, bool, error) {
	s := &session{mode: p.mode}
	switch p.mode {
	case modeLocal:
		if err := os.MkdirAll(filepath.Dir(p.dbPath), 0o755); err != nil {
			return nil, false, err
		}
		svc, err := core.Open(core.KindSQLite, p.dbPath)
		if err != nil {
			return nil, false, err
		}
		s.svc = svc
		s.where = T("Локальный сервер") + " · " + p.dbPath
		port, _ := strconv.Atoi(p.port)
		if port <= 0 {
			port = remote.DefaultPort
		}
		ln, err := net.Listen("tcp", ":"+strconv.Itoa(port))
		if err != nil {
			log.Printf("listen :%d: %v", port, err)
			s.where += " · " + Tf("порт %d занят, другие рабочие места не смогут подключиться", port)
		} else {
			s.ln = ln
			s.where += " · " + Tf("доступен по сети на порту %d", port)
			name := p.name
			if name == "" {
				name, _ = os.Hostname()
			}
			srv := remote.NewServer(svc, name, appVersion)
			go func() {
				if err := srv.Serve(ln); err != nil && !errors.Is(err, net.ErrClosed) {
					log.Printf("server: %v", err)
				}
			}()
		}
		need, err := svc.NeedsSetup()
		return s, need, err
	case modeClient:
		c := remote.NewClient(p.addr)
		info, err := c.Info()
		if err != nil {
			return nil, false, err
		}
		s.client = c
		s.where = Tf("Сервер «%s»", info.Name) + " · " + c.Addr()
		return s, info.NeedsSetup, nil
	case modePG:
		host := p.pgHost
		if host == "" {
			host = "localhost"
		}
		port := p.pgPort
		if port == "" {
			port = "5432"
		}
		u := url.URL{Scheme: "postgres", User: url.UserPassword(p.pgUser, p.pgPass), Host: net.JoinHostPort(host, port), Path: "/" + p.pgDB,
			RawQuery: "sslmode=" + p.pgSSL + "&connect_timeout=10"}
		svc, err := core.Open(core.KindPostgres, u.String())
		if err != nil {
			return nil, false, err
		}
		s.svc = svc
		s.where = "PostgreSQL · " + p.pgUser + "@" + net.JoinHostPort(host, port) + "/" + p.pgDB
		need, err := svc.NeedsSetup()
		return s, need, err
	}
	return nil, false, fmt.Errorf("mode %q", p.mode)
}

func (s *session) setup(login, fullName, password string) error {
	if s.client != nil {
		return s.client.Setup(login, fullName, password)
	}
	return s.svc.CreateFirstAdmin(login, fullName, password)
}

func (s *session) login(login, password string) error {
	if s.client != nil {
		u, err := s.client.Login(login, password)
		if err != nil {
			return err
		}
		s.api, s.me = s.client, u
		return nil
	}
	ss, err := s.svc.Login(login, password)
	if err != nil {
		return err
	}
	u, err := ss.Me()
	if err != nil {
		return err
	}
	s.api, s.me = ss, u
	return nil
}

func (u *ui) showConnect() {
	u.stopPolling()
	u.ss.close()
	u.ss = nil
	u.win.SetMainMenu(nil)
	u.win.SetTitle(appName)
	p := u.app.Preferences()

	modes := []string{T("Этот компьютер — сервер (локальный режим)"), T("Подключиться к серверу «Эгиды»"), T("База данных PostgreSQL")}
	modeKeys := []string{modeLocal, modeClient, modePG}

	dbPath := widget.NewEntry()
	dbPath.SetText(p.StringWithFallback("dbPath", filepath.Join(dataDir(), "egida.db")))
	dbBrowse := widget.NewButton(T("Обзор…"), func() {
		d := dialog.NewFileSave(func(w fyne.URIWriteCloser, err error) {
			if err == nil && w != nil {
				path := w.URI().Path()
				w.Close()
				if st, err := os.Stat(path); err == nil && st.Size() == 0 {
					os.Remove(path)
				}
				dbPath.SetText(path)
			}
		}, u.win)
		d.SetFileName("egida.db")
		d.Show()
	})
	port := widget.NewEntry()
	port.SetText(p.StringWithFallback("port", strconv.Itoa(remote.DefaultPort)))
	host, _ := os.Hostname()
	srvName := widget.NewEntry()
	srvName.SetText(p.StringWithFallback("serverName", host))
	localForm := widget.NewForm(
		widget.NewFormItem(T("Файл базы данных"), container.NewBorder(nil, nil, nil, dbBrowse, dbPath)),
		widget.NewFormItem(T("Порт для других рабочих мест"), port),
		widget.NewFormItem(T("Имя сервера в сети"), srvName),
	)
	localHint := widget.NewLabel(T("Данные хранятся на этом компьютере. Пока программа открыта, коллеги подключаются к нему по IP-адресу или через «Найти в сети»."))
	localHint.Wrapping = fyne.TextWrapWord

	addr := widget.NewEntry()
	addr.SetPlaceHolder("192.168.1.10:8080")
	addr.SetText(p.String("serverAddr"))
	found := widget.NewSelect(nil, nil)
	found.PlaceHolder = T("Серверы в сети")
	var foundList []remote.Found
	found.OnChanged = func(s string) {
		for _, f := range foundList {
			if f.Name+" ("+f.Addr+")" == s {
				addr.SetText(f.Addr)
			}
		}
	}
	findBtn := widget.NewButton(T("Найти в сети"), nil)
	findBtn.OnTapped = func() {
		findBtn.Disable()
		async(func() ([]remote.Found, error) { return remote.Discover(1500 * time.Millisecond) }, func(fl []remote.Found, err error) {
			findBtn.Enable()
			if err != nil {
				showErr(err, u.win)
				return
			}
			foundList = fl
			var opts []string
			for _, f := range fl {
				opts = append(opts, f.Name+" ("+f.Addr+")")
			}
			found.SetOptions(opts)
			if len(fl) == 0 {
				dialog.ShowInformation(appName, T("Серверы «Эгиды» в локальной сети не найдены. Введите адрес вручную."), u.win)
			} else if len(fl) == 1 {
				found.SetSelected(opts[0])
			}
		})
	}
	clientForm := widget.NewForm(
		widget.NewFormItem(T("Адрес сервера"), addr),
		widget.NewFormItem("", container.NewBorder(nil, nil, nil, findBtn, found)),
	)

	pgHost, pgPort, pgDB, pgUser := widget.NewEntry(), widget.NewEntry(), widget.NewEntry(), widget.NewEntry()
	pgPass := widget.NewPasswordEntry()
	pgHost.SetText(p.StringWithFallback("pgHost", "localhost"))
	pgPort.SetText(p.StringWithFallback("pgPort", "5432"))
	pgDB.SetText(p.StringWithFallback("pgDB", "egida"))
	pgUser.SetText(p.StringWithFallback("pgUser", "egida"))
	pgSSL := widget.NewSelect([]string{"disable", "prefer", "require", "verify-full"}, nil)
	pgSSL.SetSelected(p.StringWithFallback("pgSSL", "prefer"))
	pgForm := widget.NewForm(
		widget.NewFormItem(T("Сервер"), pgHost),
		widget.NewFormItem(T("Порт"), pgPort),
		widget.NewFormItem(T("База данных"), pgDB),
		widget.NewFormItem(T("Пользователь БД"), pgUser),
		widget.NewFormItem(T("Пароль БД"), pgPass),
		widget.NewFormItem("SSL", pgSSL),
	)

	forms := []fyne.CanvasObject{container.NewVBox(localHint, localForm), clientForm, pgForm}
	stack := container.NewStack(forms...)
	mode := widget.NewRadioGroup(modes, func(s string) {
		for i, m := range modes {
			if m == s {
				forms[i].Show()
			} else {
				forms[i].Hide()
			}
		}
		stack.Refresh()
	})
	cur := p.StringWithFallback("mode", modeLocal)
	for i, k := range modeKeys {
		if k == cur {
			mode.SetSelected(modes[i])
		}
	}
	if mode.Selected == "" {
		mode.SetSelected(modes[0])
	}

	login := widget.NewEntry()
	login.SetText(p.String("login"))
	password := widget.NewPasswordEntry()
	status := widget.NewLabel("")
	status.Wrapping = fyne.TextWrapWord
	var connectBtn *widget.Button
	connect := func() {
		var cp connParams
		for i, m := range modes {
			if m == mode.Selected {
				cp.mode = modeKeys[i]
			}
		}
		cp.dbPath, cp.port, cp.name = strings.TrimSpace(dbPath.Text), strings.TrimSpace(port.Text), strings.TrimSpace(srvName.Text)
		cp.addr = strings.TrimSpace(addr.Text)
		cp.pgHost, cp.pgPort, cp.pgDB, cp.pgUser, cp.pgPass, cp.pgSSL = strings.TrimSpace(pgHost.Text), strings.TrimSpace(pgPort.Text), strings.TrimSpace(pgDB.Text), strings.TrimSpace(pgUser.Text), pgPass.Text, pgSSL.Selected
		cp.login, cp.password = strings.TrimSpace(login.Text), password.Text
		if cp.mode == modeClient && cp.addr == "" {
			status.SetText(T("Укажите адрес сервера."))
			return
		}
		if cp.login == "" {
			status.SetText(T("Введите логин и пароль."))
			return
		}
		for k, v := range map[string]string{"mode": cp.mode, "dbPath": cp.dbPath, "port": cp.port, "serverName": cp.name, "serverAddr": cp.addr,
			"pgHost": cp.pgHost, "pgPort": cp.pgPort, "pgDB": cp.pgDB, "pgUser": cp.pgUser, "pgSSL": cp.pgSSL, "login": cp.login} {
			p.SetString(k, v)
		}
		connectBtn.Disable()
		status.SetText(T("Подключение…"))
		type res struct {
			s    *session
			need bool
		}
		async(func() (res, error) {
			s, need, err := cp.open()
			if err == nil && !need {
				if err = s.login(cp.login, cp.password); err != nil {
					s.close()
				}
			}
			return res{s, need}, err
		}, func(r res, err error) {
			connectBtn.Enable()
			if err != nil {
				status.SetText(errText(err))
				return
			}
			status.SetText("")
			if r.need {
				u.firstAdmin(r.s, cp.login, cp.password)
				return
			}
			u.startMain(r.s)
		})
	}
	connectBtn = widget.NewButtonWithIcon(T("Войти"), nil, connect)
	connectBtn.Importance = widget.HighImportance
	password.OnSubmitted = func(string) { connect() }
	loginForm := widget.NewForm(widget.NewFormItem(T("Логин"), login), widget.NewFormItem(T("Пароль"), password))

	title := widget.NewLabelWithStyle(appName, fyne.TextAlignCenter, fyne.TextStyle{Bold: true})
	sub := widget.NewLabelWithStyle(T("Управление задачами и этапами работы отделов"), fyne.TextAlignCenter, fyne.TextStyle{Italic: true})
	card := widget.NewCard(T("Подключение"), "", container.NewVBox(
		mode, widget.NewSeparator(), stack, widget.NewSeparator(), loginForm, connectBtn, status))
	content := container.NewVBox(u.langThemeBar(), title, sub, card)
	u.win.SetContent(container.NewCenter(container.NewGridWrap(fyne.NewSize(620, 640), container.NewVScroll(content))))
	u.win.Canvas().Focus(password)
}

func (u *ui) firstAdmin(s *session, login, password string) {
	name := widget.NewEntry()
	name.SetText(T("Администратор"))
	l := widget.NewEntry()
	l.SetText(login)
	p1, p2 := widget.NewPasswordEntry(), widget.NewPasswordEntry()
	p1.SetText(password)
	items := []*widget.FormItem{
		widget.NewFormItem(T("Логин"), l),
		widget.NewFormItem(T("Полное имя"), name),
		widget.NewFormItem(T("Пароль"), p1),
		widget.NewFormItem(T("Повтор пароля"), p2),
	}
	d := dialog.NewForm(T("Первый запуск: создание администратора"), T("Создать"), T("Отмена"), items, func(ok bool) {
		if !ok {
			s.close()
			return
		}
		if p1.Text != p2.Text {
			dialog.ShowError(errors.New(T("Пароли не совпадают.")), u.win)
			u.firstAdmin(s, l.Text, "")
			return
		}
		async(func() (bool, error) {
			if err := s.setup(strings.TrimSpace(l.Text), strings.TrimSpace(name.Text), p1.Text); err != nil {
				return false, err
			}
			return true, s.login(strings.TrimSpace(l.Text), p1.Text)
		}, func(_ bool, err error) {
			if err != nil {
				showErr(err, u.win)
				u.firstAdmin(s, l.Text, "")
				return
			}
			u.app.Preferences().SetString("login", strings.TrimSpace(l.Text))
			u.startMain(s)
		})
	}, u.win)
	d.Resize(fyne.NewSize(480, 320))
	d.Show()
}

func (u *ui) langThemeBar() fyne.CanvasObject {
	var names []string
	for _, l := range languages {
		names = append(names, l.Name)
	}
	sel := widget.NewSelect(names, nil)
	for _, l := range languages {
		if l.Code == lang {
			sel.SetSelected(l.Name)
		}
	}
	sel.OnChanged = func(s string) {
		for _, l := range languages {
			if l.Name == s && l.Code != lang {
				u.setLang(l.Code)
			}
		}
	}
	dark := widget.NewCheck(T("Тёмная тема"), func(b bool) {
		u.app.Preferences().SetBool(prefDark, b)
		applyTheme(u.app, b)
	})
	dark.SetChecked(isDark(u.app))
	return container.NewHBox(layout.NewSpacer(), dark, sel)
}
