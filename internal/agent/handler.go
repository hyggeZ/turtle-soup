package agent

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"turtle-soup/internal/account"
	"turtle-soup/internal/telemetry"
)

type Handler struct {
	store   *Store
	auth    *account.Handler
	decider Decider
}

func NewHandler(store *Store, auth *account.Handler, decider Decider) *Handler {
	return &Handler{store: store, auth: auth, decider: decider}
}

func (h *Handler) Register(router *gin.Engine) {
	router.POST("/api/ai/solo", h.create)
	router.GET("/api/ai/solo/mine", h.listMine)
	router.GET("/api/ai/solo/:id", h.get)
	router.POST("/api/ai/solo/:id/actions", h.action)
}

// 单人局找回入口只列出当前账号的有限概要，详情仍走按 ID 鉴权接口。
func (h *Handler) listMine(c *gin.Context) {
	user, ok := h.user(c)
	if !ok {
		return
	}
	items, err := h.store.ListMine(c.Request.Context(), user)
	if err != nil {
		h.replyError(c, err)
		return
	}
	c.JSON(http.StatusOK, items)
}

func (h *Handler) user(c *gin.Context) (int64, bool) {
	user, err := h.auth.CurrentUser(c)
	if err == nil {
		return user.ID, true
	}
	if errors.Is(err, account.ErrNotFound) {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "请先登录"})
	} else {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "读取登录状态失败"})
	}
	return 0, false
}

func (h *Handler) ready() bool {
	if h.decider == nil {
		return false
	}
	if qwen, ok := h.decider.(*Qwen); ok {
		return qwen.Ready()
	}
	return true
}

func (h *Handler) create(c *gin.Context) {
	user, ok := h.user(c)
	if !ok {
		return
	}
	if !h.ready() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "AI 主持尚未配置"})
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 1024)
	var input struct {
		SoupID int64 `json:"soupId"`
	}
	if c.ShouldBindJSON(&input) != nil || input.SoupID < 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请选择题目"})
		return
	}
	id, err := h.store.Create(c.Request.Context(), user, input.SoupID)
	if err != nil {
		h.replyError(c, err)
		return
	}
	c.JSON(http.StatusCreated, gin.H{"id": id})
}

func gameID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id < 1 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "对局 ID 无效"})
		return 0, false
	}
	return id, true
}

func (h *Handler) get(c *gin.Context) {
	user, ok := h.user(c)
	if !ok {
		return
	}
	id, ok := gameID(c)
	if !ok {
		return
	}
	game, err := h.store.Get(c.Request.Context(), user, id)
	if err != nil {
		h.replyError(c, err)
		return
	}
	c.JSON(http.StatusOK, game)
}

type actionInput struct {
	Type        string `json:"type"`
	Body        string `json:"body"`
	OperationID string `json:"operationId"`
}

func validAction(input actionInput) bool {
	if len(input.OperationID) < 8 || len(input.OperationID) > 100 {
		return false
	}
	for _, char := range input.OperationID {
		letter := char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z'
		digit := char >= '0' && char <= '9'
		if !letter && !digit && char != '-' && char != '_' {
			return false
		}
	}
	size := len([]rune(input.Body))
	switch input.Type {
	case "question":
		return size >= 1 && size <= 500
	case "restore":
		return size >= 1 && size <= 2000
	case "reveal":
		return size == 0
	default:
		return false
	}
}

