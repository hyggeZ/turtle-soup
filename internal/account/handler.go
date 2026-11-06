// Package account 管理注册、登录与基于 Cookie 的会话。
package account

import (
	"errors"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

var usernamePattern = regexp.MustCompile(`^[a-z0-9_]{3,20}$`)

func ValidUsername(name string) bool { return usernamePattern.MatchString(name) }

type Handler struct {
	store  Store
	secure bool
}

type credentials struct {
	Username string `json:"username" binding:"required"`
	Password string `json:"password" binding:"required"`
}

func NewHandler(store Store, secureCookie bool) *Handler {
	return &Handler{store: store, secure: secureCookie}
}

func (h *Handler) Register(router *gin.Engine) {
	router.POST("/api/register", h.register)
	router.POST("/api/login", h.login)
	router.POST("/api/admin/login", h.adminLogin)
	router.GET("/api/me", h.me)
	router.POST("/api/logout", h.logout)
}

// CurrentUser 读取请求中的会话，供其他业务模块判断登录状态。
func (h *Handler) CurrentUser(ctx *gin.Context) (User, error) {
	hash, err := tokenHash(ctx.Request)
	if err != nil {
		return User{}, ErrNotFound
	}
	user, err := h.store.FindSessionUser(ctx.Request.Context(), hash)
	if err == nil && user.Disabled {
		return User{}, ErrNotFound
	}
	return user, err
}

func (h *Handler) register(ctx *gin.Context) {
	ctx.Request.Body = http.MaxBytesReader(ctx.Writer, ctx.Request.Body, 4<<10)
	var input credentials
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "请填写用户名和密码"})
		return
	}
	name := strings.ToLower(strings.TrimSpace(input.Username))
	if !ValidUsername(name) {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "用户名需为 3–20 位字母、数字或下划线"})
		return
	}
	if len(input.Password) < 8 || len([]byte(input.Password)) > 72 {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "密码需为 8–72 字节"})
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(input.Password), bcrypt.DefaultCost)
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "注册失败"})
		return
	}
	user, err := h.store.CreateUser(ctx.Request.Context(), name, string(hash))
	if errors.Is(err, ErrUsernameTaken) {
		ctx.JSON(http.StatusConflict, gin.H{"error": "用户名已存在"})
		return
	}
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "注册失败"})
		return
	}
	ctx.JSON(http.StatusCreated, user)
}

func (h *Handler) login(ctx *gin.Context) {
	h.loginWithRole(ctx, false)
}

// 后台登录使用相同的会话，但会在创建会话前检查角色。
func (h *Handler) adminLogin(ctx *gin.Context) {
	h.loginWithRole(ctx, true)
}

func (h *Handler) loginWithRole(ctx *gin.Context, adminOnly bool) {
	ctx.Request.Body = http.MaxBytesReader(ctx.Writer, ctx.Request.Body, 4<<10)
	var input credentials
	if err := ctx.ShouldBindJSON(&input); err != nil {
		ctx.JSON(http.StatusBadRequest, gin.H{"error": "请填写用户名和密码"})
		return
	}
	name := strings.ToLower(strings.TrimSpace(input.Username))
	user, err := h.store.FindUser(ctx.Request.Context(), name)
	if errors.Is(err, ErrNotFound) {
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": "用户名或密码错误"})
		return
	}
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "登录失败"})
		return
	}
	if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(input.Password)) != nil {
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": "用户名或密码错误"})
		return
	}
	if user.Disabled {
		ctx.JSON(http.StatusForbidden, gin.H{"error": "账号已停用"})
		return
	}
	if adminOnly && !user.IsAdmin {
		ctx.JSON(http.StatusForbidden, gin.H{"error": "无后台登录权限"})
		return
	}

	token, hash, err := newToken()
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "登录失败"})
		return
	}
	expires := time.Now().Add(sessionDays * 24 * time.Hour)
	if err := h.store.CreateSession(ctx.Request.Context(), user.ID, hash, expires); err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "登录失败"})
		return
	}
	setCookie(ctx.Writer, token, h.secure)
	ctx.JSON(http.StatusOK, user)
}

func (h *Handler) me(ctx *gin.Context) {
	hash, err := tokenHash(ctx.Request)
	if err != nil {
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": "请先登录"})
		return
	}
	user, err := h.store.FindSessionUser(ctx.Request.Context(), hash)
	if errors.Is(err, ErrNotFound) {
		ctx.JSON(http.StatusUnauthorized, gin.H{"error": "登录已过期"})
		return
	}
	if err != nil {
		ctx.JSON(http.StatusInternalServerError, gin.H{"error": "读取用户失败"})
		return
	}
	if user.Disabled {
		ctx.JSON(http.StatusForbidden, gin.H{"error": "账号已停用"})
		return
	}
	ctx.JSON(http.StatusOK, user)
}

func (h *Handler) logout(ctx *gin.Context) {
	hash, err := tokenHash(ctx.Request)
	if err == nil {
		if err := h.store.DeleteSession(ctx.Request.Context(), hash); err != nil {
			ctx.JSON(http.StatusInternalServerError, gin.H{"error": "退出失败"})
			return
		}
	}
	clearCookie(ctx.Writer, h.secure)
	ctx.Status(http.StatusNoContent)
}
