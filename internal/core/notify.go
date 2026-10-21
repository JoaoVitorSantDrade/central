package core

import "sync"

// Notifier acorda todos os que esperam a próxima mudança.
type Notifier struct {
	mu sync.Mutex
	ch chan struct{}
}

// NewNotifier cria um Notifier pronto para uso.
func NewNotifier() *Notifier {
	return &Notifier{ch: make(chan struct{})}
}

// Wait devolve um canal que é fechado no próximo Broadcast.
func (n *Notifier) Wait() <-chan struct{} {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.ch
}

// Broadcast acorda todos os que obtiveram Wait antes desta chamada.
func (n *Notifier) Broadcast() {
	n.mu.Lock()
	defer n.mu.Unlock()
	close(n.ch)
	n.ch = make(chan struct{})
}
