package policyapply

import (
	"bytes"
	"context"
	"testing"

	"github.com/nats-io/nats.go/jetstream"
)

func TestKVLedgerStoresDocuments(t *testing.T) {
	e := newKVEnv(t)
	l := e.ledger(KVLedgerOptions{})
	ctx := context.Background()
	if doc, err := l.InForceDocument(ctx); err != nil || doc != nil {
		t.Fatalf("empty ledger: doc %q, err %v", doc, err)
	}
	in := RunInput{Ledger: l, Received: []byte(`{"generation":1}`)}
	if err := setPending(in, testRecord(1)); err != nil {
		t.Fatal(err)
	}
	if err := commit(in, 0, testRecord(1)); err != nil {
		t.Fatal(err)
	}
	doc, err := l.InForceDocument(ctx)
	if err != nil || !bytes.Equal(doc, in.Received) {
		t.Fatalf("in force document %q, err %v", doc, err)
	}
	v, _, err := l.read(ctx)
	if err != nil || !bytes.Equal(v.PendingDocument, in.Received) {
		t.Fatalf("pending document %q, err %v", v.PendingDocument, err)
	}
	if err := l.ClearPending(); err != nil {
		t.Fatal(err)
	}
	if v, _, _ = l.read(ctx); v.PendingDocument != nil {
		t.Fatalf("pending document not cleared: %q", v.PendingDocument)
	}
	// A status write keeps the document; a commit without one clears it.
	if err := l.WriteStatus(ctx, newStatus(testRecord(1), StateApplied, nil)); err != nil {
		t.Fatal(err)
	}
	if doc, _ = l.InForceDocument(ctx); !bytes.Equal(doc, in.Received) {
		t.Fatalf("status write lost the document: %q", doc)
	}
	if err := l.CompareAndCommit(1, testRecord(2)); err != nil {
		t.Fatal(err)
	}
	if doc, _ = l.InForceDocument(ctx); doc != nil {
		t.Fatalf("commit without a document kept %q", doc)
	}
}

func TestKVLedgerReadOnlyWritesNothing(t *testing.T) {
	e := newKVEnv(t)
	js, err := jetstream.New(connectJS(t, e.url))
	if err != nil {
		t.Fatal(err)
	}
	ro, err := OpenKVLedgerReadOnly(context.Background(), js, kvRange, kvAccount)
	if err != nil {
		t.Fatal(err)
	}
	if ro.Account() != kvAccount {
		t.Fatalf("account %q", ro.Account())
	}
	if st, err := ro.LatestStatus(context.Background()); err != nil || st != nil {
		t.Fatalf("status %v, err %v", st, err)
	}
	if _, err := ro.InForceDocument(context.Background()); err != nil {
		t.Fatal(err)
	}
	keys, _ := e.kv.Keys(context.Background())
	if len(keys) != 0 {
		t.Fatalf("a read-only ledger wrote keys %v", keys)
	}
}
