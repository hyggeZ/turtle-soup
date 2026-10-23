package account

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("记录不存在")
var ErrUsernameTaken = errors.New("用户名已存在")

type User struct {
	ID           int64  `json:"id"`
	Username     string `json:"username"`
	PasswordHash string `json:"-"`
}

// Store 只列出登录流程需要的数据库操作，当前只有 PostgreSQL 实现。
type Store interface {
	CreateUser(context.Context, string, string) (User, error)
	FindUser(context.Context, string) (User, error)
	CreateSession(context.Context, int64, []byte, time.Time) error
	FindSessionUser(context.Context, []byte) (User, error)
	DeleteSession(context.Context, []byte) error
}

type PGStore struct {
	db *pgxpool.Pool
}

func NewPGStore(db *pgxpool.Pool) *PGStore {
	return &PGStore{db: db}
}

func (s *PGStore) CreateUser(ctx context.Context, name, hash string) (User, error) {
	var user User
	err := s.db.QueryRow(ctx,
		`INSERT INTO users (username, password_hash) VALUES ($1, $2) RETURNING id, username`,
		name, hash,
	).Scan(&user.ID, &user.Username)
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "23505" {
		return User{}, ErrUsernameTaken
	}
	return user, err
}

func (s *PGStore) FindUser(ctx context.Context, name string) (User, error) {
	var user User
	err := s.db.QueryRow(ctx,
		`SELECT id, username, password_hash FROM users WHERE username = $1`, name,
	).Scan(&user.ID, &user.Username, &user.PasswordHash)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return user, err
}

func (s *PGStore) CreateSession(ctx context.Context, userID int64, hash []byte, expires time.Time) error {
	_, err := s.db.Exec(ctx,
		`INSERT INTO sessions (token_hash, user_id, expires_at) VALUES ($1, $2, $3)`,
		hash, userID, expires,
	)
	return err
}

func (s *PGStore) FindSessionUser(ctx context.Context, hash []byte) (User, error) {
	var user User
	err := s.db.QueryRow(ctx, `
		SELECT u.id, u.username
		FROM sessions AS s JOIN users AS u ON u.id = s.user_id
		WHERE s.token_hash = $1 AND s.expires_at > NOW()
	`, hash).Scan(&user.ID, &user.Username)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, ErrNotFound
	}
	return user, err
}

func (s *PGStore) DeleteSession(ctx context.Context, hash []byte) error {
	_, err := s.db.Exec(ctx, `DELETE FROM sessions WHERE token_hash = $1`, hash)
	return err
}
