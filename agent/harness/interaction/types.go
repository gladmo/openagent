package interaction

// types.go holds the ask-user-question vocabulary and error taxonomy:
// the Service Definition's contract types. Vocabulary only — the chain
// behavior lives in service.go, the model-facing consumer in tool.go.

import "fmt"

// Option is one selectable choice of a question.
type Option struct {
	// Label is the choice text the answer references.
	Label string
	// Description explains the tradeoff or impact (optional).
	Description string
}

// Question is one question asked to the human.
type Question struct {
	// ID identifies the question within one ask call; answers reference
	// it. Non-empty and unique within the request.
	ID string
	// Header is a short heading shown above the question.
	Header string
	// Question is the question text.
	Question string
	// Options enumerates the choices; empty allows a free-text answer.
	Options []Option
	// MultiSelect permits several labels in the answer item; requires
	// options.
	MultiSelect bool
}

// AskRequest is one ask call.
type AskRequest struct {
	Questions []Question
}

// AnswerItem answers exactly one question by ID.
type AnswerItem struct {
	QuestionID string
	// Labels are the chosen option labels, or the typed text for a
	// free-text question.
	Labels []string
}

// Answer answers every question of a request exactly once.
type Answer struct {
	Items []AnswerItem
}

// Interaction error codes.
const (
	CodeEmptyQuestions      = "EMPTY_QUESTIONS"
	CodeBadQuestion         = "BAD_QUESTION"
	CodeDuplicateQuestionID = "DUPLICATE_QUESTION_ID"
	CodeBadAnswer           = "BAD_ANSWER"
	CodeNoAnswerer          = "NO_ANSWERER"
	CodeAskAborted          = "ASK_ABORTED"
)

// InteractionError is the stable failure taxonomy; Code crosses the
// seam (tool error text, embedder switches).
type InteractionError struct {
	Code    string
	Message string
}

func (e *InteractionError) Error() string {
	return fmt.Sprintf("%s [%s]", e.Message, e.Code)
}

// AskAborted builds the aborted failure.
func AskAborted() *InteractionError {
	return &InteractionError{
		Code:    CodeAskAborted,
		Message: "ask_user_question was aborted before the user answered",
	}
}

// ValidateAskRequest checks the request shape: at least one question,
// each with a non-empty id and question text, ids unique.
func ValidateAskRequest(request *AskRequest) error {
	if request == nil || len(request.Questions) == 0 {
		return &InteractionError{
			Code:    CodeEmptyQuestions,
			Message: "ask_user_question requires at least one question",
		}
	}
	seen := make(map[string]bool, len(request.Questions))
	for _, question := range request.Questions {
		if question.ID == "" || question.Question == "" {
			return &InteractionError{
				Code:    CodeBadQuestion,
				Message: "every question needs a non-empty id and question text",
			}
		}
		if seen[question.ID] {
			return &InteractionError{
				Code:    CodeDuplicateQuestionID,
				Message: fmt.Sprintf("question id %q appears more than once", question.ID),
			}
		}
		seen[question.ID] = true
	}
	return nil
}

// ValidateAnswer checks that the answer names each request question
// exactly once with admissible labels: option questions accept only
// known labels (one, or several with multiSelect, without repeats);
// free-text questions accept exactly one non-empty label.
func ValidateAnswer(request *AskRequest, answer *Answer) error {
	if answer == nil {
		return &InteractionError{
			Code:    CodeBadAnswer,
			Message: "the answer is missing",
		}
	}
	byID := make(map[string]*Question, len(request.Questions))
	for i := range request.Questions {
		byID[request.Questions[i].ID] = &request.Questions[i]
	}
	seen := make(map[string]bool, len(answer.Items))
	for _, item := range answer.Items {
		question, known := byID[item.QuestionID]
		if !known {
			return &InteractionError{
				Code:    CodeBadAnswer,
				Message: fmt.Sprintf("the answer names unknown question %q", item.QuestionID),
			}
		}
		if seen[item.QuestionID] {
			return badAnswer(item.QuestionID, "is named more than once")
		}
		seen[item.QuestionID] = true
		if len(item.Labels) == 0 {
			return badAnswer(item.QuestionID, "carries no labels")
		}
		if len(question.Options) == 0 {
			if len(item.Labels) != 1 || item.Labels[0] == "" {
				return badAnswer(item.QuestionID, "requires exactly one non-empty free-text label")
			}
			continue
		}
		allowed := make(map[string]bool, len(question.Options))
		for _, option := range question.Options {
			allowed[option.Label] = true
		}
		chosen := make(map[string]bool, len(item.Labels))
		for _, label := range item.Labels {
			if !allowed[label] {
				return badAnswer(item.QuestionID, fmt.Sprintf("label %q is not one of its options", label))
			}
			if chosen[label] {
				return badAnswer(item.QuestionID, fmt.Sprintf("repeats label %q", label))
			}
			chosen[label] = true
		}
		if !question.MultiSelect && len(item.Labels) > 1 {
			return badAnswer(item.QuestionID, "is not multiSelect but carries several labels")
		}
	}
	if len(seen) != len(byID) {
		return &InteractionError{
			Code: CodeBadAnswer,
			Message: fmt.Sprintf("the answer must name each of the %d questions exactly once; %d named",
				len(byID), len(seen)),
		}
	}
	return nil
}

func badAnswer(questionID, reason string) *InteractionError {
	return &InteractionError{
		Code:    CodeBadAnswer,
		Message: fmt.Sprintf("the answer for question %q %s", questionID, reason),
	}
}
