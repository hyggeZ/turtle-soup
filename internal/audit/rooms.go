package audit

import (
	"context"

	"github.com/jackc/pgx/v5"
)

const roomFindingLimit = 20

// RoomFinding 仅包含定位问题所需的房间编号和固定异常类型。
type RoomFinding struct {
	RoomID int64  `json:"roomId"`
	Issue  string `json:"issue"`
}

// checkRooms 检查房间和成员表的对应关系，同一房间的每类异常分别计数。
func checkRooms(ctx context.Context, tx pgx.Tx) (int64, []RoomFinding, error) {
	rows, err := tx.Query(ctx, `WITH room_state AS (
		SELECT r.id,r.status,r.mode,r.host_id,r.turn_user_id,r.turn_deadline,r.winner_id,r.max_players,
			count(*) FILTER (WHERE m.user_id=r.owner_id) AS owners,
			count(*) FILTER (WHERE m.role='host') AS hosts,
			count(*) FILTER (WHERE m.role='host' AND m.user_id=r.host_id) AS linked_hosts,
			count(*) FILTER (WHERE m.role='player') AS players,
			count(*) FILTER (WHERE m.role='player' AND m.seen_answer) AS players_seen_answer,
			count(*) FILTER (WHERE m.user_id=r.winner_id AND m.role='spectator' AND m.correct) AS winners,
			count(*) FILTER (WHERE m.user_id=r.turn_user_id) AS turn_members
		FROM rooms r LEFT JOIN room_members m ON m.room_id=r.id
		GROUP BY r.id
	)
	SELECT count(*) OVER (), r.id, f.issue
	FROM room_state r CROSS JOIN LATERAL (VALUES
		('owner_missing', r.owners=0),
		('host_mismatch', (r.host_id IS NULL AND (r.hosts<>0 OR r.status IN ('active','paused','finished')))
			OR (r.host_id IS NOT NULL AND (r.hosts<>1 OR r.linked_hosts<>1))),
		('player_capacity_exceeded', r.players>r.max_players),
		('player_has_seen_answer', r.players_seen_answer>0),
		('winner_mismatch', r.winner_id IS NOT NULL AND (r.status<>'finished' OR r.mode<>'competitive' OR r.winners<>1)),
		('turn_member_missing', r.mode='competitive' AND r.status IN ('active','paused')
			AND (r.turn_members=0 OR r.turn_deadline IS NULL))
	) AS f(issue,broken)
	WHERE f.broken
	ORDER BY r.id,f.issue
	LIMIT $1`, roomFindingLimit)
	if err != nil {
		return 0, nil, err
	}
	defer rows.Close()
	var total int64
	samples := make([]RoomFinding, 0, roomFindingLimit)
	for rows.Next() {
		var sample RoomFinding
		if err := rows.Scan(&total, &sample.RoomID, &sample.Issue); err != nil {
			return 0, nil, err
		}
		samples = append(samples, sample)
	}
	if err := rows.Err(); err != nil {
		return 0, nil, err
	}
	return total, samples, nil
}
