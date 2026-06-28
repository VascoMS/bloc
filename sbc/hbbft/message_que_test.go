package hbbft

import (
	"sync"
	"testing"
)

func TestMessageQueConcurrentDrainDoesNotLoseMessages(t *testing.T) {
	const producers = 16
	const perProducer = 250
	q := newMessageQue()
	var producersWG sync.WaitGroup
	for producer := 0; producer < producers; producer++ {
		producersWG.Add(1)
		go func(producer int) {
			defer producersWG.Done()
			for sequence := 0; sequence < perProducer; sequence++ {
				q.addMessage(producer*perProducer+sequence, uint64(producer))
			}
		}(producer)
	}

	producerDone := make(chan struct{})
	go func() {
		producersWG.Wait()
		close(producerDone)
	}()

	seen := make(map[int]bool, producers*perProducer)
	for {
		for _, message := range q.messages() {
			id := message.Payload.(int)
			if seen[id] {
				t.Fatalf("message %d drained more than once", id)
			}
			seen[id] = true
		}
		select {
		case <-producerDone:
			for _, message := range q.messages() {
				id := message.Payload.(int)
				if seen[id] {
					t.Fatalf("message %d drained more than once", id)
				}
				seen[id] = true
			}
			if len(seen) != producers*perProducer {
				t.Fatalf("drained %d messages, want %d", len(seen), producers*perProducer)
			}
			return
		default:
		}
	}
}
