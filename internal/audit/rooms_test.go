package audit

import (
	"slices"
	"testing"
)

func TestAuditRoomRelationships(t *testing.T) {
	ctx, db := newAuditDB(t)
	auditExec(t, ctx, db, `INSERT INTO users(username,password_hash)
		SELECT 'audit_room_'||n,'hash' FROM generate_series(1,6) n;
		INSERT INTO soups(author_id,title,surface,answer,genre,difficulty,horror)
		VALUES(1,'测试题','测试汤面','测试汤底','本格',1,0);
		INSERT INTO rooms(code,owner_id,host_id,soup_id,mode,max_players,status,turn_user_id,turn_deadline,winner_id)
		VALUES ('waiting',1,NULL,1,'casual',2,'waiting',NULL,NULL,NULL),
			('closed',1,NULL,1,'casual',2,'closed',NULL,NULL,NULL),
			('paused',1,1,1,'competitive',2,'paused',2,now(),NULL),
			('finished',1,1,1,'competitive',2,'finished',NULL,NULL,2);
		INSERT INTO room_members(room_id,user_id,role,seen_answer,correct)
		VALUES (1,1,'player',false,false),(2,1,'spectator',false,false),
			(3,1,'host',true,false),(3,2,'spectator',false,false),(3,3,'player',false,false),
			(4,1,'host',true,false),(4,2,'spectator',false,true),(4,3,'player',false,false)`)
	clean, err := Run(ctx, db, 7)
	if err != nil || clean.HasIssues() || len(clean.Rooms) != 0 {
		t.Fatalf("合法旧状态被判为异常：%+v %v", clean, err)
	}
	auditExec(t, ctx, db, `INSERT INTO rooms(code,owner_id,host_id,soup_id,mode,max_players,status,turn_user_id,turn_deadline,winner_id)
		VALUES ('broken',1,1,1,'competitive',2,'active',6,now(),3);
		INSERT INTO room_members(room_id,user_id,role,seen_answer)
		VALUES (5,2,'host',true),(5,3,'player',false),(5,4,'player',true),(5,5,'player',false)`)
	bad, err := Run(ctx, db, 7)
	if err != nil || !bad.HasIssues() || bad.RoomIssueCount != 6 {
		t.Fatalf("异常数不完整：%+v %v", bad, err)
	}
	var codes []string
	for _, item := range bad.Rooms {
		if item.RoomID != 5 {
			t.Fatalf("异常房间编号错误：%+v", item)
		}
		codes = append(codes, item.Issue)
	}
	want := []string{"host_mismatch", "owner_missing", "player_capacity_exceeded", "player_has_seen_answer", "turn_member_missing", "winner_mismatch"}
	if !slices.Equal(codes, want) {
		t.Fatalf("异常类型错误：%v", codes)
	}
	auditExec(t, ctx, db, `INSERT INTO rooms(code,owner_id,soup_id,mode,max_players)
		SELECT 'no-owner-'||n,1,1,'casual',2 FROM generate_series(1,25) n`)
	limited, err := Run(ctx, db, 7)
	if err != nil || limited.RoomIssueCount != 31 || len(limited.Rooms) != 20 {
		t.Fatalf("总数和样本上限错误：%+v %v", limited, err)
	}
}
