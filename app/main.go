// Command ks26-app serves a minimal internal knowledge Q&A page.
//
// It has no runtime dependencies beyond the Go standard library. All
// configuration comes from environment variables; when INFERENCE_URL is
// unset or unreachable, the service keeps answering with a canned,
// clearly-labeled degraded reply instead of failing.
package main

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"
)

//go:embed index.html
var assets embed.FS

var pageTemplate = template.Must(template.ParseFS(assets, "index.html"))

type config struct {
	port             string
	appName          string
	owner            string
	imageTag         string
	inferenceURL     string
	inferenceTimeout time.Duration
	degradedMessage  string
}

func loadConfig() config {
	return config{
		port:             firstNonEmpty(os.Getenv("PORT"), os.Getenv("HTTP_PORT"), os.Getenv("LISTEN_PORT"), "8080"),
		appName:          firstNonEmpty(os.Getenv("APP_NAME"), "ks26 內部知識問答"),
		owner:            firstNonEmpty(os.Getenv("APP_OWNER"), "unassigned"),
		imageTag:         firstNonEmpty(os.Getenv("IMAGE_TAG"), "unknown"),
		inferenceURL:     os.Getenv("INFERENCE_URL"),
		inferenceTimeout: durationSeconds(os.Getenv("INFERENCE_TIMEOUT_SECONDS"), 5*time.Second),
		degradedMessage: firstNonEmpty(os.Getenv("DEGRADED_MESSAGE"),
			"目前沒有可用的推論後端，這是降級回覆：問題已收到，但暫時無法產生答案，請稍後再試。"),
	}
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if v != "" {
			return v
		}
	}
	return ""
}

func durationSeconds(raw string, fallback time.Duration) time.Duration {
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return fallback
	}
	return time.Duration(n) * time.Second
}

type server struct {
	cfg    config
	client *http.Client
}

func newServer(cfg config) *server {
	return &server{
		cfg:    cfg,
		client: &http.Client{Timeout: cfg.inferenceTimeout},
	}
}

// handleHealthz answers the liveness/readiness probes. It always returns 200:
// the service's own health never depends on the inference backend being up.
func (s *server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

func (s *server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	data := struct {
		AppName  string
		Owner    string
		ImageTag string
	}{s.cfg.appName, s.cfg.owner, s.cfg.imageTag}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := pageTemplate.Execute(w, data); err != nil {
		log.Printf("render index: %v", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
	}
}

type askRequest struct {
	Question string `json:"question"`
}

type askResponse struct {
	Answer   string `json:"answer"`
	Degraded bool   `json:"degraded"`
}

func (s *server) handleAsk(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var req askRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 8<<10)).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	resp := s.answer(r.Context(), req.Question)
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// answer tries the configured inference backend and falls back to a
// canned, explicitly-flagged degraded reply whenever that backend is not
// configured or does not respond successfully. The service must stay
// answerable even when nothing is listening on INFERENCE_URL.
func (s *server) answer(ctx context.Context, question string) askResponse {
	if s.cfg.inferenceURL == "" {
		log.Printf("INFERENCE_URL not set, serving degraded fallback answer")
		return s.degradedAnswer()
	}

	reply, err := s.callInference(ctx, question)
	if err != nil {
		log.Printf("inference backend unavailable, falling back to degraded answer: %v", err)
		return s.degradedAnswer()
	}
	return askResponse{Answer: reply, Degraded: false}
}

func (s *server) degradedAnswer() askResponse {
	return askResponse{Answer: s.cfg.degradedMessage, Degraded: true}
}

func (s *server) callInference(ctx context.Context, question string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, s.cfg.inferenceTimeout)
	defer cancel()

	body, err := json.Marshal(askRequest{Question: question})
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.inferenceURL+"/v1/answer", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	res, err := s.client.Do(req)
	if err != nil {
		return "", err
	}
	defer res.Body.Close()

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		return "", errors.New("inference backend returned status " + res.Status)
	}

	var out askResponse
	if err := json.NewDecoder(io.LimitReader(res.Body, 64<<10)).Decode(&out); err != nil {
		return "", err
	}
	return out.Answer, nil
}

func main() {
	cfg := loadConfig()
	s := newServer(cfg)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/api/ask", s.handleAsk)
	mux.HandleFunc("/", s.handleIndex)

	srv := &http.Server{
		Addr:              ":" + cfg.port,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf("ks26-app listening on :%s (owner=%s, inference=%s)", cfg.port, cfg.owner, presence(cfg.inferenceURL))
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("server stopped: %v", err)
	}
}

func presence(v string) string {
	if v == "" {
		return "unset (degraded mode)"
	}
	return "configured"
}
