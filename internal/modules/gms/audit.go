package gms

import (
	"context"
	"time"

	"github.com/jmoiron/sqlx"

	"cow-manager-backend/internal/httpx"
)

// 审核状态,与 Java AuditStatus / 前端 AuditWidget 一致。
const (
	AuditWait     = 0
	AuditApproval = 1
	AuditReject   = 2
	AuditFail     = 3
)

// Auditable 运营配置表共有的审核/激活字段。时间均为毫秒。
type Auditable struct {
	CreatedBy   int64 `db:"createdBy" json:"createdBy"`
	CreatedTime int64 `db:"createdTime" json:"createdTime"`
	DeletedBy   int64 `db:"deletedBy" json:"deletedBy"`
	DeletedTime int64 `db:"deletedTime" json:"deletedTime"`
	AuditStatus int   `db:"auditStatus" json:"auditStatus"`
	AuditBy     int64 `db:"auditBy" json:"auditBy"`
	AuditTime   int64 `db:"auditTime" json:"auditTime"`
	Active      bool  `db:"active" json:"active"`
}

// newAuditable 新建行的审核字段初值:待审核、未激活。
func newAuditable(uid int64) Auditable {
	return Auditable{CreatedBy: uid, CreatedTime: time.Now().UnixMilli(), AuditStatus: AuditWait}
}

// auditTables 模块名 → 表名白名单。
var auditTables = map[string]string{
	"mail":         "gms_ranch_mail",
	"group-mail":   "gms_ranch_group_mail",
	"check-in":     "gms_ranch_check_in",
	"guild-battle": "gms_ranch_guild_battle",
}

// setAuditStatus 批量审核:只允许从待审核流转。
func setAuditStatus(ctx context.Context, db *sqlx.DB, table string, ids []int64, status int, uid int64) error {
	if len(ids) == 0 {
		return httpx.BadRequest("ids 不能为空")
	}
	now := time.Now().UnixMilli()
	for _, id := range ids {
		res, err := db.ExecContext(ctx, "UPDATE `"+table+"` SET auditStatus = ?, auditBy = ?, auditTime = ? WHERE id = ? AND deletedTime = 0 AND auditStatus = ?",
			status, uid, now, id, AuditWait)
		if err != nil {
			return err
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return httpx.NewError(httpx.CodeOperationFailed, "记录不存在或不是待审核状态")
		}
	}
	return nil
}

// setActive 激活/停用。
func setActive(ctx context.Context, db *sqlx.DB, table string, id int64, active bool) error {
	res, err := db.ExecContext(ctx, "UPDATE `"+table+"` SET active = ? WHERE id = ? AND deletedTime = 0", active, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// 状态未变也算成功(与 Java no-op 一致),但记录必须存在
		var cnt int
		if err := db.GetContext(ctx, &cnt, "SELECT COUNT(*) FROM `"+table+"` WHERE id = ? AND deletedTime = 0", id); err != nil {
			return err
		}
		if cnt == 0 {
			return httpx.NotFound("记录不存在")
		}
	}
	return nil
}

// softDelete 软删除。
func softDelete(ctx context.Context, db *sqlx.DB, table string, id, uid int64) error {
	_, err := db.ExecContext(ctx, "UPDATE `"+table+"` SET deletedBy = ?, deletedTime = ? WHERE id = ? AND deletedTime = 0", uid, time.Now().UnixMilli(), id)
	return err
}
