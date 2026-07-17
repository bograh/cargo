package deployments

import (
	"bytes"
	"io"
	"os"
	"path/filepath"

	"github.com/bograh/cargo/internal/events"
)

// LogPath returns the on-disk log file for a deployment.
func (s *Service) LogPath(id string) string {
	return filepath.Join(s.dataDir, "deployments", id+".log")
}

// LogWriter appends to the deployment's log file and publishes every write
// to the hub topic "deploy:<id>" for live SSE streaming.
func (s *Service) LogWriter(id string) (io.WriteCloser, error) {
	path := s.LogPath(id)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return nil, err
	}
	return &logWriter{f: f, hub: s.hub, topic: "deploy:" + id}, nil
}

type logWriter struct {
	f     *os.File
	hub   *events.Hub
	topic string
}

func (w *logWriter) Write(p []byte) (int, error) {
	n, err := w.f.Write(p)
	if w.hub != nil && n > 0 {
		w.hub.Publish(w.topic, bytes.Clone(p[:n]))
	}
	return n, err
}

func (w *logWriter) Close() error { return w.f.Close() }
