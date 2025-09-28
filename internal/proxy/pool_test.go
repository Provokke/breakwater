package proxy

import "testing"

func TestPoolRejectsBadInput(t *testing.T) {
	if _, err := NewPool(nil); err == nil {
		t.Fatal("expected an error for no upstreams")
	}
	if _, err := NewPool([]string{"not a url"}); err == nil {
		t.Fatal("expected an error for a bad url")
	}
	if _, err := NewPool([]string{"/relative"}); err == nil {
		t.Fatal("expected an error for a relative url")
	}
}

func TestPoolRoundRobin(t *testing.T) {
	pool, err := NewPool([]string{"http://a:1", "http://b:2", "http://c:3"})
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for i := 0; i < 6; i++ {
		got = append(got, pool.Next().URL.Host)
	}
	want := []string{"a:1", "b:2", "c:3", "a:1", "b:2", "c:3"}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("order = %v, want %v", got, want)
		}
	}
}

func TestPoolSkipsUnhealthy(t *testing.T) {
	pool, _ := NewPool([]string{"http://a:1", "http://b:2", "http://c:3"})
	pool.All()[1].SetHealthy(false)
	for i := 0; i < 6; i++ {
		if host := pool.Next().URL.Host; host == "b:2" {
			t.Fatal("unhealthy upstream was chosen")
		}
	}
	if pool.HealthyCount() != 2 {
		t.Fatalf("healthy = %d", pool.HealthyCount())
	}
}

func TestPoolReturnsNilWhenAllDown(t *testing.T) {
	pool, _ := NewPool([]string{"http://a:1", "http://b:2"})
	for _, u := range pool.All() {
		u.SetHealthy(false)
	}
	if pool.Next() != nil {
		t.Fatal("expected no upstream")
	}
}

func TestPoolRecovers(t *testing.T) {
	pool, _ := NewPool([]string{"http://a:1"})
	pool.All()[0].SetHealthy(false)
	pool.All()[0].SetHealthy(true)
	if pool.Next() == nil {
		t.Fatal("upstream should be back")
	}
}
