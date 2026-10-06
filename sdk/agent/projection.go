package agent

import "github.com/roeehrl/hopsesh/sdk/ir"

// SanitizedProjection maps native nodes after the module's own Sanitizer dropped
// account-bound records. The module must prove its rewrite retained native order.
type SanitizedProjection interface {
	ProjectSanitized(source, target ir.Segment) ([]ir.Projection, error)
}
