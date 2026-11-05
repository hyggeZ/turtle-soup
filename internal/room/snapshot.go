package room

import (
	"errors"
	"github.com/gin-gonic/gin"
	"time"
)

// snapshot 只向成员展示其角色允许看到的事件内容。
func (h *Handler) get(c *gin.Context) {
	u, ok := h.user(c)
	if !ok {
		return
	}
	id, ok := roomID(c)
	if !ok {
		return
	}
	var code, mode, status, title, surface, role string
	var owner, host, soupID int64
	var max int
	var public, seen bool
	var turn *int64
	var deadline *time.Time
	var winner *int64
	e := h.db.QueryRow(c.Request.Context(), `SELECT r.code,r.mode,r.status,r.owner_id,r.host_id,r.soup_id,r.max_players,r.public,r.turn_user_id,r.turn_deadline,r.winner_id,s.title,s.surface,m.role,m.seen_answer FROM rooms r JOIN soups s ON s.id=r.soup_id JOIN room_members m ON m.room_id=r.id AND m.user_id=$2 WHERE r.id=$1`, id, u.ID).Scan(&code, &mode, &status, &owner, &host, &soupID, &max, &public, &turn, &deadline, &winner, &title, &surface, &role, &seen)
	if e != nil {
		fail(c, e)
		return
	}
	members := make([]gin.H, 0)
	rows, e := h.db.Query(c.Request.Context(), `SELECT m.user_id,u.username,m.role,m.correct,m.host_request FROM room_members m JOIN users u ON u.id=m.user_id WHERE m.room_id=$1 ORDER BY m.joined_at,m.user_id`, id)
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
	rows.Close()
	events := make([]gin.H, 0)
	ev, e := h.db.Query(c.Request.Context(), `SELECT id,user_id,kind,body,result,parent_id,created_at FROM room_events WHERE room_id=$1 ORDER BY id`, id)
	if e != nil {
		fail(c, e)
		return
	}
	for ev.Next() {
		var eid, uid int64
		var kind, body, result string
		var parent *int64
		var at time.Time
		if ev.Scan(&eid, &uid, &kind, &body, &result, &parent, &at) != nil {
			ev.Close()
			fail(c, errors.New("scan"))
			return
		}
		if kind == "restore" && uid != u.ID && role != "host" {
			body = ""
		}
		if kind == "answer_request" && uid != u.ID && role != "host" {
			continue
		}
		events = append(events, gin.H{"id": eid, "userId": uid, "kind": kind, "body": body, "result": result, "parentId": parent, "createdAt": at})
	}
	ev.Close()
	answer := ""
	if role == "host" || seen {
		_ = h.db.QueryRow(c.Request.Context(), `SELECT answer FROM soups WHERE id=$1`, soupID).Scan(&answer)
	}
	c.JSON(200, gin.H{"id": id, "code": code, "mode": mode, "status": status, "ownerId": owner, "hostId": host, "soupId": soupID, "maxPlayers": max, "public": public, "turnUserId": turn, "turnDeadline": deadline, "winnerId": winner, "title": title, "surface": surface, "answer": answer, "myRole": role, "members": members, "events": events})
}
