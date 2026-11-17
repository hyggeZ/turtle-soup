package room

import (
	"context"

	"github.com/jackc/pgx/v5"
)

const roomHistoryPageSize = 50

// roomHistoryPage 按原始事件 ID 取有界窗口；游标包含之后会被权限过滤的事件。
// 初始页的 eventCursor 是房间最新事件，较早页面的 eventCursor 是该页最新事件。
func roomHistoryPage(ctx context.Context, tx pgx.Tx, roomID, before int64) (rawIDs, parentQuestionIDs []int64, nextBefore, eventCursor int64, err error) {
	query := `SELECT id,kind,parent_id FROM room_events WHERE room_id=$1 ORDER BY id DESC LIMIT 51`
	args := []any{roomID}
	if before > 0 {
		query = `SELECT id,kind,parent_id FROM room_events WHERE room_id=$1 AND id<$2 ORDER BY id DESC LIMIT 51`
		args = append(args, before)
	}
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, nil, 0, 0, err
	}
	rawIDs = make([]int64, 0, roomHistoryPageSize)
	parentQuestionIDs = make([]int64, 0, roomHistoryPageSize)
	more := false
	for rows.Next() {
		var id int64
		var kind string
		var parent *int64
		if err = rows.Scan(&id, &kind, &parent); err != nil {
			rows.Close()
			return nil, nil, 0, 0, err
		}
		if len(rawIDs) == roomHistoryPageSize {
			more = true
			break
		}
		if len(rawIDs) == 0 {
			eventCursor = id
		}
		rawIDs = append(rawIDs, id)
		if kind == "answer" && parent != nil {
			parentQuestionIDs = append(parentQuestionIDs, *parent)
		}
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, nil, 0, 0, err
	}
	if more {
		nextBefore = rawIDs[len(rawIDs)-1]
	}
	return rawIDs, parentQuestionIDs, nextBefore, eventCursor, nil
}
