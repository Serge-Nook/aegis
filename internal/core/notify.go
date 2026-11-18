package core

import (
	"crypto/tls"
	"database/sql"
	"fmt"
	"log"
	"mime"
	"net"
	"net/smtp"
	"strconv"
	"strings"
	"time"
)

type mailMsg struct {
	to      string
	subject string
	body    string
}

// outbox collects e-mails during a transaction; they are sent after commit.
type outbox struct{ mails []mailMsg }

var notifyText = map[string]string{
	NotifyAssigned: "Вам назначена задача",
	NotifyComment:  "Новый комментарий к задаче",
	NotifyMention:  "Вас упомянули в задаче",
	NotifyDeadline: "Приближается срок задачи",
	NotifyMoved:    "Задача перемещена на этап",
}

// notify creates in-app notifications for users (except the actor) and queues e-mails.
func notify(d dbx, ob *outbox, actorID int64, typ string, taskID int64, text string, users []int64) error {
	now := nowMs()
	var boardID int64
	if err := d.row("SELECT board_id FROM tasks WHERE id = ?", taskID).Scan(&boardID); err != nil {
		return notFound(err)
	}
	b, err := loadBoard(d, boardID)
	if err != nil {
		return err
	}
	for _, uid := range uniq(users) {
		if uid == actorID {
			continue
		}
		// Only users who may open the task learn about it (e.g. an outsider
		// @mentioned on a department board gets nothing).
		a, err := (&Session{uid: uid}).me(d)
		if CodeOf(err) == CodeAuth {
			continue
		}
		if err != nil {
			return err
		}
		if !a.canSeeBoard(b) {
			continue
		}
		email := a.Email
		if _, err := d.exec("INSERT INTO notifications (user_id, type, actor_id, task_id, text, is_read, created_at) VALUES (?, ?, ?, ?, ?, 0, ?)",
			uid, typ, actorID, taskID, text, now); err != nil {
			return err
		}
		if email != "" && ob != nil {
			title, extra, _ := strings.Cut(text, "\x1f")
			body := fmt.Sprintf("%s #%d «%s»", notifyText[typ], taskID, title)
			if extra != "" {
				body += " → «" + extra + "»"
			}
			if actorID != 0 {
				var name string
				d.row("SELECT full_name FROM users WHERE id = ?", actorID).Scan(&name)
				body += "\nАвтор изменения: " + name
			}
			ob.mails = append(ob.mails, mailMsg{to: email, subject: "Эгида: " + notifyText[typ] + " #" + strconv.FormatInt(taskID, 10), body: body})
		}
	}
	return nil
}

func (s *Service) flush(ob *outbox) {
	if ob == nil || len(ob.mails) == 0 {
		return
	}
	st, err := loadSettings(s.dbx())
	if err != nil || !st.EmailEnabled || st.SMTPHost == "" {
		return
	}
	mails := ob.mails
	go func() {
		for _, m := range mails {
			if err := s.mail(st, []string{m.to}, m.subject, m.body+"\n\n— «Эгида»"); err != nil {
				log.Printf("e-mail to %s: %v", m.to, err)
			}
		}
	}()
}

