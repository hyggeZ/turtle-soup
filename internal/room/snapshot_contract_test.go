package room_test

import (
	"encoding/json"
	"fmt"
	"mime"
	"net/http"
	"os"
	"reflect"
	"strings"
	"testing"
)

type snapshotContractCase struct {
	Name     string         `json:"name"`
	Role     string         `json:"role"`
	Query    string         `json:"query"`
	Status   int            `json:"status"`
	Response map[string]any `json:"response"`
}

type snapshotContractResult struct {
	Name     string          `json:"name"`
	Query    string          `json:"query"`
	Status   int             `json:"status"`
	Response json.RawMessage `json:"response"`
}

func loadSnapshotContract(t *testing.T) []snapshotContractCase {
	t.Helper()
	data, err := os.ReadFile("testdata/snapshot-contract-v1.json")
	if err != nil {
		t.Fatal(err)
	}
	var contract struct {
		Version int                    `json:"version"`
		Cases   []snapshotContractCase `json:"cases"`
	}
	if err := json.Unmarshal(data, &contract); err != nil {
		t.Fatal(err)
	}
	if contract.Version != 1 || len(contract.Cases) == 0 {
		t.Fatal("房间快照契约版本或案例无效")
	}
	return contract.Cases
}

// 对真实 HTTP 响应核对双方共用的案例。固定事件 ID 使游标可以直接由 JSON 表达。
func TestRoomSnapshotContractV1(t *testing.T) {
	cases := loadSnapshotContract(t)
	f := newReplayFixture(t)
	host := f.addUser("contract_host")
	player := f.addUser("contract_player")
	submitter := f.addUser("contract_submitter")
	outsider := f.addUser("contract_outsider")
	f.addSoup(host)
	id := f.addRoom("24500000000000000000000000000000", "casual", "active", true, false, host)
	if _, err := f.db.Exec(f.ctx, `INSERT INTO room_members(room_id,user_id,role) VALUES($1,$2,'player'),($1,$3,'player')`, id, f.users[player], f.users[submitter]); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(f.ctx, `INSERT INTO room_events(id,room_id,user_id,kind,body) OVERRIDING SYSTEM VALUE
		VALUES(1,$1,$2,'question','已提交的问题')`, id, f.users[player]); err != nil {
		t.Fatal(err)
	}
	const privateRestore = "契约测试中的私密还原"
	if _, err := f.db.Exec(f.ctx, `INSERT INTO room_events(id,room_id,user_id,kind,body) OVERRIDING SYSTEM VALUE
		SELECT n,$1,$2,'restore',$3 FROM generate_series(2,52) AS n`, id, f.users[submitter], privateRestore); err != nil {
		t.Fatal(err)
	}
	roles := map[string]int{"host": host, "player": player, "outsider": outsider, "anonymous": -1}
	output := os.Getenv("SNAPSHOT_CONTRACT_OUTPUT")
	var results []snapshotContractResult
	for _, test := range cases {
		t.Run(test.Name, func(t *testing.T) {
			who, ok := roles[test.Role]
			if !ok {
				t.Fatalf("未知契约身份 %q", test.Role)
			}
			path := fmt.Sprintf("/api/rooms/%d?%s", id, test.Query)
			res := f.call(http.MethodGet, path, nil, who)
			if res.Code != test.Status {
				t.Fatalf("HTTP %d，预期 %d", res.Code, test.Status)
			}
			contentType, _, err := mime.ParseMediaType(res.Header().Get("Content-Type"))
			if err != nil || contentType != "application/json" {
				t.Fatalf("响应类型无效 %q", res.Header().Get("Content-Type"))
			}
			if err := verifySnapshotFields(res.Body.Bytes(), test.Response); err != nil {
				t.Fatal(err)
			}
			if test.Role != "host" && (strings.Contains(res.Body.String(), "回看绝密汤底") || strings.Contains(res.Body.String(), privateRestore)) {
				t.Fatal("非主持身份收到了私密内容")
			}
			if output != "" {
				results = append(results, snapshotContractResult{
					Name:     test.Name,
					Query:    test.Query,
					Status:   res.Code,
					Response: res.Body.Bytes(),
				})
			}
		})
	}
	if output != "" && !t.Failed() {
		writeSnapshotContract(t, output, results)
	}
}

