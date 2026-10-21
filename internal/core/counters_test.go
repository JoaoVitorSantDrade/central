package core

import (
	"errors"
	"sort"
	"sync"
	"testing"
)

func TestNextIDConcorrente(t *testing.T) {
	c, _ := newCoreClock(t)
	const n = 50
	ids := make([]string, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := c.NextID(ctx, "A1", "T", 1)
			if err != nil {
				t.Error(err)
			}
			ids[i] = id
		}()
	}
	wg.Wait()
	sort.Strings(ids)
	for i, id := range ids {
		want := "T-" + string(rune('0'+(i+1)/100)) + string(rune('0'+(i+1)/10%10)) + string(rune('0'+(i+1)%10))
		if id != want {
			t.Fatalf("ids[%d] = %q, esperava %q (todos: %v)", i, id, want, ids)
		}
	}
}

func TestNextIDMinimo(t *testing.T) {
	c, _ := newCoreClock(t)
	for _, tc := range []struct {
		minimo int
		want   string
	}{{115, "T-115"}, {1, "T-116"}, {10, "T-117"}} {
		id, err := c.NextID(ctx, "A1", "T", tc.minimo)
		if err != nil || id != tc.want {
			t.Fatalf("NextID(T, %d) = %q, %v; esperava %q", tc.minimo, id, err, tc.want)
		}
	}
	for _, p := range []string{"", "t", "TOOLONG", "T1"} {
		if _, err := c.NextID(ctx, "A1", p, 1); !errors.Is(err, ErrInvalid) {
			t.Errorf("prefixo %q: err = %v", p, err)
		}
	}
	if _, err := c.NextID(ctx, "A1", "T", 0); err != nil {
		t.Errorf("minimo 0 deve valer 1: %v", err)
	}
}

func TestNextIDGravaQuemPediu(t *testing.T) {
	c, _ := newCoreClock(t)
	if _, err := c.NextID(ctx, "A1", "T", 1); err != nil {
		t.Fatal(err)
	}
	if n := count(t, c, `SELECT count(*) FROM eventos WHERE tipo = 'id.gerado' AND quem = 'A1' AND ref = 'T-001'`); n != 1 {
		t.Fatalf("eventos id.gerado de A1 = %d", n)
	}
}
