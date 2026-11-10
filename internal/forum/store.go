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
var ErrPlaying = errors.New("正在参与该题对局")
var ErrForbidden = errors.New("只有题目作者可以管理该题")
var ErrActiveGame = errors.New("这道题仍有进行中的对局，暂时不能编辑")

type Soup struct {
	ID         int64     `json:"id"`
	AuthorID   int64     `json:"authorId"`
	Author     string    `json:"author"`
	Title      string    `json:"title"`
	Surface    string    `json:"surface"`
	Answer     string    `json:"answer,omitempty"`
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
	Spoiler   bool      `json:"spoiler"`
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

const soupDetailFields = soupFields + `,CASE WHEN s.author_id=$1 THEN s.answer ELSE '' END`

func scanSoup(row pgx.Row, detail bool) (Soup, error) {
	var soup Soup
	fields := []any{&soup.ID, &soup.AuthorID, &soup.Author, &soup.Title, &soup.Surface,
		&soup.Genre, &soup.Difficulty, &soup.Horror, &soup.CreatedAt,
		&soup.Likes, &soup.Bookmarks, &soup.Comments, &soup.Liked, &soup.Bookmarked}
	if detail {
		fields = append(fields, &soup.Answer)
	}
	err := row.Scan(fields...)
	if errors.Is(err, pgx.ErrNoRows) {
		return Soup{}, ErrNotFound
	}
	return soup, err
}

func (s *Store) List(ctx context.Context, viewer int64, author int64, limit, offset int) ([]Soup, error) {
	query := `SELECT ` + soupFields + ` FROM soups s JOIN users u ON u.id = s.author_id
		WHERE s.deleted_at IS NULL AND ($2 = 0 OR s.author_id = $2) ORDER BY s.created_at DESC, s.id DESC LIMIT $3 OFFSET $4`
	rows, err := s.db.Query(ctx, query, viewer, author, limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Soup, 0)
	for rows.Next() {
		item, err := scanSoup(rows, false)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) Get(ctx context.Context, viewer, id int64) (Soup, error) {
	return scanSoup(s.db.QueryRow(ctx, `SELECT `+soupDetailFields+` FROM soups s JOIN users u ON u.id=s.author_id WHERE s.id=$2 AND s.deleted_at IS NULL`, viewer, id), true)
}

func (s *Store) Create(ctx context.Context, author int64, title, surface, answer, genre string, difficulty, horror int) (int64, error) {
	var id int64
	err := s.db.QueryRow(ctx, `INSERT INTO soups(author_id,title,surface,answer,genre,difficulty,horror)
		VALUES($1,$2,$3,$4,$5,$6,$7) RETURNING id`, author, title, surface, answer, genre, difficulty, horror).Scan(&id)
	return id, err
}

// Update 锁定题目并检查作者；活跃对局期间不改题目正文。
func (s *Store) Update(ctx context.Context, user, id int64, title, surface, answer, genre string, difficulty, horror int) (Soup, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return Soup{}, err
	}
	defer tx.Rollback(ctx)
	var author int64
	err = tx.QueryRow(ctx, `SELECT author_id FROM soups WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, id).Scan(&author)
	if errors.Is(err, pgx.ErrNoRows) {
		return Soup{}, ErrNotFound
	}
	if err != nil {
		return Soup{}, err
	}
	if author != user {
		return Soup{}, ErrForbidden
	}
	var active bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM rooms WHERE soup_id=$1 AND status IN ('waiting','active','paused'))
		OR EXISTS(SELECT 1 FROM ai_games WHERE soup_id=$1 AND status='active')`, id).Scan(&active)
	if err != nil {
		return Soup{}, err
	}
	if active {
		return Soup{}, ErrActiveGame
	}
	_, err = tx.Exec(ctx, `UPDATE soups SET title=$2,surface=$3,answer=$4,genre=$5,difficulty=$6,horror=$7,updated_at=NOW() WHERE id=$1`, id, title, surface, answer, genre, difficulty, horror)
	if err != nil {
		return Soup{}, err
	}
	item, err := scanSoup(tx.QueryRow(ctx, `SELECT `+soupDetailFields+` FROM soups s JOIN users u ON u.id=s.author_id WHERE s.id=$2`, user, id), true)
	if err != nil {
		return Soup{}, err
	}
	return item, tx.Commit(ctx)
}

// Delete 只标记删除，已创建的真人房间和 AI 单人局继续使用各自的题目快照。
func (s *Store) Delete(ctx context.Context, user, id int64) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var author int64
	err = tx.QueryRow(ctx, `SELECT author_id FROM soups WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, id).Scan(&author)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if author != user {
		return ErrForbidden
	}
	if _, err := tx.Exec(ctx, `UPDATE soups SET deleted_at=NOW(),updated_at=NOW() WHERE id=$1`, id); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

// RevealAnswer 与加入房间共用题目行锁，避免“看汤底”和“入场”并发穿过检查。
func (s *Store) RevealAnswer(ctx context.Context, user, id int64) (string, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return "", err
	}
	defer tx.Rollback(ctx)

	var answer string
	err = tx.QueryRow(ctx, `SELECT answer FROM soups WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, id).Scan(&answer)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	var playing bool
	err = tx.QueryRow(ctx, `SELECT EXISTS(
		SELECT 1 FROM rooms r JOIN room_members m ON m.room_id=r.id
		WHERE r.soup_id=$1 AND r.status IN ('waiting','active','paused')
		AND m.user_id=$2 AND m.role='player'
	) OR EXISTS(
		SELECT 1 FROM ai_games WHERE soup_id=$1 AND user_id=$2 AND status='active'
	)`, id, user).Scan(&playing)
	if err != nil {
		return "", err
	}
	if playing {
		return "", ErrPlaying
	}
	_, err = tx.Exec(ctx, `INSERT INTO soup_reveals(soup_id,user_id) VALUES($1,$2) ON CONFLICT DO NOTHING`, id, user)
	if err != nil {
		return "", err
	}
	return answer, tx.Commit(ctx)
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
	rows, err := s.db.Query(ctx, `SELECT c.id,c.author_id,u.username,
		CASE WHEN c.spoiler THEN '' ELSE c.body END,c.spoiler,c.created_at
		FROM comments c JOIN users u ON u.id=c.author_id WHERE c.soup_id=$1 ORDER BY c.created_at,c.id LIMIT 100`, soup)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Comment, 0)
	for rows.Next() {
		var item Comment
		if err := rows.Scan(&item.ID, &item.AuthorID, &item.Author, &item.Body, &item.Spoiler, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) AddComment(ctx context.Context, soup, author int64, body string, spoiler bool) error {
	_, err := s.db.Exec(ctx, `INSERT INTO comments(soup_id,author_id,body,spoiler) VALUES($1,$2,$3,$4)`, soup, author, body, spoiler)
	return err
}

// 剧透正文只由显式展开接口读取，普通评论列表始终留空。
func (s *Store) SpoilerComment(ctx context.Context, soup, comment int64) (string, error) {
	var body string
	err := s.db.QueryRow(ctx, `SELECT body FROM comments WHERE id=$1 AND soup_id=$2 AND spoiler=TRUE`, comment, soup).Scan(&body)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	return body, err
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
