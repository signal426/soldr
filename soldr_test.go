package soldr

import (
	"context"
	"errors"
	"fmt"
	"testing"

	proplv1 "buf.build/gen/go/signal426/propl/protocolbuffers/go/propl/v1"
	"github.com/stretchr/testify/assert"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
)

type MyErrResultHandler struct{}

func (my MyErrResultHandler) HandleErrs(errs []Fault) error {
	var errString string
	for _, err := range errs {
		errString += fmt.Sprintf("%s: %s\n", err.Field, err.Err)
	}
	return errors.New(errString)
}

func TestFieldPolicies(t *testing.T) {
	t.Run("it should validate non-zero", func(t *testing.T) {
		// arrange
		req := &proplv1.CreateUserRequest{
			User: &proplv1.User{},
		}

		p := ForSubject(req).
			AssertNonZero("user", req.GetUser()).
			AssertNonZero("user.first_name", req.GetUser().GetFirstName())

		// act
		err := p.E(context.Background())

		// assert
		assert.Error(t, err)
	})

	t.Run("it should evaluate a custom function", func(t *testing.T) {
		// arrange
		req := &proplv1.CreateUserRequest{
			User: &proplv1.User{
				FirstName: "Bob",
			},
		}

		p := ForSubject(req).
			AssertCustom("user.first_name", func(ctx context.Context, msg *proplv1.CreateUserRequest) error {
				if msg.GetUser().GetFirstName() == "Bob" {
					return errors.New("can't be bob")
				}
				return nil
			})

		// act
		err := p.E(context.Background())

		// assert
		assert.Error(t, err)
	})

	t.Run("it should validate a complex structure", func(t *testing.T) {
		// arrange
		req := &proplv1.UpdateUserRequest{
			User: &proplv1.User{
				FirstName: "bob",
				PrimaryAddress: &proplv1.Address{
					Line1: "a",
					Line2: "b",
				},
			},
			UpdateMask: &fieldmaskpb.FieldMask{
				Paths: []string{"first_name", "last_name"},
			},
		}

		p := ForSubject(req, req.GetUpdateMask().Paths...).
			AssertNonZero("user.id", req.GetUser().GetId()).
			AssertNonZero("some.fake", nil).
			AssertNonZeroWhenInMask("user.last_name", req.GetUser().GetLastName()).
			AssertNonZeroWhenInMask("user.primary_address", req.GetUser().GetPrimaryAddress()).
			AssertNonZeroWhenInMask("user.primary_address.line1", req.GetUser().GetPrimaryAddress().GetLine1())

		// act
		err := p.E(context.Background())

		// assert
		assert.Error(t, err)
	})

	t.Run("it should validate custom optional action", func(t *testing.T) {
		// arrange
		req := &proplv1.UpdateUserRequest{
			User: &proplv1.User{
				FirstName: "bob",
				PrimaryAddress: &proplv1.Address{
					Line1: "a",
					Line2: "b",
				},
			},
			UpdateMask: &fieldmaskpb.FieldMask{
				Paths: []string{"first_name", "last_name", "line1"},
			},
		}

		// ForSubject(request, options...) instantiates the evaluator
		p := ForSubject(req).
			// Specify all of the field paths that should not be equal to their zero value
			AssertNonZero("user.id", req.GetUser().GetId()).
			AssertNonZero("some.fake", nil).
			AssertNonZeroWhenInMask("user.first_name", req.GetUser().GetFirstName()).
			AssertNonZeroWhenInMask("user.last_name", req.GetUser().GetLastName()).
			AssertNonZeroWhenInMask("user.primary_address", req.GetUser().GetPrimaryAddress()).
			AssertCustomWhenInMask("user.primary_addres.line1", func(ctx context.Context, msg *proplv1.UpdateUserRequest) error {
				if req.GetUser().GetPrimaryAddress().GetLine1() == "a" {
					return errors.New("cannot be a")
				}
				return nil
			})

		// act
		// call this before running the evaluation in order to substitute your own error result handler
		// to do things like custom formatting
		err := p.E(context.Background())

		// assert
		assert.Error(t, err)
	})

	t.Run("it should run a custom action before request field validation", func(t *testing.T) {
		// arrange
		authorizeUpdate := func(userId string) error {
			if userId != "abc123" {
				return errors.New("can only update user abc123")
			}
			return nil
		}

		req := &proplv1.UpdateUserRequest{
			User: &proplv1.User{
				FirstName: "bob",
				PrimaryAddress: &proplv1.Address{
					Line1: "a",
					Line2: "b",
				},
			},
			UpdateMask: &fieldmaskpb.FieldMask{
				Paths: []string{"first_name", "last_name"},
			},
		}

		p := ForSubject(req, req.GetUpdateMask().Paths...).
			BeforeValidation(func(ctx context.Context, msg *proplv1.UpdateUserRequest) error {
				return authorizeUpdate(msg.GetUser().GetId())
			}).
			AssertNonZero("user.id", req.GetUser().GetId()).
			AssertNonZero("some.fake", nil).
			AssertNonZeroWhenInMask("user.first_name", req.GetUser().GetFirstName()).
			AssertNonZeroWhenInMask("user.last_name", req.GetUser().GetLastName()).
			AssertNonZeroWhenInMask("user.primary_address", req.GetUser().GetPrimaryAddress())

		// act
		err := p.E(context.Background())

		// assert
		assert.Error(t, err)
	})

	t.Run("it should run a custom action if validation successful", func(t *testing.T) {
		// arrange
		authorizeUpdate := func(_ context.Context, userId string) error {
			if userId != "abc123" {
				return errors.New("can only update user abc123")
			}
			return nil
		}

		doLogic := func(_ context.Context, msg *proplv1.UpdateUserRequest) error {
			if msg.GetUser().GetLastName() == "" {
				msg.GetUser().LastName = "NA"
			}
			return nil
		}

		req := &proplv1.UpdateUserRequest{
			User: &proplv1.User{
				FirstName: "bob",
				Id:        "123abc",
				PrimaryAddress: &proplv1.Address{
					Line1: "a",
					Line2: "b",
				},
			},
			UpdateMask: &fieldmaskpb.FieldMask{
				Paths: []string{"first_name", "last_name"},
			},
		}

		p := ForSubject(req, req.GetUpdateMask().Paths...).
			BeforeValidation(func(ctx context.Context, msg *proplv1.UpdateUserRequest) error {
				return authorizeUpdate(ctx, msg.GetUser().GetId())
			}).
			AssertNonZero("user.id", req.GetUser().GetId()).
			AssertNonZeroWhenInMask("user.first_name", req.GetUser().GetFirstName()).
			AssertNonZeroWhenInMask("user.last_name", req.GetUser().GetLastName()).
			AssertNonZeroWhenInMask("user.primary_address", req.GetUser().GetPrimaryAddress()).
			OnSuccess(func(ctx context.Context, msg *proplv1.UpdateUserRequest) error {
				return doLogic(ctx, msg)
			})

		// act
		err := p.E(context.Background())

		// assert
		assert.NoError(t, err)
		assert.Equal(t, req.GetUser().GetLastName(), "NA")
	})
}
