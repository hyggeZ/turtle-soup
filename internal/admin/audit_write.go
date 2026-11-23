package admin

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

const (
	auditUserCreate         = "user_create"
	auditUserPasswordChange = "user_password_change"
	auditUserDisable        = "user_disable"
	auditUserEnable         = "user_enable"
	auditSoupHide           = "soup_hide"
	auditSoupRestore        = "soup_restore"
)

// 审计随业务事务提交，同一次请求的多个动作使用一次写入。
func recordAudit(ctx context.Context, tx pgx.Tx, actorID int64, subjectType string, subjectID int64, actions ...string) error {
	tag, err := tx.Exec(ctx, `INSERT INTO admin_audit_logs(actor_id,action,subject_type,subject_id)
		SELECT $1,entry.action,$2,$3 FROM unnest($4::text[]) WITH ORDINALITY AS entry(action,position)
		ORDER BY entry.position`, actorID, subjectType, subjectID, actions)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != int64(len(actions)) {
		return errors.New("操作审计记录不完整")
	}
	return nil
}
