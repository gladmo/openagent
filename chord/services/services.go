// Package services ports @gladmo/chord/src/services: the service
// registry surface pi consumes (defineService, RemoteServiceError, the
// provider/consumer halves riding the wire codec).
package services

import "fmt"

// RemoteServiceErrorCode mirrors the TS union.
type RemoteServiceErrorCode string

// The remote service error codes.
const (
	ErrServiceNotAllowed     RemoteServiceErrorCode = "service_not_allowed"
	ErrServiceNotFound       RemoteServiceErrorCode = "service_not_found"
	ErrServiceModeMismatch   RemoteServiceErrorCode = "service_mode_mismatch"
	ErrServiceMemberNotFound RemoteServiceErrorCode = "service_member_not_found"
	ErrServiceMemberMismatch RemoteServiceErrorCode = "service_member_mismatch"
	ErrInstanceNotFound      RemoteServiceErrorCode = "service_instance_not_found"
	ErrStaleInstance         RemoteServiceErrorCode = "service_stale_instance"
	ErrInvalidValue          RemoteServiceErrorCode = "service_invalid_value"
)

// RemoteServiceErrorCodes mirrors REMOTE_SERVICE_ERROR_CODES.
var RemoteServiceErrorCodes = []RemoteServiceErrorCode{
	ErrServiceNotAllowed, ErrServiceNotFound, ErrServiceModeMismatch,
	ErrServiceMemberNotFound, ErrServiceMemberMismatch, ErrInstanceNotFound,
	ErrStaleInstance, ErrInvalidValue,
}

// IsRemoteServiceErrorCode mirrors the TS guard.
func IsRemoteServiceErrorCode(value string) bool {
	for _, code := range RemoteServiceErrorCodes {
		if code == RemoteServiceErrorCode(value) {
			return true
		}
	}
	return false
}

// RemoteServiceError mirrors the TS error.
type RemoteServiceError struct {
	Code    RemoteServiceErrorCode
	Message string
}

func (e *RemoteServiceError) Error() string { return e.Message }

// NewRemoteServiceError builds the error.
func NewRemoteServiceError(code RemoteServiceErrorCode, message string) *RemoteServiceError {
	return &RemoteServiceError{Code: code, Message: message}
}

// ServiceMode mirrors the service mode union.
type ServiceMode string

// Service modes.
const (
	ServiceModeLocal  ServiceMode = "local"
	ServiceModeRemote ServiceMode = "remote"
	ServiceModeBoth   ServiceMode = "both"
)

// ServiceDefinition carries one named service contract.
type ServiceDefinition struct {
	Name string
	Mode ServiceMode
}

// DefineService mirrors defineService: registering a named contract with
// its mode (local-only services like pi.harness reject remote access).
func DefineService(name string, options struct{ Local bool }) ServiceDefinition {
	mode := ServiceModeBoth
	if options.Local {
		mode = ServiceModeLocal
	}
	return ServiceDefinition{Name: name, Mode: mode}
}

// Registry resolves definitions by name.
type Registry struct {
	definitions map[string]ServiceDefinition
}

// NewRegistry builds an empty registry.
func NewRegistry() *Registry {
	return &Registry{definitions: map[string]ServiceDefinition{}}
}

// Register installs one definition.
func (r *Registry) Register(definition ServiceDefinition) error {
	if _, exists := r.definitions[definition.Name]; exists {
		return fmt.Errorf("service %s registered twice", definition.Name)
	}
	r.definitions[definition.Name] = definition
	return nil
}

// Resolve looks one definition up.
func (r *Registry) Resolve(name string) (ServiceDefinition, bool) {
	definition, ok := r.definitions[name]
	return definition, ok
}

// ServiceSlot mirrors the host-owned mutable target: bind/unbind the
// implementation; consumers resolve members through guarded access.
type ServiceSlot struct {
	serviceID      string
	implementation any
}

// NewServiceSlot builds an unbound slot.
func NewServiceSlot(serviceID string) *ServiceSlot {
	return &ServiceSlot{serviceID: serviceID}
}

// Bind attaches the implementation.
func (s *ServiceSlot) Bind(implementation any) { s.implementation = implementation }

// Unbind detaches it.
func (s *ServiceSlot) Unbind() { s.implementation = nil }

// Resolve reads one member after the access assertion; a disconnected
// service errors.
func (s *ServiceSlot) Resolve(member string, assertAccess func() error) (any, error) {
	if err := assertAccess(); err != nil {
		return nil, err
	}
	if s.implementation == nil {
		return nil, fmt.Errorf("Service %s is disconnected", s.serviceID)
	}
	impl, ok := s.implementation.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("Service %s is disconnected", s.serviceID)
	}
	value, ok := impl[member]
	if !ok {
		return nil, NewRemoteServiceError(ErrServiceMemberNotFound, fmt.Sprintf("Service %s has no member %s", s.serviceID, member))
	}
	return value, nil
}
