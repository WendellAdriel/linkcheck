package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestCLI(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "linkcheck")
	if runtime.GOOS == "windows" {
		binary += ".exe"
	}
	build := exec.Command("go", "build", "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/missing":
			w.WriteHeader(http.StatusNotFound)
		case "/gone":
			w.WriteHeader(http.StatusGone)
		case "/limited":
			w.WriteHeader(http.StatusTooManyRequests)
		case "/slow":
			<-r.Context().Done()
		default:
			w.WriteHeader(http.StatusOK)
		}
	}))
	defer server.Close()

	for _, test := range []struct {
		name     string
		input    string
		flags    []string
		exitCode int
		outcomes []string
		stderr   string
	}{
		{
			name:     "successful deduplicated scan",
			input:    server.URL + "/ok#one\n\n" + server.URL + "/ok#two\n",
			exitCode: 0,
			outcomes: []string{"ok"},
			stderr:   "checked=1 total=1 needs_attention=0",
		},
		{
			name:     "broken links and review results",
			input:    server.URL + "/missing\n" + server.URL + "/gone\n" + server.URL + "/limited\n",
			flags:    []string{"-workers=1"},
			exitCode: 1,
			outcomes: []string{"broken", "broken", "review"},
			stderr:   "checked=3 total=3 needs_attention=3",
		},
		{
			name:     "request timeout needs review",
			input:    server.URL + "/slow\n",
			flags:    []string{"-timeout=100ms"},
			exitCode: 1,
			outcomes: []string{"review"},
			stderr:   "checked=1 total=1 needs_attention=1",
		},
		{
			name:     "scan deadline is incomplete",
			input:    server.URL + "/slow\n",
			flags:    []string{"-budget=100ms"},
			exitCode: 2,
			stderr:   "scan incomplete:",
		},
		{
			name:     "invalid input",
			input:    "file:///etc/passwd\n",
			exitCode: 2,
			stderr:   "line 1:",
		},
		{
			name:     "empty input",
			exitCode: 2,
			stderr:   "input contains no URLs",
		},
		{
			name:     "invalid limits",
			input:    server.URL + "/ok\n",
			flags:    []string{"-workers=0"},
			exitCode: 2,
			stderr:   "usage: linkcheck",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := filepath.Join(t.TempDir(), "urls.txt")
			if err := os.WriteFile(input, []byte(test.input), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			args := append([]string{"-interval=1ms"}, test.flags...)
			command := exec.CommandContext(ctx, binary, append(args, input)...)
			var stdout, stderr bytes.Buffer
			command.Stdout = &stdout
			command.Stderr = &stderr
			err := command.Run()
			if ctx.Err() != nil {
				t.Fatalf("CLI did not exit: %v", ctx.Err())
			}
			exitCode := 0
			if err != nil {
				var exitError *exec.ExitError
				if !errors.As(err, &exitError) {
					t.Fatal(err)
				}
				exitCode = exitError.ExitCode()
			}
			if exitCode != test.exitCode || !strings.Contains(stderr.String(), test.stderr) {
				t.Fatalf("exit=%d stderr=%q, want exit=%d containing %q", exitCode, stderr.String(), test.exitCode, test.stderr)
			}

			var outcomes []string
			for _, line := range strings.Split(strings.TrimSpace(stdout.String()), "\n") {
				if line == "" {
					continue
				}
				var result Result
				if err := json.Unmarshal([]byte(line), &result); err != nil {
					t.Fatalf("invalid JSON result %q: %v", line, err)
				}
				outcomes = append(outcomes, result.Outcome)
			}
			if test.outcomes != nil && !reflect.DeepEqual(outcomes, test.outcomes) {
				t.Fatalf("outcomes=%v, want %v", outcomes, test.outcomes)
			}
		})
	}
}
