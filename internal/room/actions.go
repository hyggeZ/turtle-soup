package room

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
)

// perform 在房间行锁内检查规则并写入事件，避免两次操作同时改变对局。
func perform(ctx context.Context, tx pgx.Tx, id, user, owner, host, soupID int64, max int, mode, status, role string, turn *int64, deadline *time.Time, in actionInput) (string, error) {
	bad := func() (string, error) { return "", errors.New("当前状态不允许此操作") }
	add := func(kind, body, result string, parent int64) error {
		var link *int64
		if parent > 0 {
			link = &parent
		}
		_, err := tx.Exec(ctx, `INSERT INTO room_events(room_id,user_id,kind,body,result,parent_id) VALUES($1,$2,$3,$4,$5,$6)`, id, user, kind, body, result, link)
		return err
	}
	switch in.Type {
	case "host_request":
		if status != "waiting" || role == "host" {
			return bad()
		}
		_, err := tx.Exec(ctx, `UPDATE room_members SET host_request=TRUE WHERE room_id=$1 AND user_id=$2`, id, user)
		return "已申请主持", err
	case "host_approve":
		if status != "waiting" || user != owner || in.UserID == owner {
			return bad()
		}
		var requested bool
		if tx.QueryRow(ctx, `SELECT host_request FROM room_members WHERE room_id=$1 AND user_id=$2`, id, in.UserID).Scan(&requested) != nil || !requested {
			return bad()
		}
		if _, err := tx.Exec(ctx, `UPDATE room_members SET role='spectator',seen_answer=TRUE WHERE room_id=$1 AND user_id=$2`, id, host); err != nil {
			return "", err
		}
		if _, err := tx.Exec(ctx, `UPDATE room_members SET role='host',seen_answer=TRUE,host_request=FALSE WHERE room_id=$1 AND user_id=$2`, id, in.UserID); err != nil {
			return "", err
		}
		_, err := tx.Exec(ctx, `UPDATE rooms SET host_id=$2 WHERE id=$1`, id, in.UserID)
		return "主持人已更换", err
	case "start":
		if status != "waiting" || (user != owner && user != host) {
			return bad()
		}
		var first int64
		if tx.QueryRow(ctx, `SELECT user_id FROM room_members WHERE room_id=$1 AND role='player' ORDER BY joined_at,user_id LIMIT 1`, id).Scan(&first) != nil {
			return "", errors.New("至少需要一名玩家")
		}
		if mode == "competitive" {
			_, err := tx.Exec(ctx, `UPDATE rooms SET status='active',turn_user_id=$2,turn_deadline=NOW()+INTERVAL '60 seconds' WHERE id=$1`, id, first)
			return "对局开始", err
		}
		_, err := tx.Exec(ctx, `UPDATE rooms SET status='active' WHERE id=$1`, id)
		return "对局开始", err
	case "question":
		if status != "active" || role != "player" || len([]rune(in.Body)) < 1 || len([]rune(in.Body)) > 500 {
			return bad()
		}
		if mode == "competitive" && (turn == nil || *turn != user || deadline == nil || time.Now().After(*deadline)) {
			return "", errors.New("当前不是你的提问时间")
		}
		if mode == "competitive" {
			var pending int
			if err := tx.QueryRow(ctx, `SELECT count(*) FROM room_events q WHERE q.room_id=$1 AND q.kind='question' AND NOT EXISTS(SELECT 1 FROM room_events a WHERE a.parent_id=q.id)`, id).Scan(&pending); err != nil {
				return "", err
			}
			if pending > 0 {
				return "", errors.New("请等待主持人回答")
			}
		}
		return "已提问", add("question", in.Body, "", 0)
	case "answer":
		if status != "active" || role != "host" || !oneOf(in.Result, "是", "否", "无关") || !eventReady(ctx, tx, id, in.EventID, "question") {
			return bad()
		}
		if err := add("answer", "", in.Result, in.EventID); err != nil {
			return "", err
		}
		if mode == "competitive" {
			if err := nextTurn(ctx, tx, id, turn); err != nil {
				return "", err
			}
		}
		return "已回答", nil
	case "restore":
		if status != "active" || role != "player" || len([]rune(in.Body)) < 1 || len([]rune(in.Body)) > 2000 {
			return bad()
		}
		if mode == "competitive" && (turn == nil || *turn != user || deadline == nil || time.Now().After(*deadline)) {
			return "", errors.New("当前不是你的回合")
		}
		return "已提交还原", add("restore", in.Body, "", 0)
	case "judge":
		if status != "active" || role != "host" || !oneOf(in.Result, "正确", "错误", "不完整") || !eventReady(ctx, tx, id, in.EventID, "restore") {
			return bad()
		}
		if err := add("restore_result", "", in.Result, in.EventID); err != nil {
			return "", err
		}
		if in.Result == "正确" {
			var player int64
			if err := tx.QueryRow(ctx, `SELECT user_id FROM room_events WHERE id=$1`, in.EventID).Scan(&player); err != nil {
				return "", err
			}
			if _, err := tx.Exec(ctx, `UPDATE room_members SET role='spectator',correct=TRUE WHERE room_id=$1 AND user_id=$2`, id, player); err != nil {
				return "", err
			}
			if mode == "competitive" {
				_, err := tx.Exec(ctx, `UPDATE rooms SET status='finished',winner_id=$2,turn_user_id=NULL,turn_deadline=NULL WHERE id=$1`, id, player)
				return "答对，对局结束", err
			}
		}
		return "已判定", nil
	case "answer_request":
		if status != "active" || role != "player" {
			return bad()
		}
		return "已申请查看汤底", add("answer_request", "", "", 0)
	case "answer_decide":
		if status != "active" || role != "host" || !oneOf(in.Result, "同意", "拒绝") || !eventReady(ctx, tx, id, in.EventID, "answer_request") {
			return bad()
		}
		if err := add("answer_result", "", in.Result, in.EventID); err != nil {
			return "", err
		}
		if in.Result == "同意" {
			var player int64
			if err := tx.QueryRow(ctx, `SELECT user_id FROM room_events WHERE id=$1`, in.EventID).Scan(&player); err != nil {
				return "", err
			}
			if _, err := tx.Exec(ctx, `UPDATE room_members SET role='spectator',seen_answer=TRUE WHERE room_id=$1 AND user_id=$2`, id, player); err != nil {
				return "", err
			}
			if mode == "competitive" && turn != nil && *turn == player {
				if err := nextTurn(ctx, tx, id, turn); err != nil {
					return "", err
				}
			}
		}
		return "已处理申请", nil
	case "skip_turn":
		if mode != "competitive" || status != "active" || deadline == nil || time.Now().Before(*deadline) {
			return bad()
		}
		return "已跳过超时玩家", nextTurn(ctx, tx, id, turn)
	case "finish":
		if mode != "casual" || status != "active" || role != "host" {
			return bad()
		}
		_, err := tx.Exec(ctx, `UPDATE rooms SET status='finished' WHERE id=$1`, id)
		return "对局结束", err
	}
	return "", errors.New("未知操作")
}

