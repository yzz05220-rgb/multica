package handler

// 颜趣本地补丁：用户名 + 密码登录。
// 测试阶段替代邮箱验证码；登录成功后沿用上游同一套 JWT 与 Cookie，
// 其余权限、工作区、成员逻辑完全不变。

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/multica-ai/multica/server/internal/auth"
	"github.com/multica-ai/multica/server/internal/logger"
)

const (
	yanquPBKDF2Iter     = 210000
	yanquMaxFailures    = 8
	yanquLockDuration   = 15 * time.Minute
	yanquMinPasswordLen = 8
)

type yanquPasswordLoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// YanquHashPassword 生成 pbkdf2-sha256$<iter>$<salt>$<hash> 格式。
func YanquHashPassword(password string) (string, error) {
	if len(password) < yanquMinPasswordLen {
		return "", fmt.Errorf("密码至少 %d 位", yanquMinPasswordLen)
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key, err := pbkdf2.Key(sha256.New, password, salt, yanquPBKDF2Iter, 32)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("pbkdf2-sha256$%d$%s$%s", yanquPBKDF2Iter,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

func yanquCheckPassword(stored, password string) bool {
	parts := strings.Split(stored, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" {
		return false
	}
	iter, err := strconv.Atoi(parts[1])
	if err != nil || iter < 10000 {
		return false
	}
	salt, err1 := base64.RawStdEncoding.DecodeString(parts[2])
	want, err2 := base64.RawStdEncoding.DecodeString(parts[3])
	if err1 != nil || err2 != nil {
		return false
	}
	got, err := pbkdf2.Key(sha256.New, password, salt, iter, len(want))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(got, want) == 1
}

// PasswordLogin 处理 POST /auth/password-login。
func (h *Handler) PasswordLogin(w http.ResponseWriter, r *http.Request) {
	var req yanquPasswordLoginRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "请求格式不正确")
		return
	}
	username := strings.ToLower(strings.TrimSpace(req.Username))
	if username == "" || req.Password == "" {
		writeError(w, http.StatusBadRequest, "请输入用户名和密码")
		return
	}
	ctx := r.Context()
	var (
		userID   pgtype.UUID
		hash     string
		disabled bool
		failures int
		locked   pgtype.Timestamptz
	)
	err := h.DB.QueryRow(ctx,
		`SELECT user_id, password_hash, disabled, failed_attempts, locked_until FROM yanqu_local_login WHERE username = $1`,
		username).Scan(&userID, &hash, &disabled, &failures, &locked)
	const badCredentials = "用户名或密码不正确"
	if errors.Is(err, pgx.ErrNoRows) {
		// 不存在的用户也做一次哈希，避免通过耗时判断用户名是否存在。
		_ = yanquCheckPassword("pbkdf2-sha256$210000$AAAAAAAAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA", req.Password)
		writeError(w, http.StatusUnauthorized, badCredentials)
		return
	}
	if err != nil {
		slog.Warn("yanqu password login lookup failed", append(logger.RequestAttrs(r), "error", err)...)
		writeError(w, http.StatusInternalServerError, "登录失败，请稍后再试")
		return
	}
	if disabled {
		writeError(w, http.StatusForbidden, "该账号已停用，请联系管理员")
		return
	}
	if locked.Valid && locked.Time.After(time.Now()) {
		writeError(w, http.StatusTooManyRequests, "尝试次数过多，请 15 分钟后再试")
		return
	}
	if !yanquCheckPassword(hash, req.Password) {
		failures++
		var lockUntil any
		if failures >= yanquMaxFailures {
			lockUntil = time.Now().Add(yanquLockDuration)
			failures = 0
		}
		_, _ = h.DB.Exec(ctx,
			`UPDATE yanqu_local_login SET failed_attempts = $2, locked_until = $3, updated_at = now() WHERE username = $1`,
			username, failures, lockUntil)
		writeError(w, http.StatusUnauthorized, badCredentials)
		return
	}
	_, _ = h.DB.Exec(ctx,
		`UPDATE yanqu_local_login SET failed_attempts = 0, locked_until = NULL, updated_at = now() WHERE username = $1`, username)

	user, err := h.Queries.GetUser(ctx, userID)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "登录失败，请稍后再试")
		return
	}
	tokenString, err := h.issueJWT(user)
	if err != nil {
		if errors.Is(err, auth.ErrTemporarilyDisabledUser) {
			writeError(w, http.StatusForbidden, auth.TemporarilyDisabledUserError)
			return
		}
		writeError(w, http.StatusInternalServerError, "登录失败，请稍后再试")
		return
	}
	if err := auth.SetAuthCookies(w, tokenString); err != nil {
		slog.Warn("failed to set auth cookies", "error", err)
	}
	slog.Info("user logged in (password)", append(logger.RequestAttrs(r), "user_id", uuidToString(user.ID), "username", username)...)
	writeJSON(w, http.StatusOK, LoginResponse{Token: tokenString, User: h.userToResponse(user)})
}
