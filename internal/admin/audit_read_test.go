package admin_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"
)

type auditListPage struct {
	Items []struct {
		ID          int64     `json:"id"`
		ActorID     int64     `json:"actorId"`
		ActorName   string    `json:"actorName"`
		Action      string    `json:"action"`
		SubjectType string    `json:"subjectType"`
		SubjectID   int64     `json:"subjectId"`
		CreatedAt   time.Time `json:"createdAt"`
	} `json:"items"`
	NextBefore int64 `json:"nextBefore"`
}

func TestAdminAuditListAuthorizationAndCursor(t *testing.T) {
	f := newModerationTest(t)
	const path = "/api/admin/audit"
	for role, want := range map[string]int{"": http.StatusUnauthorized, "user": http.StatusForbidden, "admin": http.StatusForbidden} {
		res := f.call(http.MethodGet, path, role, nil)
		if res.Code != want {
			t.Fatalf("角色 %q 读取审计得到 %d，期望 %d", role, res.Code, want)
		}
	}
	read := func(query string) auditListPage {
		t.Helper()
		res := f.call(http.MethodGet, path+query, "super_admin", nil)
		if res.Code != http.StatusOK || res.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("读取审计失败：HTTP %d，缓存规则 %q", res.Code, res.Header().Get("Cache-Control"))
		}
		var page auditListPage
		if err := json.Unmarshal(res.Body.Bytes(), &page); err != nil || page.Items == nil {
			t.Fatalf("审计分页响应无效：%v", err)
		}
		return page
	}
	if empty := read(""); len(empty.Items) != 0 || empty.NextBefore != 0 {
		t.Fatalf("空审计表应返回空列表，实际 %+v", empty)
	}
	for _, query := range []string{"?before=", "?before=0", "?before=2&before=1", "?before=9223372036854775808"} {
		if res := f.call(http.MethodGet, path+query, "super_admin", nil); res.Code != http.StatusBadRequest {
			t.Fatalf("错误游标 %q 返回 %d", query, res.Code)
		}
	}

	insert := func(first, last int) {
		t.Helper()
		if _, err := f.db.Exec(context.Background(), `
			INSERT INTO admin_audit_logs(actor_id,action,subject_type,subject_id)
			SELECT $1,'soup_hide','soup',n FROM generate_series($2::integer,$3::integer) AS n
			ORDER BY n`, f.users["admin"], first, last); err != nil {
			t.Fatal(err)
		}
	}
	insert(1, 52)
	first := read("")
	if len(first.Items) != 50 || first.Items[0].SubjectID != 52 || first.NextBefore != first.Items[49].ID {
		t.Fatal("审计首页的顺序、数量或续页游标错误")
	}
	for i, item := range first.Items {
		if item.SubjectID != int64(52-i) || item.ActorID != f.users["admin"] || item.ActorName != "moderation_admin" ||
			item.Action != "soup_hide" || item.SubjectType != "soup" || item.CreatedAt.IsZero() {
			t.Fatalf("第 %d 条审计记录字段异常：%+v", i+1, item)
		}
	}
	insert(53, 53)
	second := read(fmt.Sprintf("?before=%d", first.NextBefore))
	if len(second.Items) != 2 || second.Items[0].SubjectID != 2 || second.Items[1].SubjectID != 1 || second.NextBefore != 0 {
		t.Fatal("新增审计记录后，旧游标分页漏项或重复")
	}
}
