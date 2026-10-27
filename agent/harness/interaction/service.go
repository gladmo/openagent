package interaction

// service.go is the Service Definition role: the ordered answerer chain
// with fail-closed semantics. Answerers (providers) register with a
// disposer; consumers call Ask. The chain is snapshotted when an ask
// starts: registrations during a blocked ask apply to the next one.
// Blocking waits belong to answerers — they select on the context's
// abort signal — so the service keeps no pending registry.

import (
	"errors"
	"sync"

	"github.com/gladmo/openagent/agent/harness"
)

// ErrDeclined is the sentinel an answerer returns when it cannot serve
// a request; the chain tries the next answerer.
var ErrDeclined = errors.New("interaction: answerer declined the request")

// Answerer serves one ask request; it blocks until answered, declined,
// or aborted (by selecting on the context's abort signal).
type Answerer interface {
	Ask(request *AskRequest, ctx harness.Context) (*Answer, error)
}

// AnswererFunc adapts a function to the Answerer interface.
type AnswererFunc func(request *AskRequest, ctx harness.Context) (*Answer, error)

// Ask implements Answerer.
func (f AnswererFunc) Ask(request *AskRequest, ctx harness.Context) (*Answer, error) {
	return f(request, ctx)
}

type registeredAnswerer struct {
	id       int64
	answerer Answerer
}

// Service owns the ordered answerer chain for one embedder.
type Service struct {
	mu        sync.Mutex
	nextID    int64
	answerers []registeredAnswerer
}

// NewService builds an empty service; Ask fails closed until an
// answerer registers.
func NewService() *Service { return &Service{} }

// Register appends one answerer; the returned remove function
// unregisters it and is idempotent.
func (s *Service) Register(answerer Answerer) func() {
	s.mu.Lock()
	s.nextID++
	id := s.nextID
	s.answerers = append(s.answerers, registeredAnswerer{id: id, answerer: answerer})
	s.mu.Unlock()
	removed := false
	return func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		if removed {
			return
		}
		removed = true
		for i, candidate := range s.answerers {
			if candidate.id == id {
				s.answerers = append(s.answerers[:i], s.answerers[i+1:]...)
				return
			}
		}
	}
}

// snapshot copies the chain for one ask.
func (s *Service) snapshot() []Answerer {
	s.mu.Lock()
	defer s.mu.Unlock()
	chain := make([]Answerer, 0, len(s.answerers))
	for _, registered := range s.answerers {
		chain = append(chain, registered.answerer)
	}
	return chain
}

// aborted reports the context's abort state; nil contexts and nil
// signals never count as aborted.
func aborted(ctx harness.Context) bool {
	if ctx == nil {
		return false
	}
	signal := ctx.AbortSignal()
	return signal != nil && signal.Aborted()
}

// Ask validates the request, then walks the answerer chain in
// registration order: the first answer wins, and every returned answer
// is validated against the request. An empty chain, or every answerer
// declining, fails closed with NO_ANSWERER. An aborted context fails
// with ASK_ABORTED — checked before the chain and after every answerer
// return, so an answer landing after abort does not win.
func (s *Service) Ask(request *AskRequest, ctx harness.Context) (*Answer, error) {
	if err := ValidateAskRequest(request); err != nil {
		return nil, err
	}
	if aborted(ctx) {
		return nil, AskAborted()
	}
	for _, answerer := range s.snapshot() {
		answer, err := answerer.Ask(request, ctx)
		if aborted(ctx) {
			return nil, AskAborted()
		}
		if err != nil {
			if errors.Is(err, ErrDeclined) {
				continue
			}
			return nil, err
		}
		if answer == nil {
			return nil, &InteractionError{
				Code:    CodeBadAnswer,
				Message: "the answerer returned no answer",
			}
		}
		if err := ValidateAnswer(request, answer); err != nil {
			return nil, err
		}
		return answer, nil
	}
	return nil, &InteractionError{
		Code:    CodeNoAnswerer,
		Message: "no answerer accepted the request",
	}
}
