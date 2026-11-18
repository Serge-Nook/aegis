package core

import (
	"sort"
	"strings"
	"time"
)

// Report builds the analytics of ТЗ п. 3.7 over the tasks the user can see
// (an executor only sees his own tasks in reports).
func (ss *Session) Report(f ReportFilter) (*Report, error) {
	d := ss.s.dbx()
	a, err := ss.me(d)
	if err != nil {
		return nil, err
	}
	boards, err := visibleBoards(d, a, true)
	if err != nil {
		return nil, err
	}
	byBoard := map[int64]*Board{}
	var ids []int64
	for i := range boards {
		if f.BoardID != 0 && boards[i].ID != f.BoardID {
			continue
		}
		if f.DeptID != 0 && boards[i].DeptID != f.DeptID {
			continue
		}
		byBoard[boards[i].ID] = &boards[i]
		ids = append(ids, boards[i].ID)
	}
	rep := &Report{Total: GroupStats{Name: "Всего"}}
	if len(ids) == 0 {
		return rep, nil
	}
	in, args := inList(ids)
	ts, err := loadTasks(d, "board_id IN "+in+" AND archived = 0", args...)
	if err != nil {
		return nil, err
	}
	colName := map[int64]string{}
	colPos := map[string]int{}
	for _, bid := range ids {
		cols, err := loadColumns(d, bid)
		if err != nil {
			return nil, err
		}
		for _, c := range cols {
			colName[c.ID] = c.Name
			if p, ok := colPos[c.Name]; !ok || c.Pos < p {
				colPos[c.Name] = c.Pos
			}
		}
	}
	users, err := ss.Users()
	if err != nil {
		return nil, err
	}
	userName := map[int64]string{}
	for _, u := range users {
		userName[u.ID] = displayName(u)
	}
	depts, err := ss.Departments()
	if err != nil {
		return nil, err
	}
	deptName := map[int64]string{0: "Без отдела"}
	for _, dp := range depts {
		deptName[dp.ID] = dp.Name
	}
	tagName := map[int64]string{}
	if tags, err := ss.Tags(); err == nil {
		for _, t := range tags {
			tagName[t.ID] = t.Name
		}
	}

	now := nowMs()
	type acc struct {
		GroupStats
		hours  float64
		stages map[string]int
	}
	newAcc := func(id int64, name string) *acc {
		return &acc{GroupStats: GroupStats{ID: id, Name: name}, stages: map[string]int{}}
	}
	total := newAcc(0, "Всего")
	deptAcc := map[int64]*acc{}
	userAcc := map[int64]*acc{}
	days := map[int64]*DayPoint{}
	dayOf := func(ms int64) int64 {
		t := time.UnixMilli(ms)
		return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.Local).UnixMilli()
	}
	add := func(g *acc, t *taskRow) {
		g.Total++
		g.stages[colName[t.ColumnID]]++
		switch {
		case t.CompletedAt > 0:
			g.Done++
			g.hours += float64(t.CompletedAt-t.CreatedAt) / 3_600_000
		default:
			g.InProgress++
		}
		if t.Overdue(now) {
			g.Overdue++
		}
	}
	for i := range ts {
		t := &ts[i]
		if f.From > 0 && t.CreatedAt < f.From {
			continue
		}
		if f.To > 0 && t.CreatedAt >= f.To {
			continue
		}
		if f.Priority >= 0 && int(t.Priority) != f.Priority {
			continue
		}
		if f.TagID != 0 && !containsID(t.Tags, f.TagID) {
			continue
		}
		assignees := t.Assignees()
		if f.UserID != 0 && !containsID(assignees, f.UserID) {
			continue
		}
		if a.Role == RoleExecutor && !containsID(assignees, a.ID) && t.AuthorID != a.ID {
			continue
		}
		b := byBoard[t.BoardID]
		add(total, t)
		dg := deptAcc[b.DeptID]
		if dg == nil {
			dg = newAcc(b.DeptID, deptName[b.DeptID])
			deptAcc[b.DeptID] = dg
		}
		add(dg, t)
		if len(assignees) == 0 {
			assignees = []int64{0}
		}
		for _, uid := range assignees {
			ug := userAcc[uid]
			if ug == nil {
				name := userName[uid]
				if uid == 0 {
					name = "Без исполнителя"
				}
				ug = newAcc(uid, name)
				userAcc[uid] = ug
			}
			add(ug, t)
		}
		rep.ByPriority[min(max(int(t.Priority), 0), 3)]++
		dc := dayOf(t.CreatedAt)
		if days[dc] == nil {
			days[dc] = &DayPoint{Day: dc}
		}
		days[dc].Created++
		if t.CompletedAt > 0 {
			dd := dayOf(t.CompletedAt)
			if days[dd] == nil {
				days[dd] = &DayPoint{Day: dd}
			}
			days[dd].Completed++
		}
		var co, tg []string
		for _, uid := range t.CoExecutors {
			co = append(co, userName[uid])
		}
		for _, id := range t.Tags {
			tg = append(tg, tagName[id])
		}
		rep.Tasks = append(rep.Tasks, TaskRow{
			ID: t.ID, Title: t.Title, Board: b.Name, Column: colName[t.ColumnID], Department: deptName[b.DeptID],
			Priority: t.Priority, Responsible: userName[t.ResponsibleID], CoExecutors: strings.Join(co, ", "),
			Tags: strings.Join(tg, ", "), Deadline: t.Deadline, CreatedAt: t.CreatedAt, CompletedAt: t.CompletedAt, Overdue: t.Overdue(now),
		})
	}
	finish := func(g *acc) GroupStats {
		s := g.GroupStats
		if s.Total > 0 {
			s.OverduePct = 100 * float64(s.Overdue) / float64(s.Total)
		}
		if s.Done > 0 {
			s.AvgHours = g.hours / float64(s.Done)
		}
		for name, n := range g.stages {
			s.ByStage = append(s.ByStage, StageCount{Stage: name, Count: n})
		}
		sort.Slice(s.ByStage, func(i, j int) bool {
			pi, pj := colPos[s.ByStage[i].Stage], colPos[s.ByStage[j].Stage]
			if pi != pj {
				return pi < pj
			}
			return s.ByStage[i].Stage < s.ByStage[j].Stage
		})
		return s
	}
	rep.Total = finish(total)
	for _, g := range deptAcc {
		rep.Depts = append(rep.Depts, finish(g))
	}
	sort.Slice(rep.Depts, func(i, j int) bool { return rep.Depts[i].Name < rep.Depts[j].Name })
	for _, g := range userAcc {
		rep.Users = append(rep.Users, finish(g))
	}
	sort.Slice(rep.Users, func(i, j int) bool { return rep.Users[i].Name < rep.Users[j].Name })
	for _, p := range days {
		rep.Dynamics = append(rep.Dynamics, *p)
	}
	sort.Slice(rep.Dynamics, func(i, j int) bool { return rep.Dynamics[i].Day < rep.Dynamics[j].Day })
	sort.Slice(rep.Tasks, func(i, j int) bool { return rep.Tasks[i].ID < rep.Tasks[j].ID })
	return rep, nil
}

func displayName(u User) string {
	if u.FullName != "" {
		return u.FullName
	}
	return u.Login
}
