package room

import (
	"net/url"
	"strconv"
	"testing"
)

// 只测试纯游标解析，既覆盖新分页，也保护旧客户端的查询约定。
func FuzzRoomEventRequest(f *testing.F) {
	for _, raw := range []string{
		"", "after=0", "before=bad&after=15&after=99", "history=1",
		"history=1&before=1", "history=1&after=0", "history=2&after=0",
		"history=2&before=9223372036854775807", "history=2&before=9223372036854775808",
		"history=2&after=-1", "history=2&after=1&after=2",
		"history=2&after=0&before=1", "history=2&history=2", "history=%f0%9f%90%a2",
	} {
		f.Add(raw)
	}
	f.Fuzz(func(t *testing.T, query string) {
		if len(query) > 1024 {
			return
		}
		values, err := url.ParseQuery(query)
		if err != nil {
			return
		}
		page, err := parseRoomEventRequest(values)
		if err != nil {
			return
		}
		if page.before < 0 || page.after < 0 || page.before > 0 && page.hasAfter {
			t.Fatal("有效游标必须非负且只能向一个方向翻页")
		}
		switch page.version {
		case "":
			if values.Has("history") || page.before != 0 || page.hasAfter {
				t.Fatal("旧请求只使用 after")
			}
		case "1":
			if values.Has("after") || page.after != 0 || page.hasAfter {
				t.Fatal("历史第一页协议不接受 after")
			}
		case "2":
			if page.hasAfter != values.Has("after") || values.Has("before") && page.before == 0 {
				t.Fatal("增量协议必须区分无游标与零游标")
			}
		default:
			t.Fatal("不支持的历史版本未被拒绝")
		}
		if page.version != "" && (len(values["history"]) != 1 || len(values["after"]) > 1 || len(values["before"]) > 1) {
			t.Fatal("显式协议接受了重复游标或版本")
		}

		// 用解析后的值重新构造查询，结果应保持一致。
		canonical := make(url.Values)
		if page.version != "" {
			canonical.Set("history", page.version)
		}
		if page.before > 0 {
			canonical.Set("before", strconv.FormatInt(page.before, 10))
		}
		if page.hasAfter || page.after > 0 {
			canonical.Set("after", strconv.FormatInt(page.after, 10))
		}
		again, err := parseRoomEventRequest(canonical)
		if err != nil || again != page {
			t.Fatalf("规范化后游标改变：原始=%+v 再解析=%+v err=%v", page, again, err)
		}
	})
}
