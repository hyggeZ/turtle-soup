package agent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

type mockHTTP struct {
	respond func(*http.Request) *http.Response
}

func (m mockHTTP) Do(request *http.Request) (*http.Response, error) {
	return m.respond(request), nil
}

func modelResponse(body string) *http.Response {
	return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(strings.NewReader(body))}
}

type slowBody struct {
	reader io.Reader
	delay  time.Duration
}

func (b *slowBody) Read(data []byte) (int, error) {
	time.Sleep(b.delay)
	b.delay = 0
	return b.reader.Read(data)
}

func (b *slowBody) Close() error { return nil }

func TestQwenStructuredDecision(t *testing.T) {
	var input chatRequest
	client := mockHTTP{respond: func(r *http.Request) *http.Response {
		if r.URL.Path != "/compatible-mode/v1/chat/completions" || r.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("请求地址或鉴权错误")
		}
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			t.Error(err)
		}
		return modelResponse(`{"model":"qwen-plus","choices":[{"finish_reason":"stop","message":{"content":"{\"result\":\"是\"}"}}],"usage":{"prompt_tokens":30,"completion_tokens":8}}`)
	}}
	model := &Qwen{Key: "test-key", BaseURL: "https://example.test/compatible-mode/v1", Model: "qwen-plus", Client: client}
	soup := Soup{Title: "剧院", Surface: "灯灭后大家鼓掌", Answer: "灯光师关灯，演出开始"}
	decision, err := model.DecideQuestion(context.Background(), soup, nil, "他是灯光师吗？")
	if err != nil || decision.Result != "是" || decision.PromptTokens != 30 || decision.CompletionTokens != 8 {
		t.Fatalf("模型结果错误：%+v %v", decision, err)
	}
	if string(input.ResponseFormat) != `{"type":"json_object"}` || input.EnableThinking {
		t.Fatalf("结构化输出设置错误：%+v", input)
	}
	if !strings.Contains(input.Messages[0].Content, "JSON") || !strings.Contains(input.Messages[1].Content, soup.Answer) {
		t.Fatal("模型没有得到受控任务说明和题目快照")
	}
}

func TestQwenRejectsUnsafeOutput(t *testing.T) {
	for _, content := range []string{
		`{"result":"汤底是剧院"}`,
		`{"result":"是","answer":"剧院"}`,
		`并不是 JSON`,
	} {
		client := mockHTTP{respond: func(_ *http.Request) *http.Response {
			message, _ := json.Marshal(content)
			return modelResponse(`{"choices":[{"finish_reason":"stop","message":{"content":` + string(message) + `}}]}`)
		}}
		model := &Qwen{Key: "test-key", BaseURL: "https://example.test", Model: "qwen-plus", Client: client}
		_, err := model.DecideQuestion(context.Background(), Soup{}, nil, "问题")
		if !errors.Is(err, ErrInvalidOutput) {
			t.Fatalf("不安全输出未被拒绝：%q %v", content, err)
		}
	}
}

func TestQwenLatencyIncludesResponseBody(t *testing.T) {
	client := mockHTTP{respond: func(_ *http.Request) *http.Response {
		body := `{"choices":[{"finish_reason":"stop","message":{"content":"{\"result\":\"是\"}"}}]}`
		return &http.Response{StatusCode: http.StatusOK, Body: &slowBody{reader: strings.NewReader(body), delay: 30 * time.Millisecond}}
	}}
	model := &Qwen{Key: "test-key", BaseURL: "https://example.test", Model: "qwen-plus", Client: client}
	decision, err := model.DecideQuestion(context.Background(), Soup{}, nil, "问题")
	if err != nil || decision.LatencyMS < 25 {
		t.Fatalf("耗时未包含响应正文读取：%.2f ms, %v", decision.LatencyMS, err)
	}
}
