package background

import (
	"errors"
	"io"
	"os"
	"syscall"
	"time"
)

// MaxOutputChunkBytes bounds one ReadNewOutput call. The read cursor only
// advances past bytes actually returned, so a longer log is drained across
// several calls instead of being silently dropped by the caller's own result
// truncation.
const MaxOutputChunkBytes = 30_000

// OutputChunk is the incremental view of a background job's log.
type OutputChunk struct {
	Job *Job
	// Text holds the bytes appended since the previous ReadNewOutput call.
	Text string
	// Offset is the log position after this read.
	Offset int64
	// MoreOutput reports that the log already holds bytes beyond this chunk.
	MoreOutput bool
	// Running reports whether the job's process is still alive.
	Running bool
	// Restarted reports that the log shrank (truncated or rotated) and the
	// cursor was reset, so Text restarts from the beginning of the file.
	Restarted bool
}

// ReadNewOutput returns the log bytes appended since the previous call and
// advances the job's persisted read cursor. It reports ok=false when no job
// with that id exists.
func (s Store) ReadNewOutput(id string) (OutputChunk, bool, error) {
	job, ok, err := s.Find(id)
	if err != nil || !ok {
		return OutputChunk{}, ok, err
	}
	chunk := OutputChunk{Offset: job.OutputOffset, Running: job.Running()}
	if job.LogPath == "" {
		chunk.Job = &job
		return chunk, true, nil
	}
	text, offset, more, restarted, err := readLogFrom(job.LogPath, job.OutputOffset)
	if err != nil {
		return OutputChunk{}, true, err
	}
	chunk.Text, chunk.Offset, chunk.MoreOutput, chunk.Restarted = text, offset, more, restarted
	if offset != job.OutputOffset {
		updated, found, err := s.update(id, func(job *Job) { job.OutputOffset = offset })
		if err != nil {
			return OutputChunk{}, true, err
		}
		if found {
			job = updated
			chunk.Running = job.Running()
		}
	}
	chunk.Job = &job
	return chunk, true, nil
}

// readLogFrom reads at most MaxOutputChunkBytes starting at offset. A file
// shorter than offset means the log was truncated or rotated, so the read
// restarts from the beginning rather than reporting "no new output" forever.
func readLogFrom(path string, offset int64) (text string, next int64, more bool, restarted bool, err error) {
	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return "", offset, false, false, nil
		}
		return "", offset, false, false, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", offset, false, false, err
	}
	size := info.Size()
	if offset > size {
		offset = 0
		restarted = true
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return "", offset, false, restarted, err
	}
	data, err := io.ReadAll(io.LimitReader(file, MaxOutputChunkBytes))
	if err != nil {
		return "", offset, false, restarted, err
	}
	next = offset + int64(len(data))
	return string(data), next, next < size, restarted, nil
}

// Running reports whether the job's process is still alive. The recorded
// status alone is not enough: a job whose supervising process died keeps
// status "running" forever, so the PID is probed as well.
func (j Job) Running() bool {
	if terminalStatus(j.Status) {
		return false
	}
	return processAlive(j.PID)
}

// TerminateOutcome names what Terminate did, so callers can tell an actual
// kill from a job that had already exited from an unknown id.
type TerminateOutcome string

const (
	TerminateKilled     TerminateOutcome = "killed"
	TerminateNotRunning TerminateOutcome = "not_running"
	TerminateNotFound   TerminateOutcome = "not_found"
)

// Terminate kills the job's process and marks it killed.
func (s Store) Terminate(id string) (Job, TerminateOutcome, error) {
	outcome := TerminateNotRunning
	pid := 0
	job, ok, err := s.update(id, func(job *Job) {
		if terminalStatus(job.Status) {
			return
		}
		if processAlive(job.PID) {
			pid = job.PID
			outcome = TerminateKilled
		}
		now := time.Now().UTC()
		job.Status = "killed"
		job.ExitCode = -1
		job.FinishedAt = &now
	})
	if err != nil {
		return Job{}, "", err
	}
	if !ok {
		return Job{}, TerminateNotFound, nil
	}
	if pid > 0 {
		if proc, err := os.FindProcess(pid); err == nil {
			_ = proc.Kill()
		}
	}
	return job, outcome, nil
}

// processAlive probes whether pid still exists using a signal-0 send, which
// never affects the target process.
func processAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	err = proc.Signal(syscall.Signal(0))
	if err == nil {
		return true
	}
	// EPERM means the process exists but belongs to another user.
	return !errors.Is(err, os.ErrProcessDone) && !errors.Is(err, syscall.ESRCH)
}
