package sqlitestore

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"github.com/konglong87/super-niuma/internal/super/control"
)

func TestControlIngressIndependentIdentitiesAndRestart(t *testing.T) {
	s, path := openControlTestStore(t)
	ctx := context.Background()
	r := controlFixture("inbound", "tenant", "/work", false)
	in := control.Ingress{Account: "account", EventID: "e1", MessageID: "m1", Nonce: "n1", ContentHash: control.Digest("payload")}
	original, created, err := s.CreateRun(ctx, r, &in)
	if err != nil || !created {
		t.Fatalf("admit: %v %v", created, err)
	}
	// A provider may resend one message under a new event/nonce. Every alias
	// remains durable, so a later replay cannot become a second run.
	variants := []control.Ingress{in, in, in}
	variants[0].EventID = "e2"
	variants[0].Nonce = "n2"
	variants[1].MessageID = "m2"
	variants[1].Nonce = "n3"
	variants[2].EventID = "e3"
	variants[2].MessageID = "m3" // nonce-only replay
	for i, v := range variants {
		retry := copyControlRun(r)
		retry.ID = fmt.Sprintf("retry-%d", i)
		retry.RequestKey = fmt.Sprintf("key-%d", i)
		got, created, err := s.CreateRun(ctx, retry, &v)
		if err != nil || created || got.ID != original.ID {
			t.Fatalf("alias %d: %v %v", i, created, err)
		}
	}
	if controlCount(t, s, "super_ingress") != 9 || controlCount(t, s, "super_runs") != 1 || controlEventCount(t, s) != 1 {
		t.Fatal("replay duplicated work or lost identities")
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	retry := copyControlRun(r)
	retry.ID = "after-restart"
	retry.RequestKey = "after-restart"
	got, created, err := s.CreateRun(ctx, retry, &variants[0])
	if err != nil || created || got.ID != original.ID {
		t.Fatalf("restart replay: %v %v", created, err)
	}
	conflict := variants[0]
	conflict.ContentHash = control.Digest("tampered")
	if _, _, err = s.CreateRun(ctx, retry, &conflict); !errors.Is(err, control.ErrConflict) {
		t.Fatalf("hash conflict: %v", err)
	}
	retry.Scope = "other-tenant"
	if _, _, err = s.CreateRun(ctx, retry, &in); !errors.Is(err, control.ErrDenied) {
		t.Fatalf("scope replay leaked: %v", err)
	}
	retry.Scope = r.Scope
	retry.Owner = "other-owner"
	if _, _, err = s.CreateRun(ctx, retry, &in); !errors.Is(err, control.ErrDenied) {
		t.Fatalf("owner replay leaked: %v", err)
	}
}
func TestControlIngressAccountAndKindIsolation(t *testing.T) {
	s, _ := openControlTestStore(t)
	ctx := context.Background()
	for i, account := range []string{"account-a", "account-b"} {
		r := controlFixture(fmt.Sprintf("r%d", i), fmt.Sprintf("scope%d", i), "/work", false)
		in := control.Ingress{Account: account, EventID: "same", MessageID: "same", Nonce: "same", ContentHash: control.Digest("payload")}
		if _, created, err := s.CreateRun(ctx, r, &in); err != nil || !created {
			t.Fatalf("account/kind collision: %v %v", created, err)
		}
	}
	if controlCount(t, s, "super_ingress") != 6 {
		t.Fatal("dedup domains collapsed")
	}
}
func TestControlIngressConflictingExistingRuns(t *testing.T) {
	s, _ := openControlTestStore(t)
	ctx := context.Background()
	base := controlFixture("r1", "tenant", "/work", false)
	a := control.Ingress{Account: "account", EventID: "e1", MessageID: "m1", ContentHash: control.Digest("payload")}
	b := a
	b.EventID = "e2"
	b.MessageID = "m2"
	if _, _, err := s.CreateRun(ctx, base, &a); err != nil {
		t.Fatal(err)
	}
	next := copyControlRun(base)
	next.ID = "r2"
	next.RequestKey = "r2"
	if _, _, err := s.CreateRun(ctx, next, &b); err != nil {
		t.Fatal(err)
	}
	mixed := a
	mixed.MessageID = b.MessageID
	mixed.Nonce = "unused-nonce"
	next.ID = "r3"
	next.RequestKey = "r3"
	if _, _, err := s.CreateRun(ctx, next, &mixed); !errors.Is(err, control.ErrConflict) {
		t.Fatalf("mixed receipts: %v", err)
	}
	if controlCount(t, s, "super_ingress") != 4 {
		t.Fatal("conflict consumed nonce")
	}
}
func TestControlIngressConcurrentAdmission(t *testing.T) {
	first, path := openControlTestStore(t)
	second, err := Open(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	base := controlFixture("first", "tenant", "/work", false)
	in := control.Ingress{Account: "account", EventID: "event", MessageID: "message", Nonce: "nonce", ContentHash: control.Digest("payload")}
	type result struct {
		r       control.Run
		created bool
		err     error
	}
	results := make(chan result, 16)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r := copyControlRun(base)
			r.ID = fmt.Sprintf("r%d", i)
			r.RequestKey = fmt.Sprintf("k%d", i)
			<-start
			got, created, err := []*Store{first, second}[i%2].CreateRun(context.Background(), r, &in)
			results <- result{got, created, err}
		}(i)
	}
	close(start)
	wg.Wait()
	close(results)
	count := 0
	id := ""
	for x := range results {
		if x.err != nil {
			t.Fatal(x.err)
		}
		if x.created {
			count++
		}
		if id == "" {
			id = x.r.ID
		}
		if id != x.r.ID {
			t.Fatal("multiple receipt roots")
		}
	}
	if count != 1 || controlEventCount(t, first) != 1 || controlCount(t, first, "super_ingress") != 3 {
		t.Fatalf("admission winners=%d", count)
	}
}
func TestControlIngressFailureRollsBackRun(t *testing.T) {
	s, _ := openControlTestStore(t)
	if _, err := s.db.Exec(`CREATE TRIGGER fail_control_inbox BEFORE INSERT ON super_ingress WHEN NEW.kind='nonce' BEGIN SELECT RAISE(ABORT,'SECRET'); END`); err != nil {
		t.Fatal(err)
	}
	r := controlFixture("inbox-fail", "tenant", "/work", false)
	in := control.Ingress{Account: "account", EventID: "e", MessageID: "m", Nonce: "n", ContentHash: control.Digest("payload")}
	if _, _, err := s.CreateRun(context.Background(), r, &in); !errors.Is(err, control.ErrUnavailable) {
		t.Fatal(err)
	}
	if controlCount(t, s, "super_runs") != 0 || controlCount(t, s, "super_ingress") != 0 || controlEventCount(t, s) != 0 {
		t.Fatal("partial inbox transaction")
	}
}
