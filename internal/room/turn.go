package room

import "context"

// 到期时由房间快照触发推进；房间行锁保证多个客户端同时刷新也只推进一次。
func (h *Handler) advanceExpiredTurn(ctx context.Context, id int64) error {
	tx, err := h.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var mode, status string
	var turn *int64
	var expired bool
	err = tx.QueryRow(ctx, `SELECT mode,status,turn_user_id,
		COALESCE(turn_deadline<=NOW(),FALSE) FROM rooms WHERE id=$1 FOR UPDATE`, id).
		Scan(&mode, &status, &turn, &expired)
	if err != nil {
		return err
	}
	if mode != "competitive" || status != "active" || turn == nil || !expired {
		return nil
	}
	pending, err := unansweredQuestion(ctx, tx, id, *turn)
	if err != nil {
		return err
	}
	if pending {
		return nil
	}
	if err := nextTurn(ctx, tx, id, turn); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	h.liveHub.notify(id)
	return nil
}
