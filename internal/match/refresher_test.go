package match

import (
	"context"
	"errors"
	"testing"
)

type fakeSource struct {
	engineers []Engineer
	err       error
}

func (f *fakeSource) Engineers(context.Context) ([]Engineer, error) { return f.engineers, f.err }

func TestRefresherKeepsLastGoodSnapshot(t *testing.T) {
	src := &fakeSource{engineers: bench()}
	r := NewRefresher(src, 0)
	if r.Index() != nil {
		t.Fatal("index should be nil before the first load")
	}
	if err := r.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	good := r.Index()

	src.err = errors.New("dynamodb unavailable")
	if err := r.Load(context.Background()); err == nil {
		t.Fatal("want the error back")
	}
	if r.Index() != good || r.Failures() != 1 {
		t.Fatalf("a failed refresh must keep the previous snapshot (failures=%d)", r.Failures())
	}
}
