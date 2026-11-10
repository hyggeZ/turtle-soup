package room

import (
	"context"

	"github.com/jackc/pgx/v5"
)

// 所有会让成员获得汤底的操作都锁同一题目行，与入场和社区揭底排队。
func lockSoup(ctx context.Context, tx pgx.Tx, soupID int64) error {
	var id int64
	return tx.QueryRow(ctx, `SELECT id FROM soups WHERE id=$1 FOR UPDATE`, soupID).Scan(&id)
}

// 检查用户在另一场未结束的同题真人对局中是否仍占玩家席位。
func playingElsewhere(ctx context.Context, tx pgx.Tx, soupID, userID, roomID int64) (bool, error) {
	var playing bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM rooms r JOIN room_members m ON m.room_id=r.id
		WHERE r.soup_id=$1 AND r.id<>$3 AND r.status IN ('waiting','active','paused')
		AND m.user_id=$2 AND m.role='player'
	)`, soupID, userID, roomID).Scan(&playing)
	return playing, err
}

// 公开汤底前检查本房间任一成员是否仍在其他同题房间推理。
func memberPlayingElsewhere(ctx context.Context, tx pgx.Tx, soupID, roomID int64) (bool, error) {
	var playing bool
	err := tx.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM room_members here
		JOIN room_members other ON other.user_id=here.user_id AND other.role='player'
		JOIN rooms r ON r.id=other.room_id
		WHERE here.room_id=$2 AND r.id<>$2 AND r.soup_id=$1
		AND r.status IN ('waiting','active','paused')
	)`, soupID, roomID).Scan(&playing)
	return playing, err
}
