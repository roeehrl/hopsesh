// Package packaging exposes the release trust root used by verified installers.
package packaging

import _ "embed"

//go:embed release-key.pub
var ReleasePublicKey string
