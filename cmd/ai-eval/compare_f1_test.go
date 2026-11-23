package main

import (
	"bytes"
	"encoding/json"
	"math"
	"testing"
)

func TestCompareRecomputesF1FromDecisions(t *testing.T) {
	cases, _, err := loadSuite("holdout")
	if err != nil {
		t.Fatal(err)
	}
	base := correctReport(t, "holdout", cases)
	next := correctReport(t, "holdout", cases)
	base.PromptVersion, next.PromptVersion = "v2", "v3"
	for i, item := range base.Report.Items {
		if item.Kind == "question" && item.Expected == "是" {
			base.Report.Items[i].Actual = "无关"
			break
		}
	}
	dir := t.TempDir()
	var stdout bytes.Buffer
	if err := run([]string{"compare", saveReport(t, dir, "old", base), saveReport(t, dir, "new", next)}, &stdout); err != nil {
		t.Fatal(err)
	}
	var result struct {
		Baseline        evalScore `json:"baseline"`
		Candidate       evalScore `json:"candidate"`
		QuestionF1Delta f1Delta   `json:"questionF1Delta"`
		RestoreF1Delta  f1Delta   `json:"restoreF1Delta"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Baseline.PromptVersion != "v2" || result.Candidate.PromptVersion != "v3" {
		t.Fatal("比较遗漏提示词版本")
	}
	oldClasses := result.Baseline.QuestionF1.Classes
	if len(oldClasses) != 3 || oldClasses[0].Label != "是" || oldClasses[0].Support != 4 || oldClasses[0].Predicted != 3 {
		t.Fatalf("旧报告样本数应从逐例结果重算：%+v", oldClasses)
	}
	if got := result.QuestionF1Delta.MacroF1; got == nil || math.Abs(*got-(1-(6.0/7+1+8.0/9)/3)) > 1e-10 {
		t.Fatalf("问答宏 F1 差值错误：%s", stdout.String())
	}
	if got := result.RestoreF1Delta.MacroF1; got == nil || *got != 0 {
		t.Fatalf("还原宏 F1 应相等：%s", stdout.String())
	}
	for _, class := range result.QuestionF1Delta.Classes {
		if class.F1 == nil || (class.Label == "否" && *class.F1 != 0) || (class.Label != "否" && *class.F1 <= 0) {
			t.Fatalf("逐类 F1 差值错误：%+v", class)
		}
	}
}
