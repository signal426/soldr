package soldr

import (
	"context"
	"reflect"

	"google.golang.org/protobuf/proto"
)

const (
	prevalErr  = "the message failed prevalidation"
	postValErr = "the message failed postvalidation"
)

// output format of the errors
type Format uint32

const (
	Default Format = iota
	JSON
)

type Subject[T proto.Message] struct {
	// custom actions to run before fields are evaluated
	// any error from a pre-field eval returns early
	initAction Action[T]

	// custom actions to run after fields are evaluated
	successAction Action[T]

	// custom action to run regardless if an error occurred
	postAction Action[T]

	// policy manager for executing policies
	pm *policyManager[T]

	// the handler for the faults
	fh FaultHandler

	// paths is list of fields that are being evaluated if a field mask is supplied
	paths map[string]struct{}

	// the field store processor that accepts field labels and returns information about the field if it exists
	fieldProcessor *fieldProcessor

	// the message we are processing
	message T
}

// For creates a new policy aggregate for the specified message that can be built upon using the
// builder methods.
func ForSubject[T proto.Message](subject T, fieldMask ...string) *Subject[T] {
	return &Subject[T]{
		fieldProcessor: newFieldProcessor(),
		paths:          getPathsFromMask(fieldMask...),
		pm:             NewPolicyManager[T](),
		message:        subject,
	}
}

func getPathsFromMask(fieldMask ...string) map[string]struct{} {
	if fieldMask == nil || len(fieldMask) == 0 {
		return nil
	}
	paths := make(map[string]struct{})
	for _, f := range fieldMask {
		paths[f] = struct{}{}
	}
	return paths
}

func isZero(i interface{}) bool {
	return i == nil || reflect.ValueOf(i).IsZero()
}

func (p *Subject[T]) isFieldSet(i interface{}, path string, isForAction bool) bool {
	// no value to check on a custom action, so we have to source the trigger for the eval
	if isForAction {
		return p.fieldProcessor.isFieldSet(path, p.message)
	}

	// else, check the val first to see if it's set and if not check the message
	ifs := isZero(i)
	if !ifs {
		return p.fieldProcessor.isFieldSet(path, p.message)
	}
	return ifs
}

// HasNonZeroField pass in a list of fields that must not be equal to their
// zero value
//
// example: sue := HasNonZeroFields("user.id", "user.first_name")
func (p *Subject[T]) AssertNonZero(path string, value interface{}) *Subject[T] {
	// check if field is in mask
	inMask := p.isFieldInMask(path)

	// create a new field policy subject
	field := NewField(path, value, inMask, p.isFieldSet(value, path, false))

	// create the trait policy for the field
	traitPolicy := NewPolicy(NotZeroTrait(), field.MustBeSet, field)

	// add the policy to our manager
	p.pm.AddTraitPolicy(traitPolicy)
	return p
}

// HasNonZeroFieldsWhen pass in a list of field conditions if you want to customize the conditions under which
// a field non-zero evaluation is triggered
//
// example: sue := HasNonZeroFieldsWhen(IfInMask("user.first_name"), Always("user.first_name"))
func (p *Subject[T]) AssertNonZeroWhenInMask(path string, value interface{}) *Subject[T] {
	// check if field is in mask
	inMask := p.isFieldInMask(path)

	// create a new field policy subject
	field := NewField(path, value, inMask, p.isFieldSet(value, path, false))

	// create the trait policy for the field
	traitPolicy := NewPolicy(NotZeroTrait(), field.MustBeSetIfInMask, field)

	// add the policy to our manager
	p.pm.AddTraitPolicy(traitPolicy)
	return p
}

// HasCustomEvaluation sets the specified evaluation on the field and will be run if the conditions are met.
func (p *Subject[T]) AssertCustom(path string, action Action[T]) *Subject[T] {
	// check if field is in mask
	inMask := p.isFieldInMask(path)

	// create a policy subject
	field := NewField(path, nil, inMask, p.isFieldSet(nil, path, true))

	// create a policy
	actionPolicy := NewActionPolicy(field.MustBeSet, field, action)

	// add the policy to our manager
	p.pm.AddActionPolicy(actionPolicy)
	return p
}

// HasCustomEvaluationWhen sets the specified evaluation on the field and will be run if the conditions are met
func (p *Subject[T]) AssertCustomWhenInMask(path string, action Action[T]) *Subject[T] {
	// check if field is in mask
	inMask := p.isFieldInMask(path)

	// create a policy subject
	field := NewField(path, nil, inMask, p.isFieldSet(nil, path, true))

	// create a new action policy to evaluate
	actionPolicy := NewActionPolicy(field.MustBeSetIfInMask, field, action)

	// add the policy to our manager
	p.pm.AddActionPolicy(actionPolicy)
	return p
}

func (p *Subject[T]) BeforeValidation(act Action[T]) *Subject[T] {
	p.initAction = act
	return p
}

func (p *Subject[T]) OnSuccess(act Action[T]) *Subject[T] {
	p.successAction = act
	return p
}

// CustomErrResultHandler call this before calling E() or Evaluate() if you want to override
// the errors that are output from the policy execution
func (s *Subject[T]) CustomFaultHandler(e FaultHandler) *Subject[T] {
	s.fh = e
	return s
}

func (s *Subject[T]) isFieldInMask(path string) bool {
	if s.paths == nil {
		return false
	}
	_, inMask := s.paths[path]
	return inMask
}

// E shorthand for Evaluate
func (s *Subject[T]) E(ctx context.Context) error {
	return s.Evaluate(ctx)
}

func (s *Subject[T]) init(ctx context.Context) *Fault {
	// evaluate the global pre-checks
	if s.initAction != nil {
		if err := s.initAction(ctx, s.message); err != nil {
			return RequestFault(err)
		}
	}

	return nil
}

func (s *Subject[T]) onSuccess(ctx context.Context) *Fault {
	// evaluate the global pre-checks
	if s.successAction != nil {
		if err := s.successAction(ctx, s.message); err != nil {
			return RequestFault(err)
		}
	}

	return nil
}

func (s *Subject[T]) postValidation(ctx context.Context) *Fault {
	// evaluate the global pre-checks
	if s.postAction != nil {
		if err := s.postAction(ctx, s.message); err != nil {
			return RequestFault(err)
		}
	}

	return nil
}

func (s *Subject[T]) err(f []*Fault) error {
	if s.fh == nil {
		s.fh = newDefaultFaultHandler()
	}
	if len(f) == 0 {
		return nil
	}
	return s.fh.ToError(f)
}

// Evaluate checks each declared policy and returns an error describing
// each infraction. If a precheck is specified and returns an error, this exits
// and field policies are not evaluated.
//
// To use your own infractionsHandler, specify a handler using WithInfractionsHandler.
func (s *Subject[T]) Evaluate(ctx context.Context) error {
	// if any pre-field-eval actions are set, run them
	if err := s.init(ctx); err != nil {
		return s.fh.ToError([]*Fault{err})
	}

	// assert field traits based on their condition in the message
	faults := []*Fault{}
	allFaults := s.pm.ExecuteAllPolicies(ctx, s.message)
	for subject, fault := range allFaults {
		faults = append(faults, FieldFault(subject, fault))
	}

	if len(faults) == 0 {
		if successFault := s.onSuccess(ctx); successFault != nil {
			faults = append(faults, successFault)
		}
	}

	return s.err(faults)
}
