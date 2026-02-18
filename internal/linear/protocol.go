package linear

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const AgentSessionEventPayloadVersion1 = 1

var (
	ErrUnsupportedAgentSessionEventPayloadVersion = errors.New("unsupported AgentSessionEvent payloadVersion")
	ErrPromptedEventRequiresPromptActivity        = errors.New("prompted AgentSessionEvent requires prompt agentActivity")
	ErrActivityBodyRequired                       = errors.New("activity body is required")
	ErrActionLabelRequired                        = errors.New("action label is required")
	ErrActionParameterRequired                    = errors.New("action parameter is required")
	ErrUnknownAgentSessionEventAction             = errors.New("unknown AgentSessionEvent action")
	ErrUnknownAgentActivityContentType            = errors.New("unknown AgentActivity content type")
	ErrUnknownAgentSessionState                   = errors.New("unknown AgentSession state")
)

type AgentSessionEventAction string

const (
	AgentSessionEventActionCreated  AgentSessionEventAction = "created"
	AgentSessionEventActionPrompted AgentSessionEventAction = "prompted"
)

type AgentSessionState string

const (
	AgentSessionStatePending       AgentSessionState = "pending"
	AgentSessionStateActive        AgentSessionState = "active"
	AgentSessionStateAwaitingInput AgentSessionState = "awaitingInput"
	AgentSessionStateComplete      AgentSessionState = "complete"
	AgentSessionStateError         AgentSessionState = "error"
)

type AgentActivityContentType string

const (
	AgentActivityContentTypeThought     AgentActivityContentType = "thought"
	AgentActivityContentTypeElicitation AgentActivityContentType = "elicitation"
	AgentActivityContentTypeAction      AgentActivityContentType = "action"
	AgentActivityContentTypeResponse    AgentActivityContentType = "response"
	AgentActivityContentTypeError       AgentActivityContentType = "error"
	AgentActivityContentTypePrompt      AgentActivityContentType = "prompt"
)

type AgentSessionEvent struct {
	ID               string                  `json:"id"`
	Type             string                  `json:"type"`
	Action           AgentSessionEventAction `json:"action"`
	CreatedAt        time.Time               `json:"createdAt"`
	PayloadVersion   int                     `json:"payloadVersion"`
	AgentSession     AgentSession            `json:"agentSession"`
	AgentActivity    *AgentActivity          `json:"agentActivity,omitempty"`
	PreviousComments []AgentComment          `json:"previousComments,omitempty"`
}

type AgentSession struct {
	ID            string             `json:"id"`
	State         AgentSessionState  `json:"state"`
	PromptContext string             `json:"promptContext"`
	ExternalURLs  []AgentExternalURL `json:"externalUrls,omitempty"`
	Issue         *AgentIssue        `json:"issue,omitempty"`
	Comment       *AgentComment      `json:"comment,omitempty"`
	Guidance      *AgentGuidance     `json:"guidance,omitempty"`
}

type AgentExternalURL struct {
	Label string `json:"label"`
	URL   string `json:"url"`
}

type AgentIssue struct {
	ID         string `json:"id"`
	Identifier string `json:"identifier,omitempty"`
	Title      string `json:"title,omitempty"`
}

type AgentComment struct {
	ID   string `json:"id"`
	Body string `json:"body"`
}

type AgentGuidance struct {
	Body string `json:"body"`
}

type AgentActivity struct {
	ID      string               `json:"id"`
	Content AgentActivityContent `json:"content"`
}

type AgentActivityContent struct {
	Type      AgentActivityContentType `json:"type"`
	Body      string                   `json:"body,omitempty"`
	Action    string                   `json:"action,omitempty"`
	Parameter string                   `json:"parameter,omitempty"`
	Result    *string                  `json:"result,omitempty"`
}

func DecodeAgentSessionEvent(payload []byte) (AgentSessionEvent, error) {
	var event AgentSessionEvent
	if err := json.Unmarshal(payload, &event); err != nil {
		return AgentSessionEvent{}, fmt.Errorf("decode AgentSessionEvent: %w", err)
	}
	if err := event.Validate(); err != nil {
		return AgentSessionEvent{}, err
	}
	return event, nil
}

func (e AgentSessionEvent) Validate() error {
	if e.PayloadVersion != AgentSessionEventPayloadVersion1 {
		return fmt.Errorf("%w: %d", ErrUnsupportedAgentSessionEventPayloadVersion, e.PayloadVersion)
	}

	switch e.Action {
	case AgentSessionEventActionCreated, AgentSessionEventActionPrompted:
	default:
		return fmt.Errorf("%w: %q", ErrUnknownAgentSessionEventAction, e.Action)
	}

	if !isKnownAgentSessionState(e.AgentSession.State) {
		return fmt.Errorf("%w: %q", ErrUnknownAgentSessionState, e.AgentSession.State)
	}

	if e.Action == AgentSessionEventActionPrompted {
		if e.AgentActivity == nil || e.AgentActivity.Content.Type != AgentActivityContentTypePrompt {
			return ErrPromptedEventRequiresPromptActivity
		}
	}

	if e.AgentActivity != nil {
		if err := ValidateAgentActivityContent(e.AgentActivity.Content); err != nil {
			return err
		}
	}

	return nil
}

func ValidateAgentActivityContent(content AgentActivityContent) error {
	switch content.Type {
	case AgentActivityContentTypeThought,
		AgentActivityContentTypeElicitation,
		AgentActivityContentTypeResponse,
		AgentActivityContentTypeError,
		AgentActivityContentTypePrompt:
		if strings.TrimSpace(content.Body) == "" {
			return ErrActivityBodyRequired
		}
	case AgentActivityContentTypeAction:
		if strings.TrimSpace(content.Action) == "" {
			return ErrActionLabelRequired
		}
		if strings.TrimSpace(content.Parameter) == "" {
			return ErrActionParameterRequired
		}
	default:
		return fmt.Errorf("%w: %q", ErrUnknownAgentActivityContentType, content.Type)
	}
	return nil
}

func SessionStateForActivityType(contentType AgentActivityContentType) AgentSessionState {
	switch contentType {
	case AgentActivityContentTypeElicitation:
		return AgentSessionStateAwaitingInput
	case AgentActivityContentTypeResponse:
		return AgentSessionStateComplete
	case AgentActivityContentTypeError:
		return AgentSessionStateError
	case AgentActivityContentTypeThought,
		AgentActivityContentTypeAction,
		AgentActivityContentTypePrompt:
		return AgentSessionStateActive
	default:
		return AgentSessionStateActive
	}
}

func isKnownAgentSessionState(state AgentSessionState) bool {
	switch state {
	case AgentSessionStatePending,
		AgentSessionStateActive,
		AgentSessionStateAwaitingInput,
		AgentSessionStateComplete,
		AgentSessionStateError:
		return true
	default:
		return false
	}
}
