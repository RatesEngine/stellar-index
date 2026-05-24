package pipeline

import (
	"bytes"
	"io"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
)

// TestFilteringForwarder exercises the line-filter directly, without
// touching real fd 2. The forwarder reads from any io.Reader and
// writes filtered lines to its supplied *os.File; an os.Pipe lets
// us assert on what comes out.
func TestFilteringForwarder(t *testing.T) {
	cases := []struct {
		name    string
		input   string
		wantOut string
	}{
		{
			name:    "drops the SDK checksum WARN",
			input:   "SDK 2026/05/24 14:39:14 WARN Response has no supported checksum. Not validating response payload.\n",
			wantOut: "",
		},
		{
			name:    "passes through unrelated SDK lines",
			input:   "SDK 2026/05/24 14:39:14 INFO Loaded config from /etc/foo\n",
			wantOut: "SDK 2026/05/24 14:39:14 INFO Loaded config from /etc/foo\n",
		},
		{
			name:    "mixed stream: drops only matching lines",
			input:   "line one\nSDK WARN Response has no supported checksum. trailing\nline three\n",
			wantOut: "line one\nline three\n",
		},
		{
			name:    "no trailing newline still flushes the line",
			input:   "single line no newline",
			wantOut: "single line no newline\n",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Pipe acts as the "real stderr"; we read from the
			// reader side after the forwarder finishes.
			pr, pw, err := os.Pipe()
			if err != nil {
				t.Fatalf("pipe: %v", err)
			}

			var wg sync.WaitGroup
			wg.Add(1)
			go func() {
				defer wg.Done()
				filteringForwarder(strings.NewReader(tc.input), pw)
				_ = pw.Close()
			}()

			var out bytes.Buffer
			if _, err := io.Copy(&out, pr); err != nil {
				t.Fatalf("copy: %v", err)
			}
			wg.Wait()

			if got := out.String(); got != tc.wantOut {
				t.Fatalf("filtered output mismatch\n got: %q\nwant: %q", got, tc.wantOut)
			}
		})
	}
}

// TestInstallStderrFilterTo exercises the full install path:
// dup-replace fd 2, then verify a write to fd 2 is routed through
// the supplied consumer.
func TestInstallStderrFilterTo(t *testing.T) {
	// Save the real fd 2 so we can restore it after the test —
	// otherwise subsequent tests' t.Log/t.Error output goes to
	// the pipe and vanishes.
	originalStderrCopy, err := syscall.Dup(int(os.Stderr.Fd()))
	if err != nil {
		t.Fatalf("dup original stderr: %v", err)
	}
	t.Cleanup(func() {
		_ = syscall.Dup2(originalStderrCopy, int(os.Stderr.Fd()))
		_ = syscall.Close(originalStderrCopy)
	})

	// Capture the routed bytes via a channel; the consume callback
	// reads the entire pipe and sends the result down the channel.
	got := make(chan []byte, 1)
	consume := func(r io.Reader, realStderr *os.File) {
		buf, err := io.ReadAll(r)
		if err != nil {
			t.Errorf("readall: %v", err)
		}
		got <- buf
		// realStderr is the dup of original fd 2; close it so the
		// FD doesn't leak.
		_ = realStderr.Close()
	}

	if err := installStderrFilterTo(consume); err != nil {
		t.Fatalf("install: %v", err)
	}

	// Write something to fd 2 — the SDK's default logger
	// ultimately writes to fd 2 via os.Stderr, which after dup2
	// is the pipe's write end.
	want := "hello from fd 2\n"
	if _, err := syscall.Write(int(os.Stderr.Fd()), []byte(want)); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Restore fd 2 so the only remaining holder of the pipe's
	// write end is the goroutine's reference (none — fd 2 was
	// the sole holder). dup2 closes the existing fd-2 target,
	// shutting the pipe writer; io.ReadAll then returns.
	if err := syscall.Dup2(originalStderrCopy, int(os.Stderr.Fd())); err != nil {
		t.Fatalf("dup2 restore: %v", err)
	}

	buf := <-got
	if string(buf) != want {
		t.Fatalf("routed bytes mismatch\n got: %q\nwant: %q", string(buf), want)
	}
}
