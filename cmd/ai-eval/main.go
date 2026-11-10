// ai-eval 在服务器本地调用模型，评测固定题目并输出不含汤底的统计报告。
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"turtle-soup/internal/agent"
)

type output struct {
	Model         string           `json:"model"`
	PromptVersion string           `json:"promptVersion"`
	EvalSetHash   string           `json:"evalSetHash"`
	Report        agent.EvalReport `json:"report"`
}

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "评测失败："+err.Error())
		os.Exit(1)
	}
}

func run(args []string, stdout io.Writer) error {
	flags := flag.NewFlagSet("ai-eval", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	limit := flags.Int("limit", 0, "只评测前 N 条；0 表示全部")
	out := flags.String("out", "-", "JSON 输出文件；- 表示标准输出")
	timeout := flags.Duration("timeout", 15*time.Minute, "整个评测的超时时间")
	if flags.Parse(args) != nil || flags.NArg() != 0 {
		return errors.New("参数无效")
	}
	if *limit < 0 || *out == "" || *timeout <= 0 {
		return errors.New("-limit 不能为负数，-out 不能为空，-timeout 必须大于零")
	}

	cases, err := agent.LoadEvalCases()
	if err != nil {
		return errors.New("固定评测集加载失败")
	}
	if *limit > 0 && *limit < len(cases) {
		cases = cases[:*limit]
	}
	model := agent.NewQwenFromEnv()
	if !model.Ready() {
		return errors.New("需要配置 DASHSCOPE_API_KEY 和有效的模型地址")
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	report := agent.Evaluate(ctx, model, cases)
	if ctx.Err() != nil {
		return errors.New("评测已超时或取消")
	}

	// 用户可以配置模型名；这里也遮盖误填进模型名的密钥。
	modelName := strings.ReplaceAll(model.Model, model.Key, "[REDACTED]")
	result := output{Model: modelName, PromptVersion: agent.PromptVersion, EvalSetHash: agent.EvalSetHash(), Report: report}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return errors.New("报告编码失败")
	}
	data = append(data, '\n')
	if *out == "-" {
		_, err = stdout.Write(data)
	} else {
		err = writeFile(*out, data)
	}
	if err != nil {
		return errors.New("报告写入失败")
	}
	return nil
}

func writeFile(path string, data []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := file.Chmod(0o600); err != nil {
		return err
	}
	_, err = file.Write(data)
	return err
}
