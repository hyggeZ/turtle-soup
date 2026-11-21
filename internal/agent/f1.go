package agent

// ClassF1 保存一个标签的人工样本数、有效预测数和正确预测数。
type ClassF1 struct {
	Label        string   `json:"label"`
	Support      int      `json:"support"`
	Predicted    int      `json:"predicted"`
	TruePositive int      `json:"truePositive"`
	F1           *float64 `json:"f1"`
}

// ClassF1Summary 对有定义的逐类 F1 取等权平均。
type ClassF1Summary struct {
	Classes []ClassF1 `json:"classes"`
	MacroF1 *float64  `json:"macroF1"`
}

// ClassificationF1 从逐例结果计算问答与还原的 F1。调用失败仍计入人工样本数。
func ClassificationF1(items []EvalItem) (question ClassF1Summary, restore ClassF1Summary) {
	return classificationF1(items, "question", "是", "否", "无关"),
		classificationF1(items, "restore", "正确", "不完整", "错误")
}

func classificationF1(items []EvalItem, kind string, labels ...string) ClassF1Summary {
	summary := ClassF1Summary{Classes: make([]ClassF1, 0, len(labels))}
	var sum float64
	defined := 0
	for _, label := range labels {
		class := ClassF1{Label: label}
		for _, item := range items {
			if item.Kind != kind {
				continue
			}
			if item.Expected == label {
				class.Support++
			}
			if item.Error == "" && item.Actual == label {
				class.Predicted++
				if item.Expected == label {
					class.TruePositive++
				}
			}
		}
		// F1 的分母等于人工样本数与预测数之和，两者都为零时保留 null。
		class.F1 = metricRatio(2*class.TruePositive, class.Support+class.Predicted)
		if class.F1 != nil {
			sum += *class.F1
			defined++
		}
		summary.Classes = append(summary.Classes, class)
	}
	if defined > 0 {
		mean := sum / float64(defined)
		summary.MacroF1 = &mean
	}
	return summary
}
