// Package forum 提供汤题广场和用户互动接口。
package forum

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"turtle-soup/internal/account"
)

type Handler struct {
	store *Store
	auth  *account.Handler
}

func NewHandler(store *Store, auth *account.Handler) *Handler {
	return &Handler{store: store, auth: auth}
}

// Register 将论坛接口集中注册，房间玩法以后可作为独立模块加入。
func (h *Handler) Register(router *gin.Engine) {
	router.GET("/api/soups", h.list)
	router.GET("/api/soups/:id", h.get)
	router.POST("/api/soups", h.create)
	router.GET("/api/soups/:id/answer", h.answer)
	router.PUT("/api/soups/:id/like", h.like)
	router.DELETE("/api/soups/:id/like", h.unlike)
	router.PUT("/api/soups/:id/bookmark", h.bookmark)
	router.DELETE("/api/soups/:id/bookmark", h.unbookmark)
	router.GET("/api/soups/:id/comments", h.comments)
	router.POST("/api/soups/:id/comments", h.addComment)
	router.GET("/api/users/:id", h.profile)
	router.PUT("/api/users/:id/follow", h.follow)
	router.DELETE("/api/users/:id/follow", h.unfollow)
}

func (h *Handler) viewer(ctx *gin.Context) int64 {
	user, err := h.auth.CurrentUser(ctx)
	if err != nil {
		return 0
	}
	return user.ID
}

func (h *Handler) requiredUser(ctx *gin.Context) (int64, bool) {
	user, err := h.auth.CurrentUser(ctx)
	if err == nil {
		return user.ID, true
	}
	if errors.Is(err, account.ErrNotFound) {
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": "请先登录"})
	} else {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "读取登录状态失败"})
	}
	return 0, false
}

func idParam(ctx *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(ctx.Param("id"), 10, 64)
	if err != nil || id < 1 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "无效的 ID"})
		return 0, false
	}
	return id, true
}

func replyError(ctx *gin.Context, err error) {
	if errors.Is(err, ErrNotFound) {
		ctx.JSON(http.StatusNotFound, gin.H{"error": "内容不存在"})
		return
	}
	ctx.JSON(http.StatusInternalServerError, gin.H{"error": "服务器暂时无法处理请求"})
}

func (h *Handler) list(ctx *gin.Context) {
	page, _ := strconv.Atoi(ctx.DefaultQuery("page", "1"))
	if page < 1 || page > 10000 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "页码无效"})
		return
	}
	author := int64(0)
	if raw := ctx.Query("author"); raw != "" {
		var err error
		author, err = strconv.ParseInt(raw, 10, 64)
		if err != nil || author < 1 {
			ctx.JSON(http.StatusBadRequest, gin.H{"error": "作者 ID 无效"})
			return
		}
	}
	items, err := h.store.List(ctx.Request.Context(), h.viewer(ctx), author, 20, (page-1)*20)
	if err != nil {
		replyError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, items)
}

func (h *Handler) get(ctx *gin.Context) {
	id, ok := idParam(ctx)
	if !ok {
		return
	}
	item, err := h.store.Get(ctx.Request.Context(), h.viewer(ctx), id)
	if err != nil {
		replyError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, item)
}

type soupInput struct {
	Title      string `json:"title"`
	Surface    string `json:"surface"`
	Answer     string `json:"answer"`
	Genre      string `json:"genre"`
	Difficulty int    `json:"difficulty"`
	Horror     int    `json:"horror"`
}

func (h *Handler) create(ctx *gin.Context) {
	user, ok := h.requiredUser(ctx)
	if !ok {
		return
	}
	ctx.Request.Body = http.MaxBytesReader(ctx.Writer, ctx.Request.Body, 16<<10)
	var input soupInput
	if ctx.ShouldBindJSON(&input) != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "请填写完整题目"})
		return
	}
	input.Title = strings.TrimSpace(input.Title)
	input.Surface = strings.TrimSpace(input.Surface)
	input.Answer = strings.TrimSpace(input.Answer)
	if len([]rune(input.Title)) < 2 || len([]rune(input.Title)) > 80 ||
		len([]rune(input.Surface)) < 5 || len([]rune(input.Surface)) > 2000 ||
		len([]rune(input.Answer)) < 5 || len([]rune(input.Answer)) > 5000 ||
		(input.Genre != "本格" && input.Genre != "变格") ||
		input.Difficulty < 1 || input.Difficulty > 5 || input.Horror < 0 || input.Horror > 5 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "请检查标题、汤面、汤底及分类评分"})
		return
	}
	id, err := h.store.Create(ctx.Request.Context(), user, input.Title, input.Surface, input.Answer, input.Genre, input.Difficulty, input.Horror)
	if err != nil {
		replyError(ctx, err)
		return
	}
	ctx.JSON(http.StatusCreated, gin.H{"id": id})
}

