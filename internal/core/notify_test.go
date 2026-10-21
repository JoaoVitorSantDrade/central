package core

import (
	"sync"
	"testing"
	"time"
)

func TestBroadcastAcordaTodos(t *testing.T) {
	n := NewNotifier()
	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		ch := n.Wait()
		wg.Add(1)
		go func() {
			defer wg.Done()
			select {
			case <-ch:
			case <-time.After(2 * time.Second):
				t.Error("não acordou")
			}
		}()
	}
	n.Broadcast()
	wg.Wait()
	select {
	case <-n.Wait():
		t.Fatal("Wait() obtido depois do Broadcast já está fechado")
	default:
	}
}
