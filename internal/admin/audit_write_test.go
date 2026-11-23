package admin_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

type auditWriteEntry struct {
	actorID, subjectID  int64
	action, subjectType string
}

func assertAuditWrites(t *testing.T, s *moderationTest, want ...auditWriteEntry) {
	t.Helper()
	rows, err := s.db.Query(context.Background(), `SELECT actor_id,subject_id,action,subject_type,created_at FROM admin_audit_logs ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []auditWriteEntry
	for rows.Next() {
		var row auditWriteEntry
		var created time.Time
		if err := rows.Scan(&row.actorID, &row.subjectID, &row.action, &row.subjectType, &created); err != nil {
			t.Fatal(err)
		}
		if created.IsZero() {
			t.Fatal("审计时间缺失")
		}
		got = append(got, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("审计记录错误\n实际：%+v\n期望：%+v", got, want)
	}
}

func TestAdminAuditRecordsChanges(t *testing.T) {
	s := newModerationTest(t)
	ctx := context.Background()
	adminID, targetID := s.users["admin"], s.users["user"]
	userPath := fmt.Sprintf("/api/admin/users/%d", targetID)
	newUser := map[string]string{"username": "audit_new_user", "password": "audit-fixture-1", "role": "user"}
	res := s.call(http.MethodPost, "/api/admin/users", "admin", newUser)
	var created struct {
		ID int64 `json:"id"`
	}
	if res.Code != http.StatusCreated || json.Unmarshal(res.Body.Bytes(), &created) != nil || created.ID < 1 {
		t.Fatalf("创建账号失败：HTTP %d %s", res.Code, res.Body.String())
	}
	if res := s.call(http.MethodPost, "/api/admin/users", "admin", newUser); res.Code != http.StatusConflict {
		t.Fatalf("重复账号返回 HTTP %d", res.Code)
	}
	newUser["role"] = "admin"
	if res := s.call(http.MethodPost, "/api/admin/users", "admin", newUser); res.Code != http.StatusForbidden {
		t.Fatalf("管理员创建同级账号返回 HTTP %d", res.Code)
	}
	if res := s.call(http.MethodPatch, userPath, "user", map[string]bool{"disabled": true}); res.Code != http.StatusForbidden {
		t.Fatalf("普通用户更改账号状态返回 HTTP %d", res.Code)
	}
	if res := s.call(http.MethodPatch, userPath, "admin", map[string]bool{"disabled": false}); res.Code != http.StatusOK {
		t.Fatalf("相同账号状态返回 HTTP %d", res.Code)
	}
	var sessions int
	if err := s.db.QueryRow(ctx, `SELECT count(*) FROM sessions WHERE user_id=$1`, targetID).Scan(&sessions); err != nil || sessions != 1 {
		t.Fatalf("状态没变化却影响会话：会话数=%d，错误=%v", sessions, err)
	}
	if res := s.call(http.MethodPatch, userPath, "admin", map[string]any{"disabled": true, "password": "audit-fixture-2"}); res.Code != http.StatusOK {
		t.Fatalf("同时停用和改密失败：HTTP %d %s", res.Code, res.Body.String())
	}
	var disabled bool
	var hash string
	if err := s.db.QueryRow(ctx, `SELECT disabled,password_hash,(SELECT count(*) FROM sessions WHERE user_id=$1)
		FROM users WHERE id=$1`, targetID).Scan(&disabled, &hash, &sessions); err != nil || !disabled || sessions != 0 {
		t.Fatalf("停用和撤销会话未提交：停用=%v，会话数=%d，错误=%v", disabled, sessions, err)
	}
	if bcrypt.CompareHashAndPassword([]byte(hash), []byte("audit-fixture-2")) != nil {
		t.Fatal("新密码未保存")
	}
	if res := s.call(http.MethodPatch, userPath, "admin", map[string]bool{"disabled": false}); res.Code != http.StatusOK {
		t.Fatalf("启用账号返回 HTTP %d", res.Code)
	}
	peerSoup := s.soup("admin", "同级的待审核题", "同级题目汤底")
	if res := s.call(http.MethodPatch, fmt.Sprintf("/api/admin/soups/%d", peerSoup), "admin", map[string]bool{"hidden": true}); res.Code != http.StatusNotFound {
		t.Fatalf("审核同级题目返回 HTTP %d", res.Code)
	}
	soup := s.soup("user", "可审核的测试题", "用于审计测试的模拟汤底")
	soupPath := fmt.Sprintf("/api/admin/soups/%d", soup)
	for _, hidden := range []bool{true, true, false, false} {
		if res := s.call(http.MethodPatch, soupPath, "admin", map[string]bool{"hidden": hidden}); res.Code != http.StatusOK {
			t.Fatalf("审核题目失败：状态=%v，HTTP %d %s", hidden, res.Code, res.Body.String())
		}
	}
	assertAuditWrites(t, s,
		auditWriteEntry{adminID, created.ID, "user_create", "user"},
		auditWriteEntry{adminID, targetID, "user_disable", "user"},
		auditWriteEntry{adminID, targetID, "user_password_change", "user"},
		auditWriteEntry{adminID, targetID, "user_enable", "user"},
		auditWriteEntry{adminID, soup, "soup_hide", "soup"},
		auditWriteEntry{adminID, soup, "soup_restore", "soup"},
	)
}

func TestAdminAuditRollbackOnWriteFailure(t *testing.T) {
	s := newModerationTest(t)
	ctx := context.Background()
	targetID := s.users["user"]
	soup := s.soup("user", "审计回滚测试题", "审计回滚测试汤底")
	room := s.room(soup, "waiting")
	// 仅在临时 schema 注入数据库写入错误，覆盖三个独立事务入口。
	if _, err := s.db.Exec(ctx, `ALTER TABLE admin_audit_logs ADD CONSTRAINT audit_fixture_unavailable CHECK (false) NOT VALID`); err != nil {
		t.Fatal(err)
	}
	changes := []struct {
		method string
		path   string
		body   any
	}{
		{http.MethodPost, "/api/admin/users", map[string]string{"username": "audit_rolled_back", "password": "audit-fixture-3", "role": "user"}},
		{http.MethodPatch, fmt.Sprintf("/api/admin/users/%d", targetID), map[string]any{"password": "audit-fixture-4", "disabled": true}},
		{http.MethodPatch, fmt.Sprintf("/api/admin/soups/%d", soup), map[string]bool{"hidden": true}},
	}
	for _, change := range changes {
		if res := s.call(change.method, change.path, "admin", change.body); res.Code != http.StatusInternalServerError {
			t.Fatalf("审计写入失败后 %s %s 返回 HTTP %d", change.method, change.path, res.Code)
		}
	}
	var created, disabled, hidden bool
	var hash, status string
	var sessions int
	if err := s.db.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users WHERE username='audit_rolled_back'),
		(SELECT disabled FROM users WHERE id=$1), (SELECT password_hash FROM users WHERE id=$1),
		(SELECT count(*) FROM sessions WHERE user_id=$1), (SELECT hidden_at IS NOT NULL FROM soups WHERE id=$2),
		(SELECT status FROM rooms WHERE id=$3)`, targetID, soup, room).
		Scan(&created, &disabled, &hash, &sessions, &hidden, &status); err != nil {
		t.Fatal(err)
	}
	if created || disabled || hash != "test" || sessions != 1 || hidden || status != "waiting" || len(s.closed) != 0 {
		t.Fatalf("审计失败后业务未完整回滚：新账号=%v，停用=%v，旧密码=%v，会话数=%d，隐藏=%v，房间=%s，通知数=%d",
			created, disabled, hash == "test", sessions, hidden, status, len(s.closed))
	}
	assertAuditWrites(t, s)
	if _, err := s.db.Exec(ctx, `ALTER TABLE admin_audit_logs DROP CONSTRAINT audit_fixture_unavailable`); err != nil {
		t.Fatal(err)
	}
	if res := s.call(changes[2].method, changes[2].path, "admin", changes[2].body); res.Code != http.StatusOK || !slices.Equal(s.closed, []int64{room}) {
		t.Fatalf("恢复审计后重试失败：HTTP %d，通知房间=%v", res.Code, s.closed)
	}
	assertAuditWrites(t, s, auditWriteEntry{s.users["admin"], soup, "soup_hide", "soup"})
}