func (h *Handler) answer(ctx *gin.Context) {
	if _, ok := h.requiredUser(ctx); !ok {
		return
	}
	id, ok := idParam(ctx)
	if !ok {
		return
	}
	answer, err := h.store.Answer(ctx.Request.Context(), id)
	if err != nil {
		replyError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, gin.H{"answer": answer})
}

func (h *Handler) relation(ctx *gin.Context, table string, add bool) {
	user, ok := h.requiredUser(ctx)
	if !ok {
		return
	}
	id, ok := idParam(ctx)
	if !ok {
		return
	}
	if _, err := h.store.Get(ctx.Request.Context(), user, id); err != nil {
		replyError(ctx, err)
		return
	}
	if err := h.store.SetRelation(ctx.Request.Context(), table, user, id, add); err != nil {
		replyError(ctx, err)
		return
	}
	ctx.Status(http.StatusNoContent)
}
func (h *Handler) like(ctx *gin.Context)       { h.relation(ctx, "soup_likes", true) }
func (h *Handler) unlike(ctx *gin.Context)     { h.relation(ctx, "soup_likes", false) }
func (h *Handler) bookmark(ctx *gin.Context)   { h.relation(ctx, "soup_bookmarks", true) }
func (h *Handler) unbookmark(ctx *gin.Context) { h.relation(ctx, "soup_bookmarks", false) }

func (h *Handler) comments(ctx *gin.Context) {
	id, ok := idParam(ctx)
	if !ok {
		return
	}
	if _, err := h.store.Get(ctx.Request.Context(), h.viewer(ctx), id); err != nil {
		replyError(ctx, err)
		return
	}
	items, err := h.store.ListComments(ctx.Request.Context(), id)
	if err != nil {
		replyError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, items)
}

func (h *Handler) addComment(ctx *gin.Context) {
	user, ok := h.requiredUser(ctx)
	if !ok {
		return
	}
	id, ok := idParam(ctx)
	if !ok {
		return
	}
	ctx.Request.Body = http.MaxBytesReader(ctx.Writer, ctx.Request.Body, 4<<10)
	var input struct {
		Body string `json:"body"`
	}
	if ctx.ShouldBindJSON(&input) != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "评论内容不能为空"})
		return
	}
	input.Body = strings.TrimSpace(input.Body)
	if len([]rune(input.Body)) < 1 || len([]rune(input.Body)) > 500 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "评论须为 1–500 字"})
		return
	}
	if _, err := h.store.Get(ctx.Request.Context(), user, id); err != nil {
		replyError(ctx, err)
		return
	}
	if err := h.store.AddComment(ctx.Request.Context(), id, user, input.Body); err != nil {
		replyError(ctx, err)
		return
	}
	ctx.Status(http.StatusCreated)
}

func (h *Handler) profile(ctx *gin.Context) {
	id, ok := idParam(ctx)
	if !ok {
		return
	}
	item, err := h.store.GetProfile(ctx.Request.Context(), h.viewer(ctx), id)
	if err != nil {
		replyError(ctx, err)
		return
	}
	ctx.JSON(http.StatusOK, item)
}

func (h *Handler) setFollow(ctx *gin.Context, add bool) {
	user, ok := h.requiredUser(ctx)
	if !ok {
		return
	}
	id, ok := idParam(ctx)
	if !ok {
		return
	}
	if user == id {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "不能关注自己"})
		return
	}
	if _, err := h.store.GetProfile(ctx.Request.Context(), user, id); err != nil {
		replyError(ctx, err)
		return
	}
	if err := h.store.SetFollow(ctx.Request.Context(), user, id, add); err != nil {
		replyError(ctx, err)
		return
	}
	ctx.Status(http.StatusNoContent)
}
func (h *Handler) follow(ctx *gin.Context)   { h.setFollow(ctx, true) }
func (h *Handler) unfollow(ctx *gin.Context) { h.setFollow(ctx, false) }
