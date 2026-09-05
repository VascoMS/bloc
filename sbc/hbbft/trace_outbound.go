package hbbft

import "sync"

// ACSSendToken binds one scheduled ACS send to the recorder active when the
// message was emitted. Complete is safe to call more than once.
type ACSSendToken struct {
	once     sync.Once
	complete func(ACSSendObservation)
}

func (t *ACSSendToken) Complete(observation ACSSendObservation) {
	if t == nil {
		return
	}
	t.once.Do(func() {
		if t.complete != nil {
			t.complete(observation)
		}
	})
}

func (r *traceRecorder) beginMessageOutbound(subtype ACSMessageSubtype) *ACSSendToken {
	if r == nil || !r.enabled {
		return nil
	}
	r.mu.Lock()
	if r.trace.Transport.Sealed {
		r.mu.Unlock()
		return nil
	}
	entry, ok := r.trace.Messages[subtype]
	if !ok {
		r.mu.Unlock()
		return nil
	}
	entry.ScheduledCount++
	r.trace.Messages[subtype] = entry
	r.mu.Unlock()
	return &ACSSendToken{complete: func(observation ACSSendObservation) {
		r.completeMessageOutbound(subtype, observation)
	}}
}

func (r *traceRecorder) completeMessageOutbound(subtype ACSMessageSubtype, observation ACSSendObservation) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.trace.Messages[subtype]
	if !ok || entry.TerminalCount >= entry.ScheduledCount {
		return
	}
	entry.TerminalCount++
	recordCompletedSend(&entry, observation)
	r.trace.Messages[subtype] = entry
	if r.trace.Transport.Sealed && r.allMessagesTerminalLocked() {
		r.trace.Transport.Finalized = true
	}
}

func (r *traceRecorder) sealMessageOutbound() {
	if r == nil || !r.enabled {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.trace.Transport.Sealed {
		return
	}
	for _, subtype := range acsMessageSubtypes {
		entry := r.trace.Messages[subtype]
		entry.PendingAtDecision = entry.ScheduledCount - entry.TerminalCount
		r.trace.Messages[subtype] = entry
	}
	r.trace.Transport.Sealed = true
	r.trace.Transport.Finalized = r.allMessagesTerminalLocked()
}

func (r *traceRecorder) allMessagesTerminalLocked() bool {
	for _, subtype := range acsMessageSubtypes {
		entry := r.trace.Messages[subtype]
		if entry.TerminalCount != entry.ScheduledCount {
			return false
		}
	}
	return true
}

func (r *traceRecorder) traceFinalized() bool {
	if r == nil || !r.enabled {
		return true
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.trace.Transport.Finalized
}
