// Package audit 核对 AI 配额和房间成员关系。
package audit

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Report struct {
	Days           int            `json:"days"`
	Quota          []QuotaFinding `json:"quota"`
	RoomIssueCount int64          `json:"roomIssueCount"`
	Rooms          []RoomFinding  `json:"rooms"`
}

// HasIssues 区分明确违反不变量的异常与需要人工复核的全站配额差额。
func (r Report) HasIssues() bool {
	if r.RoomIssueCount > 0 {
		return true
	}
	for _, item := range r.Quota {
		if item.Issue == "global_deficit" {
			return true
		}
	}
	return false
}

// Run 在同一个只读快照内对账，避免查询过程中发生的提交制造虚假差额。
func Run(ctx context.Context, db *pgxpool.Pool, days int) (Report, error) {
	if days < 1 || days > 31 {
		return Report{}, errors.New("对账天数须在 1 至 31 之间")
	}
	tx, err := db.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return Report{}, fmt.Errorf("创建对账快照: %w", err)
	}
	defer func() {
		// 查询超时也要给回滚留出时间，及时归还连接。
		cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		_ = tx.Rollback(cleanup)
	}()
	quota, err := checkQuota(ctx, tx, days)
	if err != nil {
		return Report{}, fmt.Errorf("核对 AI 配额: %w", err)
	}
	count, rooms, err := checkRooms(ctx, tx)
	if err != nil {
		return Report{}, fmt.Errorf("核对房间成员: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return Report{}, fmt.Errorf("结束对账快照: %w", err)
	}
	return Report{Days: days, Quota: quota, RoomIssueCount: count, Rooms: rooms}, nil
}
