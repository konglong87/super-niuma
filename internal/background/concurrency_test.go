package background

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// crossProcessRootEnv hands the shared store root to the child half of
// TestStoreConcurrentCreatesAcrossProcesses.
const crossProcessRootEnv = "NIUMA_BACKGROUND_CROSS_PROCESS_ROOT"

// crossProcessCreates is how many jobs each child process appends.
const crossProcessCreates = 8

func TestStoreConcurrentCreatesKeepEveryJob(t *testing.T) {
	store := Store{Root: t.TempDir()}

	const writers = 16
	var wg sync.WaitGroup
	errs := make(chan error, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := store.CreateWithOptions(Options{Prompt: "job-" + strconv.Itoa(i)}); err != nil {
				errs <- err
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("CreateWithOptions: %v", err)
	}

	jobs, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(jobs) != writers {
		t.Fatalf("registry kept %d jobs, want %d: concurrent read-modify-write lost updates", len(jobs), writers)
	}
}

func TestStoreConcurrentUpdatesKeepEveryChange(t *testing.T) {
	store := Store{Root: t.TempDir()}

	const jobCount = 12
	ids := make([]string, 0, jobCount)
	for i := 0; i < jobCount; i++ {
		job, err := store.CreateWithOptions(Options{Prompt: "job-" + strconv.Itoa(i)})
		if err != nil {
			t.Fatalf("CreateWithOptions: %v", err)
		}
		ids = append(ids, job.ID)
	}

	var wg sync.WaitGroup
	errs := make(chan error, jobCount)
	for i, id := range ids {
		wg.Add(1)
		go func(i int, id string) {
			defer wg.Done()
			if _, _, err := store.MarkRunning(id, 1000+i); err != nil {
				errs <- err
			}
		}(i, id)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("MarkRunning: %v", err)
	}

	jobs, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(jobs) != jobCount {
		t.Fatalf("registry kept %d jobs, want %d", len(jobs), jobCount)
	}
	for _, job := range jobs {
		if job.PID == 0 {
			t.Fatalf("job %s lost its PID update: concurrent read-modify-write dropped it", job.ID)
		}
	}
}

// TestStoreListNeverReadsPartialFile pins that a reader concurrent with a
// writer sees either the old or the new registry, never a half-written one.
// The original symptom of TODO-111 was exactly this: "unexpected end of JSON
// input" from a reader that hit the window an O_TRUNC rewrite opens.
func TestStoreListNeverReadsPartialFile(t *testing.T) {
	store := Store{Root: t.TempDir()}

	// A large registry widens the window a truncating rewrite leaves open.
	const seeded = 200
	seed := make([]Job, 0, seeded)
	for i := 0; i < seeded; i++ {
		seed = append(seed, Job{
			ID:        "bg_seed" + strconv.Itoa(i),
			Prompt:    strings.Repeat("p", 500),
			Status:    "queued",
			CreatedAt: time.Unix(int64(i), 0).UTC(),
		})
	}
	if err := store.save(seed); err != nil {
		t.Fatalf("save: %v", err)
	}
	id := seed[0].ID

	const rounds = 60
	done := make(chan struct{})
	writeErr := make(chan error, 1)
	go func() {
		defer close(done)
		for i := 0; i < rounds; i++ {
			if _, _, err := store.MarkRunning(id, 4000+i); err != nil {
				writeErr <- err
				return
			}
		}
	}()

	reads := 0
	for {
		select {
		case <-done:
			select {
			case err := <-writeErr:
				t.Fatalf("MarkRunning: %v", err)
			default:
			}
			if reads == 0 {
				t.Fatal("reader never ran; the test proves nothing")
			}
			return
		default:
		}
		jobs, err := store.List()
		if err != nil {
			t.Fatalf("List read a partial registry after %d clean reads: %v", reads, err)
		}
		if len(jobs) != seeded {
			t.Fatalf("List saw %d jobs, want %d", len(jobs), seeded)
		}
		reads++
	}
}

// TestStoreConcurrentCreatesAcrossProcesses proves the lock is a file lock and
// not merely a process-local mutex: the registry lives in ~/.niuma-legacy and is
// shared by every niuma process on the machine.
func TestStoreConcurrentCreatesAcrossProcesses(t *testing.T) {
	root := t.TempDir()
	store := Store{Root: root}

	const children = 3
	const localCreates = 8

	var wg sync.WaitGroup
	childErrs := make(chan error, children)
	for i := 0; i < children; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cmd := exec.Command(os.Args[0], "-test.run=^TestStoreCreateJobsHelperProcess$")
			cmd.Env = append(os.Environ(), crossProcessRootEnv+"="+root)
			if out, err := cmd.CombinedOutput(); err != nil {
				childErrs <- &childError{err: err, out: string(out)}
			}
		}()
	}

	localErrs := make(chan error, localCreates)
	for i := 0; i < localCreates; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := store.CreateWithOptions(Options{Prompt: "parent-" + strconv.Itoa(i)}); err != nil {
				localErrs <- err
			}
		}(i)
	}
	wg.Wait()
	close(childErrs)
	close(localErrs)
	for err := range childErrs {
		t.Fatalf("child process: %v", err)
	}
	for err := range localErrs {
		t.Fatalf("CreateWithOptions: %v", err)
	}

	jobs, err := store.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := localCreates + children*crossProcessCreates
	if len(jobs) != want {
		t.Fatalf("registry kept %d jobs, want %d: writes from other processes were lost", len(jobs), want)
	}
}

type childError struct {
	err error
	out string
}

func (e *childError) Error() string { return e.err.Error() + "\n" + e.out }

// TestStoreCreateJobsHelperProcess is the child half of
// TestStoreConcurrentCreatesAcrossProcesses, not a test of its own. It is a
// Test function because re-executing the test binary is the only way to get a
// second process that links this package.
func TestStoreCreateJobsHelperProcess(t *testing.T) {
	root := os.Getenv(crossProcessRootEnv)
	if root == "" {
		t.Skip("child half of TestStoreConcurrentCreatesAcrossProcesses")
	}
	store := Store{Root: root}
	for i := 0; i < crossProcessCreates; i++ {
		if _, err := store.CreateWithOptions(Options{Prompt: "child-" + strconv.Itoa(i)}); err != nil {
			t.Fatalf("CreateWithOptions: %v", err)
		}
	}
}
