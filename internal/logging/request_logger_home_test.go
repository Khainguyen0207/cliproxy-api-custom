package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type stubHomeRequestLogClient struct {
	heartbeatOK bool
	pushed      [][]byte
}

func (c *stubHomeRequestLogClient) HeartbeatOK() bool { return c.heartbeatOK }

func (c *stubHomeRequestLogClient) RPushRequestLog(_ context.Context, payload []byte) error {
	c.pushed = append(c.pushed, bytes.Clone(payload))
	return nil
}

func assertFileBodySourceCleaned(t *testing.T, partPaths []string) {
	t.Helper()

	dirs := make(map[string]struct{}, len(partPaths))
	for _, path := range partPaths {
		if _, errStat := os.Stat(path); !os.IsNotExist(errStat) {
			t.Fatalf("expected part %s to be removed, stat err=%v", path, errStat)
		}
		dirs[filepath.Dir(path)] = struct{}{}
	}
	for dir := range dirs {
		if _, errStat := os.Stat(dir); !os.IsNotExist(errStat) {
			t.Fatalf("expected part dir %s to be removed, stat err=%v", dir, errStat)
		}
	}
}

func TestFileBodySource_RecreatesPartDirAfterManualCleanup(t *testing.T) {
	logsDir := t.TempDir()
	source, errSource := NewFileBodySourceInDir(logsDir, "websocket-timeline-test")
	if errSource != nil {
		t.Fatalf("NewFileBodySourceInDir: %v", errSource)
	}
	if errAppend := source.AppendPart([]byte("before manual cleanup")); errAppend != nil {
		t.Fatalf("AppendPart before cleanup: %v", errAppend)
	}
	if errRemove := os.RemoveAll(logsDir); errRemove != nil {
		t.Fatalf("RemoveAll logs dir: %v", errRemove)
	}
	if errAppend := source.AppendPart([]byte("after manual cleanup")); errAppend != nil {
		t.Fatalf("AppendPart after cleanup: %v", errAppend)
	}

	raw, errBytes := source.Bytes()
	if errBytes != nil {
		t.Fatalf("Bytes after cleanup: %v", errBytes)
	}
	if bytes.Contains(raw, []byte("before manual cleanup")) {
		t.Fatalf("expected manually removed part to be skipped, got %q", string(raw))
	}
	if !bytes.Contains(raw, []byte("after manual cleanup")) {
		t.Fatalf("expected recreated part content, got %q", string(raw))
	}

	partPaths := source.Paths()
	if errCleanup := source.Cleanup(); errCleanup != nil {
		t.Fatalf("Cleanup: %v", errCleanup)
	}
	assertFileBodySourceCleaned(t, partPaths)
}

func TestFileRequestLogger_HomeEnabled_ForwardsWhenRequestLogEnabled(t *testing.T) {
	original := currentHomeRequestLogClient
	defer func() {
		currentHomeRequestLogClient = original
	}()

	stub := &stubHomeRequestLogClient{heartbeatOK: true}
	currentHomeRequestLogClient = func() homeRequestLogClient {
		return stub
	}

	logsDir := t.TempDir()
	logger := NewFileRequestLogger(true, logsDir, "", 0)
	logger.SetHomeEnabled(true)

	requestHeaders := map[string][]string{
		"Content-Type":  {"application/json"},
		"Authorization": {"Bearer secret"},
	}

	errLog := logger.LogRequest(
		"/v1/chat/completions",
		http.MethodPost,
		requestHeaders,
		[]byte(`{"input":"hello"}`),
		http.StatusOK,
		map[string][]string{"Content-Type": {"application/json"}},
		[]byte(`{"ok":true}`),
		nil,
		nil,
		nil,
		nil,
		nil,
		"req-1",
		time.Now(),
		time.Now(),
	)
	if errLog != nil {
		t.Fatalf("LogRequest error: %v", errLog)
	}

	entries, errRead := os.ReadDir(logsDir)
	if errRead != nil {
		t.Fatalf("failed to read logs dir: %v", errRead)
	}
	if len(entries) != 0 {
		t.Fatalf("expected no local request log files, got entries: %+v", entries)
	}

	if len(stub.pushed) != 1 {
		t.Fatalf("home pushed records = %d, want 1", len(stub.pushed))
	}

	var got struct {
		Headers    map[string][]string `json:"headers"`
		RequestID  string              `json:"request_id"`
		RequestLog string              `json:"request_log"`
	}
	if errUnmarshal := json.Unmarshal(stub.pushed[0], &got); errUnmarshal != nil {
		t.Fatalf("unmarshal payload: %v payload=%s", errUnmarshal, string(stub.pushed[0]))
	}
	if got.Headers == nil || got.Headers["Content-Type"][0] != "application/json" {
		t.Fatalf("headers.content-type = %+v, want application/json", got.Headers["Content-Type"])
	}
	if got.Headers == nil || got.Headers["Authorization"][0] != "Bearer secret" {
		t.Fatalf("headers.authorization = %+v, want Bearer secret", got.Headers["Authorization"])
	}
	if got.RequestID != "req-1" {
		t.Fatalf("request_id = %q, want req-1", got.RequestID)
	}
	if got.RequestLog == "" {
		t.Fatalf("request_log empty, want non-empty")
	}
}

