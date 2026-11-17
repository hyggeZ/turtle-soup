package room

import (
	"errors"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
)

// snapshot 只向成员展示其角色允许看到的事件内容。
func (h *Handler) get(c *gin.Context) {
	queryParams := c.Request.URL.Query()
	history := queryParams.Has("history")
	if history && (len(queryParams["history"]) != 1 || c.Query("history") != "1" || queryParams.Has("after")) {
		c.JSON(400, gin.H{"error": "历史分页参数无效"})
		return
	}
	tasksRequested := queryParams.Has("tasks")
	if tasksRequested && (len(queryParams["tasks"]) != 1 || c.Query("tasks") != "1") {
		c.JSON(400, gin.H{"error": "待办参数无效"})
		return
	}
	var tasksAfter int64
	if tasksRequested && queryParams.Has("tasksAfter") {
		if len(queryParams["tasksAfter"]) != 1 {
			c.JSON(400, gin.H{"error": "待办游标无效"})
			return
		}
		var err error
		tasksAfter, err = strconv.ParseInt(c.Query("tasksAfter"), 10, 64)
		if err != nil || tasksAfter < 0 {
			c.JSON(400, gin.H{"error": "待办游标无效"})
			return
		}
	}
	var before int64
	if history && queryParams.Has("before") {
		if len(queryParams["before"]) != 1 {
			c.JSON(400, gin.H{"error": "历史分页游标无效"})
			return
		}
		var err error
		before, err = strconv.ParseInt(c.Query("before"), 10, 64)
		if err != nil || before <= 0 {
			c.JSON(400, gin.H{"error": "历史分页游标无效"})
			return
		}
	}
	var after int64
	if raw := c.Query("after"); raw != "" {
		var err error
		after, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || after < 0 {
			c.JSON(400, gin.H{"error": "事件游标无效"})
			return
		}
	}
	u, ok := h.user(c)
	if !ok {
		return
	}
	id, ok := roomID(c)
	if !ok {
		return
	}
	ctx := c.Request.Context()
	begin := func() (pgx.Tx, error) {
		return h.db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	}
	// 身份、成员和事件必须来自同一个数据库快照，避免主持交接后泄露新私有事件。
	tx, e := begin()
	if e != nil {
		fail(c, e)
		return
	}
	defer func() { _ = tx.Rollback(ctx) }()
	var code, mode, status, title, surface, genre, role, answer string
	var owner, soupID int64
	var host *int64
	var max, difficulty, horror int
	var public, answerPublished bool
	var turn *int64
	var deadline *time.Time
	var winner *int64
	// 汤底在成员身份查询中按权限返回，减少一次数据库往返。
	query := `SELECT r.code,r.mode,r.status,r.owner_id,r.host_id,r.soup_id,r.max_players,r.public,r.turn_user_id,r.turn_deadline,r.winner_id,r.soup_title,r.soup_surface,r.soup_genre,r.soup_difficulty,r.soup_horror,r.answer_published,m.role,
		CASE WHEN m.role='host' OR m.seen_answer OR r.answer_published THEN COALESCE(r.soup_answer,'') ELSE '' END
		FROM rooms r JOIN room_members m ON m.room_id=r.id AND m.user_id=$2 WHERE r.id=$1`
	read := func() error {
		return tx.QueryRow(ctx, query, id, u.ID).Scan(&code, &mode, &status, &owner, &host, &soupID, &max, &public, &turn, &deadline, &winner, &title, &surface, &genre, &difficulty, &horror, &answerPublished, &role, &answer)
	}
	e = read()
	if e != nil {
		fail(c, e)
		return
	}
	if mode == "competitive" && status == "active" && deadline != nil && !time.Now().Before(*deadline) {
		// 到期推进使用独立写事务；随后重开只读事务才能看到推进后的状态。
		if e := tx.Rollback(ctx); e != nil {
			fail(c, e)
			return
		}
		if e := h.advanceExpiredTurn(ctx, id); e != nil {
			fail(c, e)
			return
		}
		nextTx, e := begin()
		if e != nil {
			fail(c, e)
			return
		}
		tx = nextTx
		// 即使还有待答问题导致截止时间仍过期，本次请求也只尝试推进一次。
		if e := read(); e != nil {
			fail(c, e)
			return
		}
	}
	if before > 0 && status != "finished" {
		c.JSON(409, gin.H{"error": "仅已结束房间支持历史翻页"})
		return
	}
	members := make([]gin.H, 0)
	rows, e := tx.Query(ctx, `SELECT m.user_id,u.username,m.role,m.correct,m.host_request FROM room_members m JOIN users u ON u.id=m.user_id WHERE m.room_id=$1 ORDER BY m.joined_at,m.user_id`, id)
	if e != nil {
		fail(c, e)
		return
	}
	for rows.Next() {
		var uid int64
		var name, r string
		var correct, req bool
		if rows.Scan(&uid, &name, &r, &correct, &req) != nil {
			rows.Close()
			fail(c, errors.New("scan"))
			return
		}
		members = append(members, gin.H{"id": uid, "username": name, "role": r, "correct": correct, "hostRequest": req})
	}
	if e := rows.Err(); e != nil {
		rows.Close()
		fail(c, e)
		return
	}
	rows.Close()
	events := make([]gin.H, 0)
	pagedHistory := history && status == "finished"
	var historyBefore int64
	cursor := after
	var rawEventIDs, parentQuestionIDs []int64
	if pagedHistory {
		rawEventIDs, parentQuestionIDs, historyBefore, cursor, e = roomHistoryPage(ctx, tx, id, before)
		if e != nil {
			fail(c, e)
			return
		}
	}
	eventQuery := `SELECT e.id,e.user_id,e.kind,e.body,e.result,e.parent_id,e.created_at,p.user_id,
		EXISTS(SELECT 1 FROM room_events reply WHERE reply.parent_id=e.id AND reply.kind='answer')
		FROM room_events e`
	eventArgs := []any{id}
	if pagedHistory {
		eventQuery += ` LEFT JOIN room_events p ON p.id=e.parent_id
			WHERE e.room_id=$1 AND (e.id=ANY($2::bigint[]) OR (e.kind='question' AND e.id=ANY($3::bigint[]))) ORDER BY e.id`
		eventArgs = append(eventArgs, rawEventIDs, parentQuestionIDs)
	} else if after > 0 {
		// 新回答会使旧的未回答问题首次公开，需要一并补发给其他玩家。
		eventQuery += ` JOIN (
			SELECT id FROM room_events WHERE room_id=$1 AND id>$2
			UNION
			SELECT parent_id FROM room_events
			WHERE room_id=$1 AND id>$2 AND kind='answer' AND parent_id IS NOT NULL
		) changed ON changed.id=e.id`
		eventArgs = append(eventArgs, after)
	}
	if !pagedHistory {
		eventQuery += ` LEFT JOIN room_events p ON p.id=e.parent_id WHERE e.room_id=$1 ORDER BY e.id`
	}
	ev, e := tx.Query(ctx, eventQuery, eventArgs...)
	if e != nil {
		fail(c, e)
		return
	}
	for ev.Next() {
		var eid, uid int64
		var kind, body, result string
		var parent *int64
		var subject *int64
		var at time.Time
		var answered bool
		if ev.Scan(&eid, &uid, &kind, &body, &result, &parent, &at, &subject, &answered) != nil {
			ev.Close()
			fail(c, errors.New("scan"))
			return
		}
		// 游标包含过滤掉的私有事件，后续刷新不必重复读取这些事件。
		if !pagedHistory {
			cursor = eid
		}
		if kind == "question" && !answered && uid != u.ID && role != "host" {
			continue
		}
		if kind == "restore" && uid != u.ID && role != "host" {
			// 私人还原在判定前不向其他玩家暴露提交者或提交时间。
			continue
		}
		if kind == "answer_request" && uid != u.ID && role != "host" {
			continue
		}
		if kind == "answer_result" && role != "host" && (subject == nil || *subject != u.ID) {
			continue
		}
		events = append(events, gin.H{"id": eid, "userId": uid, "subjectUserId": subject, "kind": kind, "body": body, "result": result, "parentId": parent, "createdAt": at})
	}
	if e := ev.Err(); e != nil {
		ev.Close()
		fail(c, e)
		return
	}
	ev.Close()
	var pendingTasks roomPendingTasks
	if tasksRequested {
		pendingTasks, e = readRoomPendingTasks(ctx, tx, id, u.ID, role, status, tasksAfter)
		if e != nil {
			fail(c, e)
			return
		}
	}
	if e := tx.Commit(ctx); e != nil {
		fail(c, e)
		return
	}
	response := gin.H{"id": id, "code": code, "mode": mode, "status": status, "ownerId": owner, "hostId": host, "soupId": soupID, "maxPlayers": max, "public": public, "turnUserId": turn, "turnDeadline": deadline, "serverTime": time.Now().UTC(), "winnerId": winner, "title": title, "surface": surface, "genre": genre, "difficulty": difficulty, "horror": horror, "answerPublished": answerPublished, "answer": answer, "myRole": role, "members": members, "events": events, "eventCursor": cursor}
	if pagedHistory {
		response["historyBefore"] = historyBefore
	}
	if tasksRequested {
		response["tasks"] = pendingTasks
	}
	c.JSON(200, response)
}