func sendMail(st Settings, to []string, subject, body string) error {
	addr := net.JoinHostPort(st.SMTPHost, strconv.Itoa(st.SMTPPort))
	from := st.SMTPFrom
	if from == "" {
		from = st.SMTPUser
	}
	msg := "From: " + from + "\r\nTo: " + strings.Join(to, ", ") + "\r\nSubject: " + mime.QEncoding.Encode("utf-8", subject) +
		"\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\nContent-Transfer-Encoding: 8bit\r\nDate: " +
		time.Now().Format(time.RFC1123Z) + "\r\n\r\n" + strings.ReplaceAll(body, "\n", "\r\n")
	var conn net.Conn
	var err error
	dialer := &net.Dialer{Timeout: 15 * time.Second}
	if st.SMTPTLS {
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{ServerName: st.SMTPHost})
	} else {
		conn, err = dialer.Dial("tcp", addr)
	}
	if err != nil {
		return err
	}
	c, err := smtp.NewClient(conn, st.SMTPHost)
	if err != nil {
		conn.Close()
		return err
	}
	defer c.Close()
	if ok, _ := c.Extension("STARTTLS"); ok && !st.SMTPTLS {
		if err := c.StartTLS(&tls.Config{ServerName: st.SMTPHost}); err != nil {
			return err
		}
	}
	if st.SMTPUser != "" {
		if err := c.Auth(smtp.PlainAuth("", st.SMTPUser, st.SMTPPassword, st.SMTPHost)); err != nil {
			return err
		}
	}
	if err := c.Mail(addrOnly(from)); err != nil {
		return err
	}
	for _, t := range to {
		if err := c.Rcpt(t); err != nil {
			return err
		}
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write([]byte(msg)); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

func addrOnly(s string) string {
	if i := strings.LastIndex(s, "<"); i >= 0 {
		return strings.TrimSuffix(s[i+1:], ">")
	}
	return strings.TrimSpace(s)
}

// TestMail sends a test e-mail with the given settings to the administrator.
func (ss *Session) TestMail(st Settings) error {
	a, err := ss.me(ss.s.dbx())
	if err != nil {
		return err
	}
	if !a.admin() {
		return ErrForbidden
	}
	if a.Email == "" {
		return errf(CodeInvalid, "no_email")
	}
	if st.SMTPPassword == "" {
		old, _ := loadSettings(ss.s.dbx())
		st.SMTPPassword = old.SMTPPassword
	}
	return ss.s.mail(st, []string{a.Email}, "Эгида: проверка почты", "Это тестовое письмо «Эгиды».")
}

func (ss *Session) Notifications(unreadOnly bool, limit int) ([]Notification, error) {
	d := ss.s.dbx()
	if _, err := ss.me(d); err != nil {
		return nil, err
	}
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	q := "SELECT id, user_id, type, actor_id, task_id, text, is_read, created_at FROM notifications WHERE user_id = ?"
	if unreadOnly {
		q += " AND is_read = 0"
	}
	var out []Notification
	err := eachRow(d, func(r *sql.Rows) error {
		var n Notification
		var read int
		if err := r.Scan(&n.ID, &n.UserID, &n.Type, &n.ActorID, &n.TaskID, &n.Text, &read, &n.CreatedAt); err != nil {
			return err
		}
		n.Read = read == 1
		out = append(out, n)
		return nil
	}, q+" ORDER BY id DESC LIMIT "+strconv.Itoa(limit), ss.uid)
	return out, err
}

// MarkRead marks the given notifications (all when ids is empty) as read.
func (ss *Session) MarkRead(ids []int64) error {
	return ss.s.tx(func(d dbx) error {
		if _, err := ss.me(d); err != nil {
			return err
		}
		if len(ids) == 0 {
			_, err := d.exec("UPDATE notifications SET is_read = 1 WHERE user_id = ? AND is_read = 0", ss.uid)
			return err
		}
		in, args := inList(ids)
		_, err := d.exec("UPDATE notifications SET is_read = 1 WHERE user_id = ? AND id IN "+in, append([]any{ss.uid}, args...)...)
		return err
	})
}

// CheckDeadlines notifies the assignees of tasks whose deadline is within the
// configured number of hours. Several processes may run it concurrently: each
// (task, user, deadline) is notified once thanks to the deadline_sent key.
func (s *Service) CheckDeadlines() error {
	ob := &outbox{}
	sent := 0
	err := s.tx0(func(d dbx) error {
		st, err := loadSettings(d)
		if err != nil || st.DeadlineWarnHours <= 0 {
			return err
		}
		limit := nowMs() + int64(st.DeadlineWarnHours)*3_600_000
		ts, err := loadTasks(d, "archived = 0 AND completed_at = 0 AND deadline > 0 AND deadline <= ?", limit)
		if err != nil {
			return err
		}
		for _, t := range ts {
			for _, uid := range t.Assignees() {
				res, err := d.exec("INSERT INTO deadline_sent (task_id, user_id, deadline) VALUES (?, ?, ?) ON CONFLICT DO NOTHING", t.ID, uid, t.Deadline)
				if err != nil {
					return err
				}
				if n, _ := res.RowsAffected(); n == 1 {
					if err := notify(d, ob, 0, NotifyDeadline, t.ID, t.Title, []int64{uid}); err != nil {
						return err
					}
					sent++
				}
			}
		}
		if sent > 0 {
			_, err = d.exec("UPDATE meta SET rev = rev + 1 WHERE id = 1")
		}
		return err
	})
	if err == nil {
		s.flush(ob)
	}
	return err
}