func TestFileRequestLogger_LogRequestWithSourcesWritesLocalLogAndCleansParts(t *testing.T) {
	logsDir := t.TempDir()
	logger := NewFileRequestLogger(true, logsDir, "", 0)

	timelineSource, errSource := logger.NewFileBodySource("websocket-timeline-test")
	if errSource != nil {
		t.Fatalf("logger.NewFileBodySource: %v", errSource)
	}
	if errAppend := timelineSource.AppendPart([]byte("Timestamp: 2026-05-25T12:00:00Z\nEvent: websocket.request\n{}")); errAppend != nil {
		t.Fatalf("AppendPart request: %v", errAppend)
	}
	if errAppend := timelineSource.AppendPart([]byte("Timestamp: 2026-05-25T12:00:01Z\nEvent: websocket.response\n{}")); errAppend != nil {
		t.Fatalf("AppendPart response: %v", errAppend)
	}
	partPaths := timelineSource.Paths()
	for _, path := range partPaths {
		if !strings.HasPrefix(path, logsDir+string(os.PathSeparator)) {
			t.Fatalf("part path %s is not under logs dir %s", path, logsDir)
		}
	}

	errLog := logger.LogRequestWithOptionsAndSources(
		"/v1/responses/ws",
		http.MethodGet,
		map[string][]string{"Upgrade": {"websocket"}},
		nil,
		http.StatusSwitchingProtocols,
		map[string][]string{"Upgrade": {"websocket"}},
		nil,
		nil,
		timelineSource,
		nil,
		nil,
		nil,
		nil,
		nil,
		false,
		"ws-req-1",
		time.Now(),
		time.Now(),
	)
	if errLog != nil {
		t.Fatalf("LogRequestWithOptionsAndSources error: %v", errLog)
	}

	assertFileBodySourceCleaned(t, partPaths)

	dayDir := filepath.Join(logsDir, time.Now().Format("02_01_2006"))
	entries, errRead := os.ReadDir(dayDir)
	if errRead != nil {
		t.Fatalf("failed to read day logs dir: %v", errRead)
	}
	var logPath string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if entry.Name() == latestRequestLogFilename {
			continue
		}
		logPath = filepath.Join(dayDir, entry.Name())
		break
	}
	if logPath == "" {
		t.Fatal("expected local request log file")
	}
	raw, errReadLog := os.ReadFile(logPath)
	if errReadLog != nil {
		t.Fatalf("read log file: %v", errReadLog)
	}
	if !bytes.Contains(raw, []byte("=== WEBSOCKET TIMELINE ===")) {
		t.Fatalf("websocket timeline section missing: %s", string(raw))
	}
	if !bytes.Contains(raw, []byte("Event: websocket.request")) || !bytes.Contains(raw, []byte("Event: websocket.response")) {
		t.Fatalf("merged websocket events missing: %s", string(raw))
	}

	if _, errResult := os.Stat(filepath.Join(dayDir, latestRequestLogFilename)); !os.IsNotExist(errResult) {
		t.Fatalf("websocket transcript should not write daily result log, stat err=%v", errResult)
	}
}

