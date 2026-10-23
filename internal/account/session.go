package account

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"time"
)

const cookieName = "turtle_session"
const sessionDays = 7

func newToken() (string, []byte, error) {
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		return "", nil, err
	}
	token := base64.RawURLEncoding.EncodeToString(bytes)
	hash := sha256.Sum256([]byte(token))
	return token, hash[:], nil
}

func tokenHash(request *http.Request) ([]byte, error) {
	cookie, err := request.Cookie(cookieName)
	if err != nil {
		return nil, err
	}
	bytes, err := base64.RawURLEncoding.DecodeString(cookie.Value)
	if err != nil || len(bytes) != 32 {
		return nil, errors.New("无效的会话")
	}
	hash := sha256.Sum256([]byte(cookie.Value))
	return hash[:], nil
}

func setCookie(response http.ResponseWriter, token string, secure bool) {
	http.SetCookie(response, &http.Cookie{
		Name:     cookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   int((sessionDays * 24 * time.Hour).Seconds()),
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	})
}

func clearCookie(response http.ResponseWriter, secure bool) {
	http.SetCookie(response, &http.Cookie{
		Name:     cookieName,
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
	})
}
