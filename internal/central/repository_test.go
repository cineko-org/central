package central

import (
	"errors"
	"strings"
	"testing"
)

func TestPublicErrorContract(t *testing.T) {
	t.Parallel()
	err := InvalidRequest(" theater id is required ")
	var public *PublicError
	if !errors.As(err, &public) || !errors.Is(err, ErrInvalid) {
		t.Fatalf("public validation error = %v", err)
	}
	if public.PublicMessage() != "theater id is required" ||
		!strings.Contains(public.Error(), "theater id is required") || !errors.Is(public.Unwrap(), ErrInvalid) {
		t.Fatalf("public validation fields = %q, %q, %v", public.PublicMessage(), public.Error(), public.Unwrap())
	}
	if empty := InvalidRequest(" "); !errors.Is(empty, ErrInvalid) {
		t.Fatalf("empty public validation error = %v", empty)
	}
}
