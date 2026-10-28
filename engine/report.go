package engine

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

const maxReportJSONDepth = 32

// clearReport removes the previous turn report. A stale report must never be
// accepted as the settlement output of a later turn.
func clearReport(workspace string) error {
	path := filepath.Join(workspace, ReportFileName)
	err := os.Remove(path)
	if err == nil || errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return fmt.Errorf("clear report: %w", err)
}

// readReport reads the protocol-mandated report file after agent_settled. The
// report is untrusted input: it must be a regular, bounded file inside the
// session workspace and a single strict JSON object.
func readReport(workspace string, maxBytes int64) ([]byte, error) {
	if workspace == "" {
		return nil, errors.New("report workspace is required")
	}
	root, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		return nil, fmt.Errorf("resolve report workspace: %w", err)
	}
	path := filepath.Join(root, ReportFileName)
	info, err := os.Lstat(path)
	if err != nil {
		return nil, fmt.Errorf("read report: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil, errors.New("report must be a regular file")
	}
	if maxBytes > 0 && info.Size() > maxBytes {
		return nil, fmt.Errorf("report exceeds %d bytes", maxBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read report: %w", err)
	}
	if maxBytes > 0 && int64(len(data)) > maxBytes {
		return nil, fmt.Errorf("report exceeds %d bytes", maxBytes)
	}
	return data, nil
}

func decodeReport(data []byte, out any) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return errors.New("report must contain one JSON object")
	}
	decoder := json.NewDecoder(bytes.NewReader(trimmed))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return errors.New("report contains multiple JSON values")
		}
		return err
	}
	return nil
}