func oneOf(value string, choices ...string) bool {
	for _, choice := range choices {
		if value == choice {
			return true
		}
	}
	return false
}

func eventReady(ctx context.Context, tx pgx.Tx, room, event int64, kind string) bool {
	if event < 1 {
		return false
	}
	var count int
	err := tx.QueryRow(ctx, `SELECT count(*) FROM room_events e WHERE e.id=$1 AND e.room_id=$2 AND e.kind=$3 AND NOT EXISTS(SELECT 1 FROM room_events reply WHERE reply.parent_id=e.id)`, event, room, kind).Scan(&count)
	return err == nil && count == 1
}

func nextTurn(ctx context.Context, tx pgx.Tx, room int64, current *int64) error {
	rows, err := tx.Query(ctx, `SELECT user_id,role FROM room_members WHERE room_id=$1 ORDER BY joined_at,user_id`, room)
	if err != nil {
		return err
	}
	players := []int64{}
	roles := []string{}
	for rows.Next() {
		var id int64
		var role string
		if err = rows.Scan(&id, &role); err != nil {
			break
		}
		players = append(players, id)
		roles = append(roles, role)
	}
	rows.Close()
	if err != nil {
		return err
	}
	active := false
	for _, role := range roles {
		if role == "player" {
			active = true
			break
		}
	}
	if !active {
		_, err = tx.Exec(ctx, `UPDATE rooms SET status='finished',turn_user_id=NULL,turn_deadline=NULL WHERE id=$1`, room)
		return err
	}
	start := 0
	if current != nil {
		for i, id := range players {
			if id == *current {
				start = (i + 1) % len(players)
				break
			}
		}
	}
	var next int64
	for i := 0; i < len(players); i++ {
		index := (start + i) % len(players)
		if roles[index] == "player" {
			next = players[index]
			break
		}
	}
	_, err = tx.Exec(ctx, `UPDATE rooms SET turn_user_id=$2,turn_deadline=NOW()+INTERVAL '60 seconds' WHERE id=$1`, room, next)
	return err
}
