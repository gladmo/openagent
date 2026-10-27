package interaction

import (
	"errors"
	"strings"
	"testing"

	"github.com/gladmo/openagent/abort"
	"github.com/gladmo/openagent/agent/harness"
	chordcontext "github.com/gladmo/openagent/chord/context"
)

func choiceQuestion() Question {
	return Question{
		ID: "confirm", Header: "Confirm", Question: "Proceed?",
		Options: []Option{{Label: "yes"}, {Label: "no"}},
	}
}

func freeTextQuestion() Question {
	return Question{ID: "name", Header: "Name", Question: "Which name?"}
}

func validRequest() *AskRequest {
	return &AskRequest{Questions: []Question{choiceQuestion(), freeTextQuestion()}}
}

func validAnswer() *Answer {
	return &Answer{Items: []AnswerItem{
		{QuestionID: "confirm", Labels: []string{"yes"}},
		{QuestionID: "name", Labels: []string{"openagent"}},
	}}
}

func interactionCode(t *testing.T, err error) string {
	t.Helper()
	var interactionErr *InteractionError
	if !errors.As(err, &interactionErr) {
		t.Fatalf("expected *InteractionError, got %T: %v", err, err)
	}
	return interactionErr.Code
}

// askCode runs one ask and reduces it to its error code.
func askCode(t *testing.T, service *Service, request *AskRequest, ctx harness.Context) string {
	t.Helper()
	_, err := service.Ask(request, ctx)
	return interactionCode(t, err)
}

func TestAskValidatesRequestShape(t *testing.T) {
	service := NewService()
	service.Register(AnswererFunc(func(*AskRequest, harness.Context) (*Answer, error) {
		return validAnswer(), nil
	}))
	ctx := harness.BackgroundContext

	if code := askCode(t, service, nil, ctx); code != CodeEmptyQuestions {
		t.Fatalf("nil request code = %s", code)
	}
	if code := askCode(t, service, &AskRequest{}, ctx); code != CodeEmptyQuestions {
		t.Fatalf("empty request code = %s", code)
	}
	noText := &AskRequest{Questions: []Question{{ID: "q"}}}
	if code := askCode(t, service, noText, ctx); code != CodeBadQuestion {
		t.Fatalf("missing text code = %s", code)
	}
	duplicate := &AskRequest{Questions: []Question{choiceQuestion(), {ID: "confirm", Question: "Again?"}}}
	if code := askCode(t, service, duplicate, ctx); code != CodeDuplicateQuestionID {
		t.Fatalf("duplicate id code = %s", code)
	}
}

func TestAskWalksChainInRegistrationOrder(t *testing.T) {
	service := NewService()
	var order []string
	service.Register(AnswererFunc(func(*AskRequest, harness.Context) (*Answer, error) {
		order = append(order, "first")
		return nil, ErrDeclined
	}))
	service.Register(AnswererFunc(func(*AskRequest, harness.Context) (*Answer, error) {
		order = append(order, "second")
		return validAnswer(), nil
	}))
	answer, err := service.Ask(validRequest(), harness.BackgroundContext)
	if err != nil {
		t.Fatal(err)
	}
	if len(answer.Items) != 2 || answer.Items[0].Labels[0] != "yes" {
		t.Fatalf("answer = %+v", answer)
	}
	if strings.Join(order, ",") != "first,second" {
		t.Fatalf("order = %v", order)
	}
}

func TestAskFailsClosedWithoutAnswerer(t *testing.T) {
	empty := NewService()
	if code := askCode(t, empty, validRequest(), harness.BackgroundContext); code != CodeNoAnswerer {
		t.Fatalf("empty service code = %s", code)
	}
	allDecline := NewService()
	allDecline.Register(AnswererFunc(func(*AskRequest, harness.Context) (*Answer, error) {
		return nil, ErrDeclined
	}))
	if code := askCode(t, allDecline, validRequest(), harness.BackgroundContext); code != CodeNoAnswerer {
		t.Fatalf("all-declined code = %s", code)
	}
}

func TestAskStopsAtFirstFailure(t *testing.T) {
	service := NewService()
	boom := errors.New("answerer exploded")
	reached := false
	service.Register(AnswererFunc(func(*AskRequest, harness.Context) (*Answer, error) {
		return nil, boom
	}))
	service.Register(AnswererFunc(func(*AskRequest, harness.Context) (*Answer, error) {
		reached = true
		return validAnswer(), nil
	}))
	if _, err := service.Ask(validRequest(), harness.BackgroundContext); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if reached {
		t.Fatal("a failed answerer must end the chain")
	}
}

