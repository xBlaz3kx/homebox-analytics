package mcp

import (
	"errors"

	"go.uber.org/zap"

	"github.com/xBlaz3kx/homebox-analytics/internal/pkg/homebox"
	"github.com/xBlaz3kx/homebox-analytics/internal/service/analytics"
	"github.com/xBlaz3kx/homebox-analytics/internal/service/catalog"
	"github.com/xBlaz3kx/homebox-analytics/internal/service/ledger"
)

var errInternal = errors.New("internal error")

// toolError keeps errors the model can act on (invalid input, unknown item, not enough stock) and
// hides everything else behind a generic message. The SDK turns the returned error into an
// isError tool result.
func (s *server) toolError(tool string, err error) error {
	var (
		ledgerVE *ledger.ValidationError
		chartVE  *analytics.ValidationError
		inputVE  *inputError
	)
	switch {
	case errors.As(err, &ledgerVE), errors.As(err, &chartVE), errors.As(err, &inputVE),
		errors.Is(err, catalog.ErrEmptyQuery),
		errors.Is(err, catalog.ErrNotFound), errors.Is(err, ledger.ErrNotFound), errors.Is(err, homebox.ErrNotFound),
		errors.Is(err, ledger.ErrInsufficientQuantity):
		return err
	}
	s.logger.Error("MCP tool failed", zap.String("tool", tool), zap.Error(err))
	return errInternal
}

// inputError marks invalid tool arguments not caught by schema validation.
type inputError struct{ msg string }

func (e *inputError) Error() string { return e.msg }
