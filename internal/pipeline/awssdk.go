package pipeline

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"syscall"
)

// checksumWarnSubstring is the marker every aws-sdk-go-v2 line we want
// to drop contains. The full line shipped by the SDK reads
//
//	SDK 2026/05/24 14:39:14 WARN Response has no supported checksum. Not validating response payload.
//
// — substring match is good enough; the SDK never logs the same
// phrase for anything else, and we accept the (vanishingly small)
// risk of dropping a future unrelated line that happens to contain
// it.
const checksumWarnSubstring = "Response has no supported checksum"

// silenceOnce guards against accidental double-install (e.g. a test
// calling the function alongside the real main). The second call is
// a no-op.
var silenceOnce sync.Once

// SilenceSDKChecksumWarnings wraps the process's stderr (fd 2) with a
// filtering pipe that drops lines containing
// "Response has no supported checksum" before they reach the real
// stderr. Everything else is forwarded byte-for-byte.
//
// Why this exists: aws-sdk-go-v2 logs a WARN on every S3 GetObject
// response that lacks one of its supported checksum headers. MinIO
// (our colo galexie backend) never sends those headers, so *every*
// ledger read triggers the line. ratesengine-indexer's live tail
// reads ~1 ledger/5s so the noise is trivial; verify-archive's
// 12-way parallel walk does ~50k ledgers/s and floods journald
// (~22k WARN/30s observed during r1 bootstrap, ballooning
// /tmp/va-full.log to 1.65 GB and burying the real verify-archive
// failure under noise journald then rate-dropped).
//
// The previous attempt (rc.72: QuietS3ChecksumWarnings) set
// AWS_RESPONSE_CHECKSUM_VALIDATION=when_required so the SDK's
// default-config layer would skip the validation attempt entirely.
// That fix is a no-op for our use because
// go-stellar-sdk/support/datastore/s3.go:161 hardcodes
//
//	ChecksumMode: types.ChecksumModeEnabled
//
// on every GetObjectInput, overriding whatever the env-var default
// produced. The upstream-respect path is to fix that line in
// go-stellar-sdk; until that lands, stderr filtering is the
// reliable workaround.
//
// Mechanism:
//
//  1. dup the current fd 2 to a fresh fd (the "real stderr").
//  2. create a pipe; dup2 the write end onto fd 2 so every
//     subsequent write — including the SDK's default logger which
//     was bound to os.Stderr at config-load time — flows into our
//     reader.
//  3. spin a goroutine that scans the reader line-by-line, drops
//     lines containing checksumWarnSubstring, and forwards the
//     rest to the real stderr.
//
// Constraints honoured:
//
//   - Must run BEFORE config.LoadDefaultConfig — the SDK captures
//     os.Stderr into logging.NewStandardLogger at that point. Call
//     this from the first line of main().
//   - Fail-soft: any error in pipe/dup2 logs to the original
//     stderr and returns; the binary keeps running with noisy
//     stderr, never crashes at startup over a logging filter.
//   - sync.Once-guarded; second call is a no-op.
//   - The goroutine drains the pipe continuously, so a slow real
//     stderr (e.g. journald rate-limit) can't deadlock the
//     writer side beyond the pipe buffer.
func SilenceSDKChecksumWarnings() {
	silenceOnce.Do(func() {
		if err := installStderrFilter(); err != nil {
			fmt.Fprintf(os.Stderr, "SilenceSDKChecksumWarnings: install failed, continuing with raw stderr: %v\n", err)
		}
	})
}

// installStderrFilter is the testable core. It dup-and-replaces fd
// 2, then launches the filter goroutine. Returns an error if any
// syscall fails; callers should fail-soft.
func installStderrFilter() error {
	return installStderrFilterTo(filteringForwarder)
}

// installStderrFilterTo is the test seam: the consumer function
// receives the pipe reader + the real-stderr writer and is
// responsible for draining the reader to completion.
func installStderrFilterTo(consume func(r io.Reader, realStderr *os.File)) error {
	// Duplicate fd 2 to a fresh fd so we can keep writing to the
	// real stderr after we overwrite fd 2 with the pipe.
	savedFD, err := syscall.Dup(int(os.Stderr.Fd()))
	if err != nil {
		return fmt.Errorf("dup fd 2: %w", err)
	}

	pr, pw, err := os.Pipe()
	if err != nil {
		_ = syscall.Close(savedFD)
		return fmt.Errorf("pipe: %w", err)
	}

	// Replace fd 2 with the pipe's writer end. Every existing
	// reference to fd 2 (including the aws-sdk-go-v2 default
	// logger bound to os.Stderr) is redirected through us from
	// here on.
	if err := syscall.Dup2(int(pw.Fd()), int(os.Stderr.Fd())); err != nil {
		_ = pr.Close()
		_ = pw.Close()
		_ = syscall.Close(savedFD)
		return fmt.Errorf("dup2 onto fd 2: %w", err)
	}

	// pw still holds a duplicate FD pointing at the pipe's writer
	// end; close it so fd 2 is the *only* writer reference. That
	// way, when something (e.g. a test or graceful shutdown)
	// closes fd 2, the reader-side goroutine sees EOF and exits
	// cleanly. Real production never closes fd 2, so this only
	// affects test/shutdown paths.
	_ = pw.Close()

	realStderr := os.NewFile(uintptr(savedFD), "stderr-original")

	go consume(pr, realStderr)
	return nil
}

// filteringForwarder scans `r` line-by-line, drops every line that
// contains checksumWarnSubstring, and writes the rest verbatim to
// `realStderr` (each followed by a newline — Scanner strips it).
// Exits cleanly on scanner error or EOF.
func filteringForwarder(r io.Reader, realStderr *os.File) {
	scanner := bufio.NewScanner(r)
	// SDK lines fit in default 64 KiB; bump the cap defensively to
	// 1 MiB so a stray giant line can never panic the goroutine.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if strings.Contains(string(line), checksumWarnSubstring) {
			continue
		}
		_, _ = realStderr.Write(line)
		_, _ = realStderr.Write([]byte{'\n'})
	}
}