func TestAskValidatesReturnedAnswer(t *testing.T) {
	cases := []struct {
		name   string
		answer *Answer
	}{
		{"missing question", &Answer{Items: []AnswerItem{{QuestionID: "confirm", Labels: []string{"yes"}}}}},
		{"unknown question", &Answer{Items: append(validAnswer().Items, AnswerItem{QuestionID: "ghost", Labels: []string{"x"}})}},
		{"unknown label", &Answer{Items: []AnswerItem{
			{QuestionID: "confirm", Labels: []string{"maybe"}},
			{QuestionID: "name", Labels: []string{"openagent"}},
		}}},
		{"several labels without multiSelect", &Answer{Items: []AnswerItem{
			{QuestionID: "confirm", Labels: []string{"yes", "no"}},
			{QuestionID: "name", Labels: []string{"openagent"}},
		}}},
		{"repeated label", &Answer{Items: []AnswerItem{
			{QuestionID: "confirm", Labels: []string{"yes", "yes"}},
			{QuestionID: "name", Labels: []string{"openagent"}},
		}}},
		{"free text with several labels", &Answer{Items: []AnswerItem{
			{QuestionID: "confirm", Labels: []string{"yes"}},
			{QuestionID: "name", Labels: []string{"a", "b"}},
		}}},
		{"no answer", nil},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			service := NewService()
			service.Register(AnswererFunc(func(*AskRequest, harness.Context) (*Answer, error) {
				return testCase.answer, nil
			}))
			if code := askCode(t, service, validRequest(), harness.BackgroundContext); code != CodeBadAnswer {
				t.Fatalf("code = %s", code)
			}
		})
	}
}

func TestAskAcceptsMultiSelectAnswer(t *testing.T) {
	request := &AskRequest{Questions: []Question{{
		ID: "pick", Question: "Which?", MultiSelect: true,
		Options: []Option{{Label: "a"}, {Label: "b"}},
	}}}
	service := NewService()
	service.Register(AnswererFunc(func(*AskRequest, harness.Context) (*Answer, error) {
		return &Answer{Items: []AnswerItem{{QuestionID: "pick", Labels: []string{"a", "b"}}}}, nil
	}))
	if _, err := service.Ask(request, harness.BackgroundContext); err != nil {
		t.Fatal(err)
	}
}

func TestAskAbortsBeforeTheChain(t *testing.T) {
	controller := abort.NewController()
	controller.Abort()
	ctx := chordcontext.WithAbortSignal(controller.Signal(), harness.BackgroundContext)
	service := NewService()
	called := false
	service.Register(AnswererFunc(func(*AskRequest, harness.Context) (*Answer, error) {
		called = true
		return validAnswer(), nil
	}))
	if code := askCode(t, service, validRequest(), ctx); code != CodeAskAborted {
		t.Fatalf("code = %s", code)
	}
	if called {
		t.Fatal("an aborted context must not reach the chain")
	}
}

func TestAskAbortedAfterAnswererReturnWins(t *testing.T) {
	controller := abort.NewController()
	ctx := chordcontext.WithAbortSignal(controller.Signal(), harness.BackgroundContext)
	service := NewService()
	service.Register(AnswererFunc(func(*AskRequest, harness.Context) (*Answer, error) {
		controller.Abort()
		return validAnswer(), nil
	}))
	if code := askCode(t, service, validRequest(), ctx); code != CodeAskAborted {
		t.Fatalf("code = %s", code)
	}
}

func TestRegisterDisposerIsIdempotent(t *testing.T) {
	service := NewService()
	remove := service.Register(AnswererFunc(func(*AskRequest, harness.Context) (*Answer, error) {
		return nil, ErrDeclined
	}))
	service.Register(AnswererFunc(func(*AskRequest, harness.Context) (*Answer, error) {
		return validAnswer(), nil
	}))
	remove()
	remove()
	answer, err := service.Ask(validRequest(), harness.BackgroundContext)
	if err != nil || answer == nil {
		t.Fatalf("answer = %v err = %v", answer, err)
	}
}

func TestAskSnapshotsTheChain(t *testing.T) {
	service := NewService()
	entered := make(chan struct{})
	release := make(chan struct{})
	removeBlocker := service.Register(AnswererFunc(func(*AskRequest, harness.Context) (*Answer, error) {
		close(entered)
		<-release
		return validAnswer(), nil
	}))
	results := make(chan *Answer, 1)
	failures := make(chan error, 1)
	go func() {
		answer, err := service.Ask(validRequest(), harness.BackgroundContext)
		results <- answer
		failures <- err
	}()
	<-entered
	removeLate := service.Register(AnswererFunc(func(*AskRequest, harness.Context) (*Answer, error) {
		return &Answer{Items: []AnswerItem{
			{QuestionID: "confirm", Labels: []string{"no"}},
			{QuestionID: "name", Labels: []string{"late"}},
		}}, nil
	}))
	close(release)
	if err := <-failures; err != nil {
		t.Fatal(err)
	}
	if first := <-results; first.Items[1].Labels[0] != "openagent" {
		t.Fatalf("the in-flight ask must not see the late registration: %+v", first)
	}
	removeBlocker()
	second, err := service.Ask(validRequest(), harness.BackgroundContext)
	if err != nil {
		t.Fatal(err)
	}
	if second.Items[1].Labels[0] != "late" {
		t.Fatalf("the next ask must see the late registration: %+v", second)
	}
	removeLate()
	if code := askCode(t, service, validRequest(), harness.BackgroundContext); code != CodeNoAnswerer {
		t.Fatalf("after both removals code = %s", code)
	}
}
