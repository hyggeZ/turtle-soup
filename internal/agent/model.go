// Package agent 提供海龟汤 AI 主持决策和单人对局。
package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const defaultBaseURL = "https://dashscope.aliyuncs.com/compatible-mode/v1"
const defaultModel = "qwen-plus"
const PromptVersion = "v2"

var ErrUnavailable = errors.New("AI 服务未配置或暂时不可用")
var ErrInvalidOutput = errors.New("AI 返回了无效判定")

// Soup 是一次对局固定使用的题目内容，不能在请求时重新读取可编辑的作品。
type Soup struct {
	Title   string `json:"title"`
	Surface string `json:"surface"`
	Answer  string `json:"answer"`
}

type Exchange struct {
	Question string `json:"question"`
	Answer   string `json:"answer"`
}

// Decision 只保存枚举判定和调用指标，不保存模型的自由文本或推理过程。
type Decision struct {
	Result           string  `json:"result"`
	Model            string  `json:"model"`
	LatencyMS        float64 `json:"latencyMs"`
	PromptTokens     int     `json:"promptTokens"`
	CompletionTokens int     `json:"completionTokens"`
	PromptVersion    string  `json:"promptVersion"`
}

// Decider 可用 Qwen 实现，也可在离线测试中替换。
type Decider interface {
	DecideQuestion(ctx context.Context, soup Soup, history []Exchange, question string) (Decision, error)
	JudgeRestore(ctx context.Context, soup Soup, history []Exchange, restore string) (Decision, error)
}

type HTTPClient interface {
	Do(request *http.Request) (*http.Response, error)
}

type Qwen struct {
	Key     string
	BaseURL string
	Model   string
	Client  HTTPClient
}

// QWEN_BASE_URL 可填写华北2业务空间专属地址；未设置时使用官方共享地址。
func NewQwenFromEnv() *Qwen {
	base := strings.TrimSpace(os.Getenv("QWEN_BASE_URL"))
	if base == "" {
		base = defaultBaseURL
	}
	model := strings.TrimSpace(os.Getenv("QWEN_MODEL"))
	if model == "" {
		model = defaultModel
	}
	return &Qwen{
		Key:     strings.TrimSpace(os.Getenv("DASHSCOPE_API_KEY")),
		BaseURL: base,
		Model:   model,
		Client:  &http.Client{Timeout: 15 * time.Second},
	}
}

func (q *Qwen) Ready() bool {
	return q != nil && q.Key != "" && q.BaseURL != "" && q.Model != ""
}

func (q *Qwen) DecideQuestion(ctx context.Context, soup Soup, history []Exchange, question string) (Decision, error) {
	return q.decide(ctx, soup, history, "question", question)
}

func (q *Qwen) JudgeRestore(ctx context.Context, soup Soup, history []Exchange, restore string) (Decision, error) {
	return q.decide(ctx, soup, history, "restore", restore)
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model           string          `json:"model"`
	Messages        []chatMessage   `json:"messages"`
	ResponseFormat  json.RawMessage `json:"response_format"`
	EnableThinking  bool            `json:"enable_thinking"`
	MaxOutputTokens int             `json:"max_tokens"`
	Temperature     float64         `json:"temperature"`
}

type chatResponse struct {
	Model   string `json:"model"`
	Choices []struct {
		FinishReason string `json:"finish_reason"`
		Message      struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

func (q *Qwen) decide(ctx context.Context, soup Soup, history []Exchange, kind, input string) (Decision, error) {
	if !q.Ready() {
		return Decision{}, ErrUnavailable
	}
	if q.Client == nil {
		return Decision{}, ErrUnavailable
	}
	// 历史记录设置上限，避免长局对话使单次推理成本不断增长。
	if len(history) > 12 {
		history = history[len(history)-12:]
	}
	userData, err := json.Marshal(struct {
		Soup    Soup       `json:"soup"`
		History []Exchange `json:"history"`
		Input   string     `json:"input"`
	}{Soup: soup, History: history, Input: input})
	if err != nil {
		return Decision{}, err
	}
	prompt := questionPrompt
	if kind == "restore" {
		prompt = restorePrompt
	}
	requestBody := chatRequest{
		Model:           q.Model,
		Messages:        []chatMessage{{Role: "system", Content: prompt}, {Role: "user", Content: string(userData)}},
		ResponseFormat:  json.RawMessage(`{"type":"json_object"}`),
		EnableThinking:  false,
		MaxOutputTokens: 80,
		Temperature:     0,
	}
	encoded, err := json.Marshal(requestBody)
	if err != nil {
		return Decision{}, err
	}
	base := strings.TrimRight(q.BaseURL, "/")
	if !strings.HasSuffix(base, "/chat/completions") {
		base += "/chat/completions"
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, base, bytes.NewReader(encoded))
	if err != nil {
		return Decision{}, err
	}
	request.Header.Set("Authorization", "Bearer "+q.Key)
	request.Header.Set("Content-Type", "application/json")
	started := time.Now()
	response, err := q.Client.Do(request)
	if err != nil {
		return Decision{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		// 不把上游错误正文写入应用日志，它可能包含用户内容。
		return Decision{}, fmt.Errorf("%w: HTTP %d", ErrUnavailable, response.StatusCode)
	}
	var output chatResponse
	if err := json.NewDecoder(io.LimitReader(response.Body, 64<<10)).Decode(&output); err != nil {
		return Decision{}, ErrInvalidOutput
	}
	if len(output.Choices) != 1 || output.Choices[0].FinishReason != "stop" {
		return Decision{}, ErrInvalidOutput
	}
	var choice struct {
		Result string `json:"result"`
	}
	reader := json.NewDecoder(strings.NewReader(output.Choices[0].Message.Content))
	reader.DisallowUnknownFields()
	if reader.Decode(&choice) != nil || reader.Decode(new(any)) != io.EOF {
		return Decision{}, ErrInvalidOutput
	}
	valid := kind == "question" && oneOf(choice.Result, "是", "否", "无关")
	valid = valid || kind == "restore" && oneOf(choice.Result, "正确", "错误", "不完整")
	if !valid {
		return Decision{}, ErrInvalidOutput
	}
	model := output.Model
	if model == "" {
		model = q.Model
	}
	latency := float64(time.Since(started)) / float64(time.Millisecond)
	return Decision{Result: choice.Result, Model: model, LatencyMS: latency, PromptTokens: output.Usage.PromptTokens, CompletionTokens: output.Usage.CompletionTokens, PromptVersion: PromptVersion}, nil
}

func oneOf(value string, choices ...string) bool {
	for _, choice := range choices {
		if value == choice {
			return true
		}
	}
	return false
}
