package agent

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("单人对局不存在")
var ErrKnownAnswer = errors.New("已经知道这道题的汤底")
var ErrPlaying = errors.New("正在参与这道题的真人对局")
var ErrBusy = errors.New("上一条操作仍在处理")
var ErrFinished = errors.New("对局已结束")
var ErrLimit = errors.New("本局操作次数已达到上限")
var ErrDailyLimit = errors.New("今日 AI 调用次数已达到上限")
var ErrOperationMismatch = errors.New("操作 ID 已用于其他内容")

const globalDailyLimit = 300

type Store struct {
	db *pgxpool.Pool
}

func NewStore(db *pgxpool.Pool) *Store {
	return &Store{db: db}
}

type Game struct {
	ID        int64     `json:"id"`
	SoupID    int64     `json:"soupId"`
	Title     string    `json:"title"`
	Surface   string    `json:"surface"`
	Status    string    `json:"status"`
	Answer    string    `json:"answer,omitempty"`
	Events    []Event   `json:"events"`
	CreatedAt time.Time `json:"createdAt"`
}

type Event struct {
	ID        int64     `json:"id"`
	Kind      string    `json:"kind"`
	Body      string    `json:"body"`
	Result    string    `json:"result"`
	Model     string    `json:"model"`
	LatencyMS float64   `json:"latencyMs"`
	CreatedAt time.Time `json:"createdAt"`
}

type reservation struct {
	Soup Soup
}

