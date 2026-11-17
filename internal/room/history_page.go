package room

import (
	"context"
	"errors"
	"net/url"
	"strconv"

	"github.com/jackc/pgx/v5"
)

const roomHistoryPageSize = 50

type roomEventRequest struct {
	version  string
	before   int64
	after    int64
	hasAfter bool
}

// 旧请求沿用原来的 after 解析；只有显式选择 history=2 才启用有界增量。
func parseRoomEventRequest(params url.Values) (roomEventRequest, error) {
	var request roomEventRequest
	if values, present := params["history"]; present {
		if len(values) != 1 || values[0] != "1" && values[0] != "2" {
			return request, errors.New("历史分页参数无效")
		}
		request.version = values[0]
	}
	if request.version == "1" && params.Has("after") ||
		request.version == "2" && params.Has("before") && params.Has("after") {
		return request, errors.New("历史分页参数无效")
	}
	if request.version != "" && params.Has("before") {
		values := params["before"]
		if len(values) != 1 {
			return request, errors.New("历史分页游标无效")
		}
		before, err := strconv.ParseInt(values[0], 10, 64)
		if err != nil || before <= 0 {
			return request, errors.New("历史分页游标无效")
		}
		request.before = before
	}
	if request.version == "2" && params.Has("after") {
		values := params["after"]
		if len(values) != 1 {
			return request, errors.New("事件游标无效")
		}
		after, err := strconv.ParseInt(values[0], 10, 64)
		if err != nil || after < 0 {
			return request, errors.New("事件游标无效")
		}
		request.after = after
		request.hasAfter = true
	} else if raw := params.Get("after"); raw != "" {
		after, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || after < 0 {
			return request, errors.New("事件游标无效")
		}
		request.after = after
	}
	return request, nil
}

// roomHistoryPage 按原始事件 ID 取有界窗口；游标包含之后会被权限过滤的事件。
// 初始页的 eventCursor 是房间最新事件，较早页面的 eventCursor 是该页最新事件。
func roomHistoryPage(ctx context.Context, tx pgx.Tx, roomID, before int64) (rawIDs, parentQuestionIDs []int64, nextBefore, eventCursor int64, err error) {
	query := `SELECT id,kind,parent_id FROM room_events WHERE room_id=$1 ORDER BY id DESC LIMIT 51`
	args := []any{roomID}
	if before > 0 {
		query = `SELECT id,kind,parent_id FROM room_events WHERE room_id=$1 AND id<$2 ORDER BY id DESC LIMIT 51`
		args = append(args, before)
	}
	rawIDs, parentQuestionIDs, more, err := roomEventWindowIDs(ctx, tx, query, args...)
	if err != nil {
		return nil, nil, 0, 0, err
	}
	if len(rawIDs) > 0 {
		eventCursor = rawIDs[0]
		if more {
			nextBefore = rawIDs[len(rawIDs)-1]
		}
	}
	return rawIDs, parentQuestionIDs, nextBefore, eventCursor, nil
}

// roomDeltaPage 先限定新原始事件，再补其回答所对应的旧问题。
// 新事件即使稍后被权限过滤，也必须推进 eventCursor。
func roomDeltaPage(ctx context.Context, tx pgx.Tx, roomID, after int64) (rawIDs, parentQuestionIDs []int64, eventCursor int64, hasMoreNew bool, err error) {
	query := `SELECT id,kind,parent_id FROM room_events WHERE room_id=$1 AND id>$2 ORDER BY id ASC LIMIT 51`
	rawIDs, parentQuestionIDs, hasMoreNew, err = roomEventWindowIDs(ctx, tx, query, roomID, after)
	if err != nil {
		return nil, nil, 0, false, err
	}
	eventCursor = after
	if len(rawIDs) > 0 {
		eventCursor = rawIDs[len(rawIDs)-1]
	}
	return rawIDs, parentQuestionIDs, eventCursor, hasMoreNew, nil
}

// 两个方向都先取 51 条原始事件，用额外一条判断是否还有下一页。
func roomEventWindowIDs(ctx context.Context, tx pgx.Tx, query string, args ...any) (rawIDs, parentQuestionIDs []int64, more bool, err error) {
	rows, err := tx.Query(ctx, query, args...)
	if err != nil {
		return nil, nil, false, err
	}
	rawIDs = make([]int64, 0, roomHistoryPageSize)
	parentQuestionIDs = make([]int64, 0, roomHistoryPageSize)
	for rows.Next() {
		var id int64
		var kind string
		var parent *int64
		if err = rows.Scan(&id, &kind, &parent); err != nil {
			rows.Close()
			return nil, nil, false, err
		}
		if len(rawIDs) == roomHistoryPageSize {
			more = true
			break
		}
		rawIDs = append(rawIDs, id)
		if kind == "answer" && parent != nil {
			parentQuestionIDs = append(parentQuestionIDs, *parent)
		}
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return nil, nil, false, err
	}
	return rawIDs, parentQuestionIDs, more, nil
}
