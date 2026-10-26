// Package forum 保存汤题、评论、点赞收藏和用户关注的数据。
package forum

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("记录不存在")

type Soup struct {
	ID         int64     `json:"id"`
	AuthorID   int64     `json:"authorId"`
	Author     string    `json:"author"`
	Title      string    `json:"title"`
	Surface    string    `json:"surface"`
	Genre      string    `json:"genre"`
	Difficulty int       `json:"difficulty"`
	Horror     int       `json:"horror"`
	Likes      int       `json:"likes"`
	Bookmarks  int       `json:"bookmarks"`
	Comments   int       `json:"comments"`
	Liked      bool      `json:"liked"`
	Bookmarked bool      `json:"bookmarked"`
	CreatedAt  time.Time `json:"createdAt"`
}

type Comment struct {
	ID        int64     `json:"id"`
	AuthorID  int64     `json:"authorId"`
	Author    string    `json:"author"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"createdAt"`
}

type Profile struct {
	ID        int64  `json:"id"`
	Username  string `json:"username"`
	Followers int    `json:"followers"`
	Following int    `json:"following"`
	Followed  bool   `json:"followed"`
}

type Store struct{ db *pgxpool.Pool }

func NewStore(db *pgxpool.Pool) *Store { return &Store{db: db} }

// 列表只查询汤面；汤底必须经过单独的接口读取。
const soupFields = `s.id, s.author_id, u.username, s.title, s.surface, s.genre,
	s.difficulty, s.horror, s.created_at,
	(SELECT COUNT(*) FROM soup_likes WHERE soup_id = s.id),
	(SELECT COUNT(*) FROM soup_bookmarks WHERE soup_id = s.id),
	(SELECT COUNT(*) FROM comments WHERE soup_id = s.id),
	EXISTS(SELECT 1 FROM soup_likes WHERE soup_id = s.id AND user_id = $1),
	EXISTS(SELECT 1 FROM soup_bookmarks WHERE soup_id = s.id AND user_id = $1)`

func scanSoup(row pgx.Row) (Soup, error) {
	var soup Soup
	err := row.Scan(&soup.ID, &soup.AuthorID, &soup.Author, &soup.Title, &soup.Surface,
		&soup.Genre, &soup.Difficulty, &soup.Horror, &soup.CreatedAt,
		&soup.Likes, &soup.Bookmarks, &soup.Comments, &soup.Liked, &soup.Bookmarked)
	if errors.Is(err, pgx.ErrNoRows) {
		return Soup{}, ErrNotFound
	}
	return soup, err
}

func (s *Store) List(ctx context.Context, viewer int64, author int64, limit, offset int) ([]Soup, error) {
	query := `SELECT ` + soupFields + ` FROM soups s JOIN users u ON u.id = s.author_id
		WHERE ($2 = 0 OR s.author_id = $2) ORDER BY s.created_at DESC, s.id DESC LIMIT $3 OFFSET $4`
	rows, err := s.db.Query(ctx, query, viewer, author, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Soup, 0)
	for rows.Next() {
		item, err := scanSoup(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) Get(ctx context.Context, viewer, id int64) (Soup, error) {
	return scanSoup(s.db.QueryRow(ctx, `SELECT `+soupFields+` FROM soups s JOIN users u ON u.id=s.author_id WHERE s.id=$2`, viewer, id))
}

func (s *Store) Create(ctx context.Context, author int64, title, surface, answer, genre string, difficulty, horror int) (int64, error) {
	var id int64
	err := s.db.QueryRow(ctx, `INSERT INTO soups(author_id,title,surface,answer,genre,difficulty,horror)
		VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id`, author, title, surface, answer, genre, difficulty, horror).Scan(&id)
	return id, err
}

func (s *Store) Answer(ctx context.Context, id int64) (string, error) {
	var answer string
	err := s.db.QueryRow(ctx, `SELECT answer FROM soups WHERE id=$1`, id).Scan(&answer)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return answer, err
}

func (s *Store) SetRelation(ctx context.Context, table string, user, target int64, add bool) error {
	// table 只能由处理器中的固定枚举传入，不能来自请求参数。
	if table != "soup_likes" && table != "soup_bookmarks" {
		return errors.New("无效的关系表")
	}
	query := `DELETE FROM ` + table + ` WHERE user_id=$1 AND soup_id=$2`
	if add {
		query = `INSERT INTO ` + table + `(user_id,soup_id) VALUES($1,$2) ON CONFLICT DO NOTHING`
	}
	_, err := s.db.Exec(ctx, query, user, target)
	return err
}

func (s *Store) ListComments(ctx context.Context, soup int64) ([]Comment, error) {
	rows, err := s.db.Query(ctx, `SELECT c.id,c.author_id,u.username,c.body,c.created_at
		FROM comments c JOIN users u ON u.id=c.author_id WHERE c.soup_id=$1 ORDER BY c.created_at,c.id LIMIT 100`, soup)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Comment, 0)
	for rows.Next() {
		var item Comment
		if err := rows.Scan(&item.ID, &item.AuthorID, &item.Author, &item.Body, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) AddComment(ctx context.Context, soup, author int64, body string) error {
	_, err := s.db.Exec(ctx, `INSERT INTO comments(soup_id,author_id,body) VALUES($1,$2,$3)`, soup, author, body)
	return err
}

func (s *Store) GetProfile(ctx context.Context, viewer, id int64) (Profile, error) {
	var item Profile
	err := s.db.QueryRow(ctx, `SELECT u.id,u.username,
		(SELECT COUNT(*) FROM follows WHERE followed_id=u.id),
		(SELECT COUNT(*) FROM follows WHERE follower_id=u.id),
		EXISTS(SELECT 1 FROM follows WHERE follower_id=$1 AND followed_id=u.id)
		FROM users u WHERE u.id=$2`, viewer, id).Scan(&item.ID, &item.Username, &item.Followers, &item.Following, &item.Followed)
	if errors.Is(err, pgx.ErrNoRows) {
		return Profile{}, ErrNotFound
	}
	return item, err
}

func (s *Store) SetFollow(ctx context.Context, viewer, id int64, add bool) error {
	query := `DELETE FROM follows WHERE follower_id=$1 AND followed_id=$2`
	if add {
		query = `INSERT INTO follows(follower_id,followed_id) VALUES($1,$2) ON CONFLICT DO NOTHING`
	}
	_, err := s.db.Exec(ctx, query, viewer, id)
	return err
}