func TestFileRequestLogger_LogRequestWritesDailyResultAndMovesDetailLog(t *testing.T) {
	logsDir := t.TempDir()
	logger := NewFileRequestLogger(true, logsDir, "", 0)
	requestTime := time.Date(2026, 7, 27, 21, 54, 33, 0, time.Local)
	responseBody := []byte(`{"id":"resp_result_1","object":"chat.completion","choices":[{"message":{"content":"hi"}}]}`)

	errLog := logger.LogRequest(
		"/v1/chat/completions",
		http.MethodPost,
		map[string][]string{"Content-Type": {"application/json"}},
		[]byte(`{"model":"test-model","messages":[{"role":"user","content":"hello"}]}`),
		http.StatusOK,
		map[string][]string{"Content-Type": {"application/json"}},
		responseBody,
		nil,
		nil,
		nil,
		nil,
		nil,
		"req-result-1",
		requestTime,
		time.Now(),
	)
	if errLog != nil {
		t.Fatalf("LogRequest error: %v", errLog)
	}

	dayDir := filepath.Join(logsDir, "27_07_2026")
	latestRaw, errLatest := os.ReadFile(filepath.Join(dayDir, latestRequestLogFilename))
	if errLatest != nil {
		t.Fatalf("read daily result log: %v", errLatest)
	}
	if got := strings.TrimSpace(string(latestRaw)); got != string(responseBody) {
		t.Fatalf("daily result log = %s, want %s", got, string(responseBody))
	}
	if bytes.Contains(latestRaw, []byte("=== REQUEST INFO ===")) {
		t.Fatalf("daily result log contains detail log content: %s", string(latestRaw))
	}

	detailPath := filepath.Join(dayDir, "resp_result_1_2026-07-27T215433.log")
	detailRaw, errDetail := os.ReadFile(detailPath)
	if errDetail != nil {
		t.Fatalf("read moved detail log: %v", errDetail)
	}
	for _, want := range [][]byte{
		[]byte("=== REQUEST BODY ==="),
		[]byte(`"model":"test-model"`),
		[]byte("=== RESPONSE ==="),
		[]byte(`"content":"hi"`),
	} {
		if !bytes.Contains(detailRaw, want) {
			t.Fatalf("detail request log missing %q: %s", string(want), string(detailRaw))
		}
	}
}