// 只导出隔离测试库的响应，供 OpenAPI 校验完整 JSON。
func writeSnapshotContract(t *testing.T, path string, results []snapshotContractResult) {
	t.Helper()
	data, err := json.Marshal(struct {
		Version int                      `json:"version"`
		Cases   []snapshotContractResult `json:"cases"`
	}{Version: 1, Cases: results})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

// 比较协议相关字段，同时允许快照业务字段新增。
func verifySnapshotFields(data []byte, want map[string]any) error {
	var got map[string]any
	if err := json.Unmarshal(data, &got); err != nil {
		return err
	}
	for _, field := range []string{"answer", "events", "eventCursor", "historyBefore", "hasMoreNew", "tasks", "error"} {
		if _, present := got[field]; present {
			if _, expected := want[field]; !expected {
				return fmt.Errorf("%s 不应出现在此响应中", field)
			}
		}
	}
	return compareSnapshotObject("", got, want)
}

func compareSnapshotObject(prefix string, got, want map[string]any) error {
	for name, expected := range want {
		path := prefix + name
		value, present := got[name]
		if !present {
			return fmt.Errorf("%s 缺失", path)
		}
		if err := compareSnapshotValue(path, value, expected); err != nil {
			return err
		}
	}
	return nil
}

func compareSnapshotValue(path string, got, want any) error {
	switch expected := want.(type) {
	case map[string]any:
		if value, ok := got.(map[string]any); ok {
			return compareSnapshotObject(path+".", value, expected)
		}
	case []any:
		if value, ok := got.([]any); ok && len(value) == len(expected) {
			for i, item := range expected {
				if err := compareSnapshotValue(fmt.Sprintf("%s[%d]", path, i), value[i], item); err != nil {
					return err
				}
			}
			return nil
		}
	default:
		if reflect.DeepEqual(got, want) {
			return nil
		}
	}
	return fmt.Errorf("%s 与契约不符。收到 %v，预期 %v", path, got, want)
}

// 变更合成响应，确认检查器能分辨缺字段、null、零值与错误游标。
func TestRoomSnapshotContractRejectsInvalid(t *testing.T) {
	cases := loadSnapshotContract(t)
	byName := make(map[string]map[string]any)
	for _, test := range cases {
		byName[test.Name] = test.Response
	}
	for _, test := range []struct {
		name, source, field string
		mutate              func(map[string]any)
	}{
		{"布尔字段被省略", "权限过滤后的增量", "hasMoreNew", func(data map[string]any) { delete(data, "hasMoreNew") }},
		{"空数组被编码为 null", "权限过滤后的增量", "events", func(data map[string]any) { data["events"] = nil }},
		{"结束游标被省略", "较早历史", "historyBefore", func(data map[string]any) { delete(data, "historyBefore") }},
		{"待办游标没有前进", "主持待办续页", "tasks.nextAfter", func(data map[string]any) { data["tasks"].(map[string]any)["nextAfter"] = float64(51) }},
	} {
		t.Run(test.name, func(t *testing.T) {
			want, ok := byName[test.source]
			if !ok {
				t.Fatalf("找不到基础案例 %q", test.source)
			}
			original, err := json.Marshal(want)
			if err != nil {
				t.Fatal(err)
			}
			var changed map[string]any
			if err := json.Unmarshal(original, &changed); err != nil {
				t.Fatal(err)
			}
			test.mutate(changed)
			data, err := json.Marshal(changed)
			if err != nil {
				t.Fatal(err)
			}
			if err := verifySnapshotFields(data, want); err == nil || !strings.HasPrefix(err.Error(), test.field+" ") {
				t.Fatalf("未在预期字段 %s 拒绝修改。返回 %v", test.field, err)
			}
		})
	}
}