// Create 先锁用户、再锁题目；同一用户跨题开局串行，题目锁防止同时揭底。
func (s *Store) Create(ctx context.Context, user, soupID int64) (int64, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	var lockedUser int64
	// NO KEY UPDATE 不阻挡其他表对用户外键的只读检查。
	err = tx.QueryRow(ctx, `SELECT id FROM users WHERE id=$1 FOR NO KEY UPDATE`, user).Scan(&lockedUser)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	var author int64
	var soup Soup
	err = tx.QueryRow(ctx, `SELECT author_id,title,surface,answer FROM soups WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, soupID).Scan(&author, &soup.Title, &soup.Surface, &soup.Answer)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, err
	}
	var revealed bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM soup_reveals WHERE soup_id=$1 AND user_id=$2)`, soupID, user).Scan(&revealed); err != nil {
		return 0, err
	}
	if author == user || revealed {
		return 0, ErrKnownAnswer
	}
	var playing bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM rooms r JOIN room_members m ON m.room_id=r.id
		WHERE r.soup_id=$1 AND m.user_id=$2
		AND r.status IN ('waiting','active','paused')
	)`, soupID, user).Scan(&playing)
	if err != nil {
		return 0, err
	}
	if playing {
		return 0, ErrPlaying
	}
	var existing int64
	err = tx.QueryRow(ctx, `SELECT id FROM ai_games WHERE user_id=$1 AND soup_id=$2 AND status='active'`, user, soupID).Scan(&existing)
	if err == nil {
		return existing, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return 0, err
	}
	var active int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM ai_games WHERE user_id=$1 AND status='active'`, user).Scan(&active); err != nil {
		return 0, err
	}
	if active >= 5 {
		return 0, ErrLimit
	}
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO ai_games(user_id,soup_id,title,surface,answer) VALUES($1,$2,$3,$4,$5) RETURNING id`, user, soupID, soup.Title, soup.Surface, soup.Answer).Scan(&id)
	if err != nil {
		return 0, err
	}
	return id, tx.Commit(ctx)
}

// Get 只返回所属用户的对局；汤底仅在本局结束后开放。
func (s *Store) Get(ctx context.Context, user, id int64) (Game, error) {
	var game Game
	var answer string
	err := s.db.QueryRow(ctx, `SELECT id,soup_id,title,surface,status,answer,created_at FROM ai_games WHERE id=$1 AND user_id=$2`, id, user).Scan(&game.ID, &game.SoupID, &game.Title, &game.Surface, &game.Status, &answer, &game.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Game{}, ErrNotFound
	}
	if err != nil {
		return Game{}, err
	}
	if game.Status == "finished" {
		game.Answer = answer
	}
	rows, err := s.db.Query(ctx, `SELECT id,kind,body,result,model,latency_ms,created_at FROM ai_events WHERE game_id=$1 ORDER BY id LIMIT 200`, id)
	if err != nil {
		return Game{}, err
	}
	defer rows.Close()
	game.Events = make([]Event, 0)
	for rows.Next() {
		var event Event
		if err := rows.Scan(&event.ID, &event.Kind, &event.Body, &event.Result, &event.Model, &event.LatencyMS, &event.CreatedAt); err != nil {
			return Game{}, err
		}
		game.Events = append(game.Events, event)
	}
	return game, rows.Err()
}

// Claim 用对局行锁和 90 秒租约挡住同一局的并发调用，模型调用在事务外进行。
func (s *Store) Claim(ctx context.Context, user, id int64, operationID, kind, body string) (reservation, Decision, bool, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return reservation{}, Decision{}, false, err
	}
	defer tx.Rollback(ctx)
	var soup Soup
	var status string
	var busyOp *string
	var leaseActive bool
	err = tx.QueryRow(ctx, `SELECT title,surface,answer,status,busy_op,COALESCE(busy_until > NOW(),FALSE) FROM ai_games WHERE id=$1 AND user_id=$2 FOR UPDATE`, id, user).Scan(&soup.Title, &soup.Surface, &soup.Answer, &status, &busyOp, &leaseActive)
	if errors.Is(err, pgx.ErrNoRows) {
		return reservation{}, Decision{}, false, ErrNotFound
	}
	if err != nil {
		return reservation{}, Decision{}, false, err
	}
	var prior Decision
	var priorKind, priorBody string
	err = tx.QueryRow(ctx, `SELECT kind,body,result,model,latency_ms,prompt_tokens,completion_tokens,prompt_version FROM ai_events WHERE game_id=$1 AND operation_id=$2`, id, operationID).Scan(&priorKind, &priorBody, &prior.Result, &prior.Model, &prior.LatencyMS, &prior.PromptTokens, &prior.CompletionTokens, &prior.PromptVersion)
	if err == nil {
		if priorKind != kind || priorBody != body {
			return reservation{}, Decision{}, false, ErrOperationMismatch
		}
		return reservation{}, prior, true, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return reservation{}, Decision{}, false, err
	}
	if status != "active" {
		return reservation{}, Decision{}, false, ErrFinished
	}
	if busyOp != nil && leaseActive {
		return reservation{}, Decision{}, false, ErrBusy
	}
	var count int
	if kind != "reveal" {
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM ai_events WHERE game_id=$1`, id).Scan(&count); err != nil {
			return reservation{}, Decision{}, false, err
		}
		if count >= 100 {
			return reservation{}, Decision{}, false, ErrLimit
		}
		var calls int
		// 跨账号共用当天额度；冲突更新会锁住同一天的计数行。
		err := tx.QueryRow(ctx, `INSERT INTO ai_global_usage(usage_day,calls)
			VALUES(CURRENT_DATE,1)
			ON CONFLICT (usage_day) DO UPDATE SET calls=ai_global_usage.calls+1
			WHERE ai_global_usage.calls < $1 RETURNING calls`, globalDailyLimit).Scan(&calls)
		if errors.Is(err, pgx.ErrNoRows) {
			return reservation{}, Decision{}, false, ErrDailyLimit
		}
		if err != nil {
			return reservation{}, Decision{}, false, err
		}
		err = tx.QueryRow(ctx, `INSERT INTO ai_daily_usage(user_id,usage_day,calls)
			VALUES($1,CURRENT_DATE,1)
			ON CONFLICT (user_id,usage_day) DO UPDATE SET calls=ai_daily_usage.calls+1
			WHERE ai_daily_usage.calls < 120 RETURNING calls`, user).Scan(&calls)
		if errors.Is(err, pgx.ErrNoRows) {
			return reservation{}, Decision{}, false, ErrDailyLimit
		}
		if err != nil {
			return reservation{}, Decision{}, false, err
		}
	}
	_, err = tx.Exec(ctx, `UPDATE ai_games SET busy_op=$2,busy_until=NOW()+INTERVAL '90 seconds' WHERE id=$1`, id, operationID)
	if err != nil {
		return reservation{}, Decision{}, false, err
	}
	return reservation{Soup: soup}, Decision{}, false, tx.Commit(ctx)
}