func TestFileRequestLogger_LogRequestDailyResultAppendsJSONL(t *testing.T) {
	logsDir := t.TempDir()
	logger := NewFileRequestLogger(true, logsDir, "", 0)
	requestTime := time.Date(2026, 7, 27, 21, 54, 33, 0, time.Local)

	for _, tc := range []struct {
		requestID string
		response  []byte
	}{
		{requestID: "req-result-1", response: []byte(`{"id":"resp_result_1","object":"chat.completion"}`)},
		{requestID: "req-result-2", response: []byte(`{"id":"resp_result_2","object":"chat.completion"}`)},
	} {
		errLog := logger.LogRequest(
			"/v1/chat/completions",
			http.MethodPost,
			map[string][]string{"Content-Type": {"application/json"}},
			[]byte(`{"model":"test-model"}`),
			http.StatusOK,
			map[string][]string{"Content-Type": {"application/json"}},
			tc.response,
			nil,
			nil,
			nil,
			nil,
			nil,
			tc.requestID,
			requestTime,
			time.Now(),
		)
		if errLog != nil {
			t.Fatalf("LogRequest error: %v", errLog)
		}
	}

	raw, errRead := os.ReadFile(filepath.Join(logsDir, "27_07_2026", latestRequestLogFilename))
	if errRead != nil {
		t.Fatalf("read daily result log: %v", errRead)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 {
		t.Fatalf("daily result log lines = %d, want 2: %s", len(lines), string(raw))
	}
	if lines[0] != `{"id":"resp_result_1","object":"chat.completion"}` || lines[1] != `{"id":"resp_result_2","object":"chat.completion"}` {
		t.Fatalf("daily result log lines = %#v", lines)
	}
}

func TestFileRequestLogger_LogRequestUsesRequestIDFallbackWhenResponseIDMissing(t *testing.T) {
	logsDir := t.TempDir()
	logger := NewFileRequestLogger(true, logsDir, "", 0)
	requestTime := time.Date(2026, 7, 27, 21, 54, 33, 0, time.Local)

	errLog := logger.LogRequest(
		"/v1/chat/completions",
		http.MethodPost,
		map[string][]string{"Content-Type": {"application/json"}},
		[]byte(`{"model":"test-model"}`),
		http.StatusOK,
		map[string][]string{"Content-Type": {"application/json"}},
		[]byte(`{"object":"chat.completion"}`),
		nil,
		nil,
		nil,
		nil,
		nil,
		"req-fallback-1",
		requestTime,
		time.Now(),
	)
	if errLog != nil {
		t.Fatalf("LogRequest error: %v", errLog)
	}

	detailPath := filepath.Join(logsDir, "27_07_2026", "req-fallback-1_2026-07-27T215433.log")
	if _, errStat := os.Stat(detailPath); errStat != nil {
		t.Fatalf("expected fallback detail log %s: %v", detailPath, errStat)
	}
}

func TestFileRequestLogger_HomeEnabled_ForwardsSourceLogAndCleansParts(t *testing.T) {
	original := currentHomeRequestLogClient
	defer func() {
		currentHomeRequestLogClient = original
	}()

	stub := &stubHomeRequestLogClient{heartbeatOK: true}
	currentHomeRequestLogClient = func() homeRequestLogClient {
		return stub
	}

	logsDir := t.TempDir()
	logger := NewFileRequestLogger(true, logsDir, "", 0)
	logger.SetHomeEnabled(true)

	timelineSource, errSource := logger.NewFileBodySource("home-websocket-timeline-test")
	if errSource != nil {
		t.Fatalf("logger.NewFileBodySource: %v", errSource)
	}
	if errAppend := timelineSource.AppendPart([]byte("Timestamp: 2026-05-25T12:00:00Z\nEvent: websocket.request\n{}")); errAppend != nil {
		t.Fatalf("AppendPart request: %v", errAppend)
	}
	partPaths := timelineSource.Paths()
	for _, path := range partPaths {
		if !strings.HasPrefix(path, logsDir+string(os.PathSeparator)) {
			t.Fatalf("part path %s is not under logs dir %s", path, logsDir)
		}
	}

	errLog := logger.LogRequestWithOptionsAndSources(
		"/v1/responses/ws",
		http.MethodGet,
		map[string][]string{"Upgrade": {"websocket"}},
		nil,
		http.StatusSwitchingProtocols,
		map[string][]string{"Upgrade": {"websocket"}},
		nil,
		nil,
		timelineSource,
		nil,
		nil,
		nil,
		nil,
		nil,
		false,
		"home-ws-req-1",
		time.Now(),
		time.Now(),
	)
	if errLog != nil {
		t.Fatalf("LogRequestWithOptionsAndSources error: %v", errLog)
	}
	if len(stub.pushed) != 1 {
		t.Fatalf("home pushed records = %d, want 1", len(stub.pushed))
	}

	var got struct {
		RequestID  string `json:"request_id"`
		RequestLog string `json:"request_log"`
	}
	if errUnmarshal := json.Unmarshal(stub.pushed[0], &got); errUnmarshal != nil {
		t.Fatalf("unmarshal payload: %v payload=%s", errUnmarshal, string(stub.pushed[0]))
	}
	if got.RequestID != "home-ws-req-1" {
		t.Fatalf("request_id = %q, want home-ws-req-1", got.RequestID)
	}
	if !strings.Contains(got.RequestLog, "Event: websocket.request") {
		t.Fatalf("forwarded request_log missing websocket request: %s", got.RequestLog)
	}
	assertFileBodySourceCleaned(t, partPaths)
}

func TestFileRequestLogger_HomeEnabled_ForwardsStreamingRequestID(t *testing.T) {
	original := currentHomeRequestLogClient
	defer func() {
		currentHomeRequestLogClient = original
	}()

	stub := &stubHomeRequestLogClient{heartbeatOK: true}
	currentHomeRequestLogClient = func() homeRequestLogClient {
		return stub
	}

	logsDir := t.TempDir()
	logger := NewFileRequestLogger(true, logsDir, "", 0)
	logger.SetHomeEnabled(true)

	writer, errLog := logger.LogStreamingRequest(
		"/v1/responses",
		http.MethodPost,
		map[string][]string{"Content-Type": {"application/json"}},
		[]byte(`{"input":"hello"}`),
		"stream-req-1",
	)
	if errLog != nil {
		t.Fatalf("LogStreamingRequest error: %v", errLog)
	}

	if errStatus := writer.WriteStatus(http.StatusOK, map[string][]string{"Content-Type": {"text/event-stream"}}); errStatus != nil {
		t.Fatalf("WriteStatus error: %v", errStatus)
	}
	writer.WriteChunkAsync([]byte("data: ok\n\n"))
	if errClose := writer.Close(); errClose != nil {
		t.Fatalf("Close error: %v", errClose)
	}

	if len(stub.pushed) != 1 {
		t.Fatalf("home pushed records = %d, want 1", len(stub.pushed))
	}

	var got struct {
		RequestID  string `json:"request_id"`
		RequestLog string `json:"request_log"`
	}
	if errUnmarshal := json.Unmarshal(stub.pushed[0], &got); errUnmarshal != nil {
		t.Fatalf("unmarshal payload: %v payload=%s", errUnmarshal, string(stub.pushed[0]))
	}
	if got.RequestID != "stream-req-1" {
		t.Fatalf("request_id = %q, want stream-req-1", got.RequestID)
	}
	if got.RequestLog == "" {
		t.Fatalf("request_log empty, want non-empty")
	}
}

func TestFileRequestLogger_LogStreamingRequestWritesDailyDetailLog(t *testing.T) {
	logsDir := t.TempDir()
	logger := NewFileRequestLogger(true, logsDir, "", 0)

	writer, errLog := logger.LogStreamingRequest(
		"/v1/responses",
		http.MethodPost,
		map[string][]string{"Content-Type": {"application/json"}},
		[]byte(`{"input":"hello"}`),
		"stream-result-1",
	)
	if errLog != nil {
		t.Fatalf("LogStreamingRequest error: %v", errLog)
	}

	if errStatus := writer.WriteStatus(http.StatusOK, map[string][]string{"Content-Type": {"text/event-stream"}}); errStatus != nil {
		t.Fatalf("WriteStatus error: %v", errStatus)
	}
	if errAPIRequest := writer.WriteAPIRequest([]byte("=== API REQUEST 1 ===\nBody: upstream request\n")); errAPIRequest != nil {
		t.Fatalf("WriteAPIRequest error: %v", errAPIRequest)
	}
	if errAPIResponse := writer.WriteAPIResponse([]byte("=== API RESPONSE 1 ===\nBody: upstream response\n")); errAPIResponse != nil {
		t.Fatalf("WriteAPIResponse error: %v", errAPIResponse)
	}
	writer.WriteChunkAsync([]byte(`{"id":"stream_result_1","object":"response","output_text":"stream result"}`))
	if errClose := writer.Close(); errClose != nil {
		t.Fatalf("Close error: %v", errClose)
	}

	dayDir := filepath.Join(logsDir, time.Now().Format("02_01_2006"))
	latestRaw, errLatest := os.ReadFile(filepath.Join(dayDir, latestRequestLogFilename))
	if errLatest != nil {
		t.Fatalf("read daily result log: %v", errLatest)
	}
	if strings.TrimSpace(string(latestRaw)) != `{"id":"stream_result_1","object":"response","output_text":"stream result"}` {
		t.Fatalf("daily result log = %s", string(latestRaw))
	}

	detailPath := ""
	entries, errReadDir := os.ReadDir(dayDir)
	if errReadDir != nil {
		t.Fatalf("read day logs dir: %v", errReadDir)
	}
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == latestRequestLogFilename {
			continue
		}
		if strings.HasPrefix(entry.Name(), "stream_result_1_") && strings.HasSuffix(entry.Name(), ".log") {
			detailPath = filepath.Join(dayDir, entry.Name())
			break
		}
	}
	if detailPath == "" {
		t.Fatalf("streaming detail log not found in %s", dayDir)
	}
	detailRaw, errDetail := os.ReadFile(detailPath)
	if errDetail != nil {
		t.Fatalf("read streaming detail log: %v", errDetail)
	}
	for _, want := range [][]byte{
		[]byte("=== REQUEST BODY ==="),
		[]byte(`"input":"hello"`),
		[]byte("=== API REQUEST 1 ==="),
		[]byte("upstream response"),
		[]byte("stream result"),
	} {
		if !bytes.Contains(detailRaw, want) {
			t.Fatalf("streaming detail log missing %q: %s", string(want), string(detailRaw))
		}
	}
}

