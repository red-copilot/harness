package engine

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
)

const reportProtocolInstruction = "Use a file-writing tool to replace /workspace/report.json with exactly one JSON object before you finish. Assistant text is ignored; JSON printed in the assistant response will not be parsed. Each tool invocation is hard-limited to 30 seconds; keep commands shorter and split long scans.\n"

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
