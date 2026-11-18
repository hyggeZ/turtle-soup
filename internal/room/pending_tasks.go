package room

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

const roomTaskPageSize = 50

type roomTaskItem struct {
	ID        int64     `json:"id"`
	UserID    int64     `json:"userId"`
	Kind      string    `json:"kind"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"createdAt"`
}

type roomPendingTasks struct {
	PendingRestore bool           `json:"pendingRestore"`
	PendingAnswer  bool           `json:"pendingAnswer"`
	Items          []roomTaskItem `json:"items"`
	NextAfter      int64          `json:"nextAfter"`
}

// 待办与历史事件分开读取，仍使用调用方的同一个权限一致性事务。
func readRoomPendingTasks(ctx context.Context, tx pgx.Tx, roomID, userID int64, role, status string, after int64) (roomPendingTasks, error) {
	tasks := roomPendingTasks{Items: make([]roomTaskItem, 0)}
	if status != "active" && status != "paused" {
		return tasks, nil
	}
	if role == "player" {
		// 与提交还原、申请看底的防重复检查使用相同的回复类型。
		err := tx.QueryRow(ctx, `SELECT
			EXISTS(SELECT 1 FROM room_events e WHERE e.room_id=$1 AND e.user_id=$2 AND e.kind='restore'
				AND NOT EXISTS(SELECT 1 FROM room_events reply WHERE reply.parent_id=e.id AND reply.kind='restore_result')),
			EXISTS(SELECT 1 FROM room_events e WHERE e.room_id=$1 AND e.user_id=$2 AND e.kind='answer_request'
				AND NOT EXISTS(SELECT 1 FROM room_events reply WHERE reply.parent_id=e.id AND reply.kind='answer_result'))`,
			roomID, userID).Scan(&tasks.PendingRestore, &tasks.PendingAnswer)
		return tasks, err
	}
	if role != "host" {
		return tasks, nil
	}

	// 主持可处理旧玩家的问题和看底申请；还原则仅当前玩家可被判定。
	// 任意子事件都终结主持待办，与 eventReady、readyPlayerRestore 保持一致。
	// 各房间事件量差距较大，按当前房间和游标规划查询，避免通用计划扫描过多事件。
	rows, err := tx.Query(ctx, `SELECT e.id,e.user_id,e.kind,e.body,e.created_at
		FROM room_events e LEFT JOIN room_members m ON m.room_id=e.room_id AND m.user_id=e.user_id
		WHERE e.room_id=$1 AND e.id>$2
		AND (e.kind IN ('question','answer_request') OR (e.kind='restore' AND m.role='player'))
		AND NOT EXISTS(SELECT 1 FROM room_events reply WHERE reply.parent_id=e.id)
		ORDER BY e.id LIMIT 51`, pgx.QueryExecModeExec, roomID, after)
	if err != nil {
		return tasks, err
	}
	for rows.Next() {
		var item roomTaskItem
		if err := rows.Scan(&item.ID, &item.UserID, &item.Kind, &item.Body, &item.CreatedAt); err != nil {
			rows.Close()
			return tasks, err
		}
		if len(tasks.Items) == roomTaskPageSize {
			tasks.NextAfter = tasks.Items[len(tasks.Items)-1].ID
			break
		}
		tasks.Items = append(tasks.Items, item)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return tasks, err
	}
	return tasks, nil
}