func TestFileRequestLogger_HomeEnabled_DoesNotForwardForcedErrorLogsWhenRequestLogDisabled(t *testing.T) {
	original := currentHomeRequestLogClient
	defer func() {
		currentHomeRequestLogClient = original
	}()

	stub := &stubHomeRequestLogClient{heartbeatOK: true}
	currentHomeRequestLogClient = func() homeRequestLogClient {
		return stub
	}

	logsDir := t.TempDir()
	logger := NewFileRequestLogger(false, logsDir, "", 0)
	logger.SetHomeEnabled(true)

	errLog := logger.LogRequestWithOptions(
		"/v1/chat/completions",
		http.MethodPost,
		map[string][]string{"Content-Type": {"application/json"}},
		[]byte(`{"input":"hello"}`),
		http.StatusBadGateway,
		map[string][]string{"Content-Type": {"application/json"}},
		[]byte(`{"error":"upstream failure"}`),
		nil,
		nil,
		nil,
		nil,
		nil,
		true,
		"req-2",
		time.Now(),
		time.Now(),
	)
	if errLog != nil {
		t.Fatalf("LogRequestWithOptions error: %v", errLog)
	}

	if len(stub.pushed) != 0 {
		t.Fatalf("home pushed records = %d, want 0", len(stub.pushed))
	}

	entries, errRead := os.ReadDir(logsDir)
	if errRead != nil {
		t.Fatalf("failed to read logs dir: %v", errRead)
	}
	found := false
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		if entry.Name() != "" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected local forced error log file when request-log disabled")
	}
}
