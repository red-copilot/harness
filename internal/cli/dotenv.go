package cli

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strconv"
	"strings"
	"unicode"
)

type envAssignment struct {
	key   string
	value string
}

// loadEnvFile supplements the process environment without overriding values
// explicitly supplied by the caller.
func loadEnvFile(path string) error {
	file, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("open env file: %w", err)
	}
	defer file.Close()

	var assignments []envAssignment
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	for lineNumber := 1; scanner.Scan(); lineNumber++ {
		assignment, ok, err := parseEnvLine(scanner.Text())
		if err != nil {
			return fmt.Errorf("parse env file line %d: %w", lineNumber, err)
		}
		if ok {
			assignments = append(assignments, assignment)
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read env file: %w", err)
	}

	for _, assignment := range assignments {
		if _, exists := os.LookupEnv(assignment.key); exists {
			continue
		}
		if err := os.Setenv(assignment.key, assignment.value); err != nil {
			return fmt.Errorf("set %s: %w", assignment.key, err)
		}
	}
	return nil
}

func parseEnvLine(line string) (envAssignment, bool, error) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return envAssignment{}, false, nil
	}
	if strings.HasPrefix(line, "export ") {
		line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
	}
	key, value, found := strings.Cut(line, "=")
	if !found {
		return envAssignment{}, false, errors.New("missing '='")
	}
	key = strings.TrimSpace(key)
	if !validEnvName(key) {
		return envAssignment{}, false, errors.New("invalid variable name")
	}
	value = strings.TrimSpace(value)
	if len(value) >= 2 && value[0] == '\'' && value[len(value)-1] == '\'' {
		value = value[1 : len(value)-1]
	} else if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		unquoted, err := strconv.Unquote(value)
		if err != nil {
			return envAssignment{}, false, errors.New("invalid quoted value")
		}
		value = unquoted
	}
	return envAssignment{key: key, value: value}, true, nil
}

func validEnvName(name string) bool {
	if name == "" {
		return false
	}
	for index, char := range name {
		if char == '_' || unicode.IsLetter(char) || (index > 0 && unicode.IsDigit(char)) {
			continue
		}
		return false
	}
	return true
}