func (h *Handler) action(c *gin.Context) {
	user, ok := h.user(c)
	if !ok {
		return
	}
	id, ok := gameID(c)
	if !ok {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 4<<10)
	var input actionInput
	if c.ShouldBindJSON(&input) != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "操作内容无效"})
		return
	}
	input.Body = strings.TrimSpace(input.Body)
	if !validAction(input) {
		c.JSON(http.StatusBadRequest, gin.H{"error": "请检查操作类型和内容长度"})
		return
	}
	if input.Type != "reveal" && !h.ready() {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "AI 主持尚未配置"})
		return
	}
	claim, decision, duplicate, err := h.store.Claim(c.Request.Context(), user, id, input.OperationID, input.Type, input.Body)
	if err != nil {
		h.replyError(c, err)
		return
	}
	if duplicate {
		response := gin.H{"ok": true, "duplicate": true, "result": decision.Result, "model": decision.Model, "latencyMs": decision.LatencyMS, "promptTokens": decision.PromptTokens, "completionTokens": decision.CompletionTokens, "promptVersion": decision.PromptVersion}
		if input.Type == "reveal" || input.Type == "restore" && decision.Result == "正确" {
			if game, err := h.store.Get(c.Request.Context(), user, id); err == nil {
				response["answer"] = game.Answer
			}
		}
		c.JSON(http.StatusOK, response)
		return
	}

	if input.Type == "reveal" {
		decision.Result = "已查看汤底"
	} else {
		started := time.Now()
		endDecision := telemetry.Begin(c.Request.Context(), "ai.decision")
		decision, err = h.decide(c.Request.Context(), id, claim.Soup, input)
		if err == nil && !validDecision(input.Type, decision.Result) {
			err = ErrInvalidOutput
		}
		endDecision()
		model := decision.Model
		if qwen, ok := h.decider.(*Qwen); ok && model == "" {
			model = qwen.Model
		}
		metricCtx, stop := context.WithTimeout(context.Background(), 3*time.Second)
		h.store.RecordAttempt(metricCtx, id, input.Type, model, float64(time.Since(started))/float64(time.Millisecond), err)
		stop()
	}
	if err != nil {
		h.release(user, id, input.OperationID)
		h.replyError(c, err)
		return
	}
	// 客户端中途断开时也要提交已完成的结果，让同一操作 ID 的重试可复用。
	commitCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	answer, err := h.store.Complete(commitCtx, user, id, input.OperationID, input.Type, input.Body, decision)
	if err != nil {
		h.release(user, id, input.OperationID)
		h.replyError(c, err)
		return
	}
	response := gin.H{"ok": true, "result": decision.Result, "model": decision.Model, "latencyMs": decision.LatencyMS, "promptTokens": decision.PromptTokens, "completionTokens": decision.CompletionTokens, "promptVersion": PromptVersion}
	if answer != "" {
		response["answer"] = answer
	}
	c.JSON(http.StatusOK, response)
}

func validDecision(kind, result string) bool {
	if kind == "question" {
		return oneOf(result, "是", "否", "无关")
	}
	return kind == "restore" && oneOf(result, "正确", "错误", "不完整")
}

func (h *Handler) decide(ctx context.Context, id int64, soup Soup, input actionInput) (Decision, error) {
	modelCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	history, err := h.store.History(modelCtx, id)
	if err != nil {
		return Decision{}, err
	}
	if input.Type == "question" {
		return h.decider.DecideQuestion(modelCtx, soup, history, input.Body)
	}
	return h.decider.JudgeRestore(modelCtx, soup, history, input.Body)
}

func (h *Handler) release(user, id int64, operationID string) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	h.store.Release(ctx, user, id, operationID)
}

func (h *Handler) replyError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, ErrNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "题目或对局不存在"})
	case errors.Is(err, ErrKnownAnswer), errors.Is(err, ErrPlaying), errors.Is(err, ErrBusy), errors.Is(err, ErrFinished), errors.Is(err, ErrLimit), errors.Is(err, ErrActiveGamesLimit), errors.Is(err, ErrOperationMismatch):
		c.JSON(http.StatusConflict, gin.H{"error": err.Error()})
	case errors.Is(err, ErrDailyLimit):
		c.JSON(http.StatusTooManyRequests, gin.H{"error": err.Error()})
	case errors.Is(err, ErrUnavailable):
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "AI 主持暂时不可用，请稍后重试"})
	case errors.Is(err, ErrInvalidOutput):
		c.JSON(http.StatusBadGateway, gin.H{"error": "AI 判定格式无效，请重试"})
	default:
		c.JSON(http.StatusInternalServerError, gin.H{"error": "暂时无法处理请求"})
	}
}
