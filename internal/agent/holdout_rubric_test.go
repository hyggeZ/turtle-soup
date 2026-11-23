package agent

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"
	"testing"
)

type holdoutRubric struct {
	Version int                 `json:"version"`
	Items   []holdoutRubricItem `json:"items"`
}

type holdoutRubricItem struct {
	ID       string `json:"id"`
	Label    string `json:"label"`
	Evidence string `json:"evidence"`
	Reason   string `json:"reason"`
}

func readHoldoutRubric(t testing.TB) []byte {
	t.Helper()
	data, err := os.ReadFile("testdata/holdout_rubric.json")
	if err != nil {
		t.Fatal(err)
	}
	return data
}

// 人工准则单独留在测试资料中，逐条与当前独立题集核对。
func validateHoldoutRubric(data []byte, cases []EvalCase) error {
	var rubric holdoutRubric
	if err := json.Unmarshal(data, &rubric); err != nil {
		return err
	}
	if rubric.Version != 1 {
		return fmt.Errorf("不支持的准则版本 %d", rubric.Version)
	}
	if len(rubric.Items) != len(cases) {
		return fmt.Errorf("准则应覆盖全部 %d 条样例，实际 %d 条", len(cases), len(rubric.Items))
	}
	byID := make(map[string]EvalCase, len(cases))
	for _, sample := range cases {
		byID[sample.ID] = sample
	}
	seen := make(map[string]bool, len(rubric.Items))
	for _, item := range rubric.Items {
		sample, ok := byID[item.ID]
		if !ok || seen[item.ID] {
			return fmt.Errorf("准则样例 ID 不存在或重复：%q", item.ID)
		}
		seen[item.ID] = true
		if item.Label != sample.Expected {
			return fmt.Errorf("%s 的准则标签 %q 与题集标签 %q 不一致", item.ID, item.Label, sample.Expected)
		}
		if strings.TrimSpace(item.Evidence) == "" || strings.TrimSpace(item.Reason) == "" {
			return fmt.Errorf("%s 缺少证据或理由", item.ID)
		}
		if strings.Contains(item.Evidence, sample.Soup.Answer) || strings.Contains(item.Reason, sample.Soup.Answer) {
			return fmt.Errorf("%s 的准则复制了完整汤底", item.ID)
		}
	}
	return nil
}

func TestHoldoutRubricCoverageAndScope(t *testing.T) {
	cases, err := LoadHoldoutCases()
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != 24 {
		t.Fatalf("独立验证题集应有 24 条，实际 %d 条", len(cases))
	}
	if err := validateHoldoutRubric(readHoldoutRubric(t), cases); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	// 与既有 CLI 的独立验证集 scope 使用同一序列化口径。
	const want = "3379c61de4d326391adfde8a64c6d659e421a8a18cad2f1fa6b9d6fe006bf975"
	if got := fmt.Sprintf("%x", sha256.Sum256(data)); got != want {
		t.Fatalf("独立验证集 scope 指纹改变：%s", got)
	}
}

func TestHoldoutRubricRejectsBadAnnotations(t *testing.T) {
	cases, err := LoadHoldoutCases()
	if err != nil {
		t.Fatal(err)
	}
	var original holdoutRubric
	if err := json.Unmarshal(readHoldoutRubric(t), &original); err != nil {
		t.Fatal(err)
	}
	checks := []struct {
		name   string
		mutate func(*holdoutRubric)
		issue  string
	}{
		{"版本变化", func(r *holdoutRubric) { r.Version = 0 }, "不支持的准则版本"},
		{"遗漏样例", func(r *holdoutRubric) { r.Items = r.Items[1:] }, "覆盖全部"},
		{"重复样例", func(r *holdoutRubric) { r.Items[1].ID = r.Items[0].ID }, "ID 不存在或重复"},
		{"缺少 ID", func(r *holdoutRubric) { r.Items[0].ID = "" }, "ID 不存在或重复"},
		{"空白证据", func(r *holdoutRubric) { r.Items[0].Evidence = " \t" }, "缺少证据或理由"},
		{"缺少理由", func(r *holdoutRubric) { r.Items[0].Reason = "" }, "缺少证据或理由"},
		{"缺少标签", func(r *holdoutRubric) { r.Items[0].Label = "" }, "标签"},
		{"标签漂移", func(r *holdoutRubric) { r.Items[0].Label = "否" }, "标签"},
		{"复制汤底", func(r *holdoutRubric) { r.Items[0].Reason = cases[0].Soup.Answer }, "复制了完整汤底"},
	}
	for _, check := range checks {
		t.Run(check.name, func(t *testing.T) {
			changed := holdoutRubric{Version: original.Version, Items: slices.Clone(original.Items)}
			check.mutate(&changed)
			data, err := json.Marshal(changed)
			if err != nil {
				t.Fatal(err)
			}
			if err := validateHoldoutRubric(data, cases); err == nil || !strings.Contains(err.Error(), check.issue) {
				t.Fatalf("应拒绝此准则并指出 %q，得到 %v", check.issue, err)
			}
		})
	}
}

func BenchmarkHoldoutRubric(b *testing.B) {
	data := readHoldoutRubric(b)
	b.Run("Cases", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			if _, err := LoadHoldoutCases(); err != nil {
				b.Fatal(err)
			}
		}
	})
	b.Run("CasesAndRubric", func(b *testing.B) {
		b.ReportAllocs()
		for b.Loop() {
			cases, err := LoadHoldoutCases()
			if err != nil {
				b.Fatal(err)
			}
			if err := validateHoldoutRubric(data, cases); err != nil {
				b.Fatal(err)
			}
		}
	})
}