func (s *Store) History(ctx context.Context, gameID int64) ([]Exchange, error) {
	rows, err := s.db.Query(ctx, `SELECT body,result FROM ai_events WHERE game_id=$1 AND kind='question' ORDER BY id DESC LIMIT 12`, gameID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	history := make([]Exchange, 0, 12)
	for rows.Next() {
		var item Exchange
		if err := rows.Scan(&item.Question, &item.Answer); err != nil {
			return nil, err
		}
		history = append(history, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i, j := 0, len(history)-1; i < j; i, j = i+1, j-1 {
		history[i], history[j] = history[j], history[i]
	}
	return history, nil
}

// Complete 验证租约仍属于当前操作后提交结果；答对或主动揭底会结束对局。
func (s *Store) Complete(ctx context.Context, user, id int64, operationID, kind, body string, decision Decision) (string, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)
	var soupID int64
	var answer, status string
	var busyOp *string
	err = tx.QueryRow(ctx, `SELECT soup_id,answer,status,busy_op FROM ai_games WHERE id=$1 AND user_id=$2 FOR UPDATE`, id, user).Scan(&soupID, &answer, &status, &busyOp)
	if err != nil {
		return "", err
	}
	if status != "active" || busyOp == nil || *busyOp != operationID {
		return "", ErrBusy
	}
	_, err = tx.Exec(ctx, `INSERT INTO ai_events(game_id,operation_id,kind,body,result,model,prompt_version,latency_ms,prompt_tokens,completion_tokens) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)`, id, operationID, kind, body, decision.Result, decision.Model, PromptVersion, decision.LatencyMS, decision.PromptTokens, decision.CompletionTokens)
	if err != nil {
		return "", err
	}
	finished := kind == "reveal" || kind == "restore" && decision.Result == "正确"
	if finished {
		_, err = tx.Exec(ctx, `UPDATE ai_games SET status='finished',busy_op=NULL,busy_until=NULL,finished_at=NOW() WHERE id=$1`, id)
		if err == nil {
			_, err = tx.Exec(ctx, `INSERT INTO soup_reveals(soup_id,user_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, soupID, user)
		}
	} else {
		_, err = tx.Exec(ctx, `UPDATE ai_games SET busy_op=NULL,busy_until=NULL WHERE id=$1`, id)
	}
	if err != nil {
		return "", err
	}
	if err := tx.Commit(ctx); err != nil {
		return "", err
	}
	if finished {
		return answer, nil
	}
	return "", nil
}

func (s *Store) Release(ctx context.Context, user, id int64, operationID string) {
	_, _ = s.db.Exec(ctx, `UPDATE ai_games SET busy_op=NULL,busy_until=NULL WHERE id=$1 AND user_id=$2 AND busy_op=$3`, id, user, operationID)
}

// RecordAttempt 只记录调用指标，不写入模型请求和响应内容。
func (s *Store) RecordAttempt(ctx context.Context, gameID int64, kind, model string, elapsed float64, err error) {
	errorKind := ""
	if errors.Is(err, ErrInvalidOutput) {
		errorKind = "invalid_output"
	} else if err != nil {
		errorKind = "provider_error"
	}
	_, _ = s.db.Exec(ctx, `INSERT INTO ai_attempts(game_id,kind,model,prompt_version,latency_ms,success,error_kind) VALUES($1,$2,$3,$4,$5,$6,$7)`, gameID, kind, model, PromptVersion, elapsed, err == nil, errorKind)
}
